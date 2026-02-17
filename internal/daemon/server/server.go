// Package server implements the gRPC server for the daemon.
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/prompts"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/daemon/tray"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// Server is the daemon's gRPC server with gRPC-Web support.
type Server struct {
	grpcServer     *grpc.Server
	grpcWebWrapper *grpcweb.WrappedGrpcServer
	httpServer     *http.Server
	listener       net.Listener
	port           int
	projectManager *project.Manager
	taskManager    *task.Manager
	agentManager   *agent.Manager
	watcher        *watcher.Watcher
	updateState    UpdateState
}

// New creates a new server listening on the specified port.
// Pass port 0 for dynamic allocation.
func New(port int) (*Server, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.TODO(), "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Get actual port if dynamically allocated
	actualPort := listener.Addr().(*net.TCPAddr).Port

	grpcServer := grpc.NewServer()

	// Create managers
	projectMgr := project.NewManager()
	taskMgr := task.NewManager()
	agentMgr := agent.NewManager()

	// Create and start file watcher
	w, err := watcher.New()
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}
	if err := w.Start(); err != nil {
		listener.Close()
		return nil, fmt.Errorf("failed to start watcher: %w", err)
	}

	// Auto-watch all registered projects
	if index, err := config.LoadProjectsIndex(); err == nil {
		for _, entry := range index.Projects {
			if watchErr := w.WatchProject(entry.ProjectID, entry.Path); watchErr != nil {
				log.Printf("Warning: failed to watch project %s: %v", entry.Name, watchErr)
			}
		}
	}

	// Wire on-task-done callback for git merge + worktree cleanup.
	// Returns true if chaining should continue, false to stop (e.g. merge conflict).
	agentMgr.SetOnTaskDoneFn(func(projectPath string, taskNumber int, worktreePath string) bool {
		if taskNumber == 0 || worktreePath == "" {
			log.Printf("[merge] Skipping merge: taskNumber=%d worktreePath=%q", taskNumber, worktreePath)
			return true
		}
		proj, err := config.LoadProject(projectPath)
		if err != nil {
			log.Printf("[merge] Failed to load project for task #%04d: %v", taskNumber, err)
			return false
		}

		t, err := config.LoadTask(projectPath, taskNumber)
		if err != nil || t == nil {
			log.Printf("[merge] Failed to load task #%04d: %v", taskNumber, err)
			return false
		}
		if t.Status != models.TaskStatusDone {
			log.Printf("[merge] Task #%04d not done (status: %s), skipping merge", taskNumber, t.Status)
			return true
		}
		log.Printf("[merge] Task #%04d done, proceeding with merge (auto_merge=%v, auto_delete=%v)", taskNumber, proj.AutoMerge, proj.AutoDeleteBranch)

		var merged bool
		mergeFailed := false
		if proj.AutoMerge {
			var err error
			merged, err = agent.MergeWorktree(projectPath, taskNumber, proj.DefaultBranch)
			if err != nil {
				log.Printf("[merge] Auto-merge failed for task #%04d: %v", taskNumber, err)
				mergeFailed = true
			} else if merged {
				log.Printf("[merge] Auto-merged task #%04d to %s", taskNumber, proj.DefaultBranch)
			} else {
				log.Printf("[merge] Task #%04d has no file differences — skipped merge", taskNumber)
			}
		}
		if proj.AutoDeleteBranch && !mergeFailed {
			if err := agent.RemoveWorktree(projectPath, taskNumber, merged); err != nil {
				log.Printf("[merge] Failed to remove worktree for task #%04d: %v", taskNumber, err)
			} else {
				log.Printf("[merge] Removed worktree for task #%04d", taskNumber)
			}
		}
		return !mergeFailed
	})

	// Wire watch-project callback so chained agents re-watch the project
	// (picks up directories like .watchfire/tasks/ created during earlier phases)
	agentMgr.SetWatchProjectFn(func(projectID, projectPath string) {
		_ = w.WatchProject(projectID, projectPath)
	})

	// Wire next-task callback for start-all and wildfire modes
	agentMgr.SetNextTaskFn(func(projectID, projectPath string, mode agent.Mode, phase agent.WildfirePhase, rows, cols int) (*agent.StartOptions, error) {
		proj, err := config.LoadProject(projectPath)
		if err != nil {
			return nil, err
		}

		// Start-all mode: chain through ready tasks only
		if mode == agent.ModeStartAll {
			readyStatus := string(models.TaskStatusReady)
			tasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{Status: &readyStatus})
			if err != nil {
				return nil, err
			}
			if len(tasks) == 0 {
				return nil, nil // No more ready tasks — start-all done
			}
			t := tasks[0]
			return &agent.StartOptions{
				ProjectID:        projectID,
				ProjectName:      proj.Name,
				ProjectPath:      projectPath,
				ProjectColor:     proj.Color,
				Mode:             agent.ModeStartAll,
				TaskNumber:       t.TaskNumber,
				TaskTitle:        t.Title,
				TaskPrompt:       prompts.ComposeTaskUserPrompt(t.TaskNumber, t.Title),
				TaskSystemPrompt: prompts.ComposeTaskSystemPrompt(proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria),
				Rows:             rows,
				Cols:             cols,
			}, nil
		}

		// Wildfire mode: three-phase state machine
		if mode == agent.ModeWildfire {
			// 1. Check for ready tasks → Execute phase
			readyStatus := string(models.TaskStatusReady)
			readyTasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{Status: &readyStatus})
			if err != nil {
				return nil, err
			}
			if len(readyTasks) > 0 {
				t := readyTasks[0]
				return &agent.StartOptions{
					ProjectID:        projectID,
					ProjectName:      proj.Name,
					ProjectPath:      projectPath,
					ProjectColor:     proj.Color,
					Mode:             agent.ModeWildfire,
					WildfirePhase:    agent.WildfirePhaseExecute,
					TaskNumber:       t.TaskNumber,
					TaskTitle:        t.Title,
					TaskPrompt:       prompts.ComposeTaskUserPrompt(t.TaskNumber, t.Title),
					TaskSystemPrompt: prompts.ComposeTaskSystemPrompt(proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria),
					Rows:             rows,
					Cols:             cols,
				}, nil
			}

			// 2. Check for draft tasks → Refine phase
			draftStatus := string(models.TaskStatusDraft)
			draftTasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{Status: &draftStatus})
			if err != nil {
				return nil, err
			}
			if len(draftTasks) > 0 {
				t := draftTasks[0]
				return &agent.StartOptions{
					ProjectID:        projectID,
					ProjectName:      proj.Name,
					ProjectPath:      projectPath,
					ProjectColor:     proj.Color,
					Mode:             agent.ModeWildfire,
					WildfirePhase:    agent.WildfirePhaseRefine,
					TaskNumber:       t.TaskNumber,
					TaskTitle:        t.Title,
					TaskPrompt:       prompts.ComposeWildfireRefineUserPrompt(t.TaskNumber, t.Title),
					TaskSystemPrompt: prompts.ComposeWildfireRefineSystemPrompt(proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria),
					Rows:             rows,
					Cols:             cols,
				}, nil
			}

			// 3. If previous phase was Generate → wildfire complete, transition to chat
			if phase == agent.WildfirePhaseGenerate {
				return &agent.StartOptions{
					ProjectID:    projectID,
					ProjectName:  proj.Name,
					ProjectPath:  projectPath,
					ProjectColor: proj.Color,
					Mode:         agent.ModeChat,
					Rows:         rows,
					Cols:         cols,
				}, nil
			}

			// 4. Otherwise → Generate phase
			return &agent.StartOptions{
				ProjectID:        projectID,
				ProjectName:      proj.Name,
				ProjectPath:      projectPath,
				ProjectColor:     proj.Color,
				Mode:             agent.ModeWildfire,
				WildfirePhase:    agent.WildfirePhaseGenerate,
				TaskPrompt:       prompts.ComposeWildfireGenerateUserPrompt(),
				TaskSystemPrompt: prompts.ComposeWildfireGenerateSystemPrompt(proj),
				Rows:             rows,
				Cols:             cols,
			}, nil
		}

		return nil, nil
	})

	// Wrap gRPC server with gRPC-Web for Electron GUI access
	grpcWebWrapper := grpcweb.WrapServer(grpcServer,
		grpcweb.WithOriginFunc(func(origin string) bool { return true }),
		grpcweb.WithWebsockets(true),
		grpcweb.WithWebsocketOriginFunc(func(req *http.Request) bool { return true }),
	)

	srv := &Server{
		grpcServer:     grpcServer,
		grpcWebWrapper: grpcWebWrapper,
		listener:       listener,
		port:           actualPort,
		projectManager: projectMgr,
		taskManager:    taskMgr,
		agentManager:   agentMgr,
		watcher:        w,
	}

	// Register services with generated proto descriptors
	pb.RegisterProjectServiceServer(grpcServer, &projectService{manager: projectMgr})
	pb.RegisterTaskServiceServer(grpcServer, &taskService{manager: taskMgr})
	pb.RegisterDaemonServiceServer(grpcServer, &daemonService{server: srv})
	pb.RegisterAgentServiceServer(grpcServer, &agentService{manager: agentMgr, watcher: w})
	pb.RegisterLogServiceServer(grpcServer, &logService{})
	pb.RegisterBranchServiceServer(grpcServer, &branchService{})
	pb.RegisterSettingsServiceServer(grpcServer, &settingsService{})

	// Start watcher event processing loop
	go srv.processWatcherEvents()

	// Start background update check
	srv.startUpdateCheck()

	return srv, nil
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

// AgentManager returns the agent manager.
func (s *Server) AgentManager() *agent.Manager {
	return s.agentManager
}

// Serve starts serving requests. This blocks until Stop is called.
// It serves both native gRPC (for CLI/TUI) and gRPC-Web (for Electron GUI)
// on the same port using h2c content-type negotiation.
func (s *Server) Serve() error {
	// Handler that routes gRPC-Web requests to the wrapper,
	// and native gRPC requests to the gRPC server directly.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.grpcWebWrapper.IsGrpcWebRequest(r) || s.grpcWebWrapper.IsGrpcWebSocketRequest(r) ||
			s.grpcWebWrapper.IsAcceptableGrpcCorsRequest(r) {
			s.grpcWebWrapper.ServeHTTP(w, r)
			return
		}
		// Native gRPC (content-type: application/grpc)
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			s.grpcServer.ServeHTTP(w, r)
			return
		}
		// Fallback: try gRPC-Web wrapper (handles preflight etc.)
		s.grpcWebWrapper.ServeHTTP(w, r)
	})

	// h2c allows HTTP/2 cleartext (no TLS) — required for native gRPC
	h2cHandler := h2c.NewHandler(handler, &http2.Server{})

	s.httpServer = &http.Server{Handler: h2cHandler}
	return s.httpServer.Serve(s.listener)
}

// Stop gracefully stops the server.
func (s *Server) Stop() {
	// Stop watcher before agents (prevents new task-done events during shutdown)
	if s.watcher != nil {
		s.watcher.Stop()
	}
	// Stop all running agents
	s.agentManager.StopAll()
	// Shut down HTTP server (which serves both native gRPC and gRPC-Web)
	if s.httpServer != nil {
		_ = s.httpServer.Shutdown(context.Background())
	}
	s.grpcServer.GracefulStop()
}

// processWatcherEvents listens for file system events and handles them.
func (s *Server) processWatcherEvents() {
	for event := range s.watcher.Events() {
		switch event.Type {
		case watcher.EventTaskChanged, watcher.EventTaskCreated:
			// Handle both: atomic writes (write tmp → rename) produce Create events
			// even for existing files, so we can't rely on the distinction.
			s.handleTaskChanged(event)
			// Sync next_task_number — agents create task files directly,
			// bypassing the task manager that normally increments it.
			if path := s.projectPathForID(event.ProjectID); path != "" {
				if err := config.SyncNextTaskNumber(path); err != nil {
					log.Printf("[task-watch] Failed to sync next_task_number: %v", err)
				}
			}
		case watcher.EventRefinePhaseEnded:
			s.handlePhaseEnded(event, agent.WildfirePhaseRefine)
		case watcher.EventGeneratePhaseEnded:
			s.handlePhaseEnded(event, agent.WildfirePhaseGenerate)
		case watcher.EventDefinitionDone:
			s.handleGenerateModeEnded(event, agent.ModeGenerateDefinition)
		case watcher.EventTasksDone:
			s.handleGenerateModeEnded(event, agent.ModeGenerateTasks)
		}
	}
}

// handleTaskChanged reacts to a task YAML file changing.
// If the task status is "done" and the agent is working on that task, stop the agent.
func (s *Server) handleTaskChanged(event watcher.Event) {
	projectPath := s.projectPathForID(event.ProjectID)
	log.Printf("[task-watch] Event for task #%04d in project %s (path: %s)", event.TaskNumber, event.ProjectID, projectPath)

	t, err := config.LoadTask(projectPath, event.TaskNumber)
	if err != nil {
		log.Printf("[task-watch] Failed to load task #%04d: %v", event.TaskNumber, err)
		return
	}
	if t == nil {
		log.Printf("[task-watch] Task #%04d not found (nil)", event.TaskNumber)
		return
	}

	log.Printf("[task-watch] Task #%04d status: %s", event.TaskNumber, t.Status)
	if t.Status != models.TaskStatusDone {
		return
	}

	// Use StopAgentForTask to atomically verify the agent is still working on
	// this specific task before stopping. Run in a goroutine to avoid blocking
	// the event processing loop — StopAgent can take up to 5+ seconds.
	log.Printf("Task #%04d marked done — stopping agent for project %s", event.TaskNumber, event.ProjectID)
	go func() {
		if err := s.agentManager.StopAgentForTask(event.ProjectID, event.TaskNumber); err != nil {
			log.Printf("[task-watch] Stop skipped for project %s task #%04d: %v", event.ProjectID, event.TaskNumber, err)
		}
	}()
}

// handlePhaseEnded reacts to a wildfire phase signal file being created.
// Deletes the signal file and stops the agent to trigger the next phase.
func (s *Server) handlePhaseEnded(event watcher.Event, expectedPhase agent.WildfirePhase) {
	projectPath := s.projectPathForID(event.ProjectID)
	log.Printf("[phase-watch] %s signal detected for project %s", expectedPhase, event.ProjectID)

	running, ok := s.agentManager.GetAgent(event.ProjectID)
	if !ok {
		log.Printf("[phase-watch] No agent running for project %s", event.ProjectID)
		// Still delete the signal file
		_ = os.Remove(event.Path)
		return
	}

	if running.Mode != agent.ModeWildfire {
		log.Printf("[phase-watch] Agent not in wildfire mode (mode: %s)", running.Mode)
		_ = os.Remove(event.Path)
		return
	}

	if running.WildfirePhase != expectedPhase {
		log.Printf("[phase-watch] Agent in phase %s, not %s", running.WildfirePhase, expectedPhase)
		_ = os.Remove(event.Path)
		return
	}

	// Delete the signal file before stopping
	if err := os.Remove(event.Path); err != nil {
		log.Printf("[phase-watch] Failed to delete signal file %s: %v", event.Path, err)
	} else {
		log.Printf("[phase-watch] Deleted signal file: %s", event.Path)
	}

	// Run StopAgent in a goroutine to avoid blocking the event processing loop.
	log.Printf("Wildfire %s phase ended — stopping agent for project %s", expectedPhase, event.ProjectID)
	go func() {
		if err := s.agentManager.StopAgent(event.ProjectID); err != nil {
			log.Printf("Failed to stop agent after phase end: %v", err)
		}
	}()
	_ = projectPath // used for logging context
}

// handleGenerateModeEnded reacts to a generate mode signal file being created.
// Deletes the signal file and stops the agent (no chaining - single-shot command).
func (s *Server) handleGenerateModeEnded(event watcher.Event, expectedMode agent.Mode) {
	log.Printf("[generate-watch] %s signal detected for project %s", expectedMode, event.ProjectID)

	running, ok := s.agentManager.GetAgent(event.ProjectID)
	if !ok {
		log.Printf("[generate-watch] No agent running for project %s", event.ProjectID)
		_ = os.Remove(event.Path)
		return
	}

	if running.Mode != expectedMode {
		log.Printf("[generate-watch] Agent not in %s mode (mode: %s)", expectedMode, running.Mode)
		_ = os.Remove(event.Path)
		return
	}

	// Delete the signal file before stopping
	if err := os.Remove(event.Path); err != nil {
		log.Printf("[generate-watch] Failed to delete signal file %s: %v", event.Path, err)
	} else {
		log.Printf("[generate-watch] Deleted signal file: %s", event.Path)
	}

	// Run StopAgent in a goroutine to avoid blocking the event processing loop.
	log.Printf("%s complete — stopping agent for project %s", expectedMode, event.ProjectID)
	go func() {
		if err := s.agentManager.StopAgent(event.ProjectID); err != nil {
			log.Printf("Failed to stop agent after %s: %v", expectedMode, err)
		}
	}()
}

// projectPathForID resolves a project ID to its filesystem path.
func (s *Server) projectPathForID(projectID string) string {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return ""
	}
	entry := index.FindProject(projectID)
	if entry == nil {
		return ""
	}
	return entry.Path
}

// TrayState adapts a Server to the tray.DaemonState interface.
type TrayState struct {
	srv *Server
}

// NewTrayState creates a TrayState for the given server.
func NewTrayState(srv *Server) *TrayState {
	return &TrayState{srv: srv}
}

// Port returns the port the server is listening on.
func (t *TrayState) Port() int {
	return t.srv.Port()
}

// ProjectCount returns the number of registered projects.
func (t *TrayState) ProjectCount() int {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return 0
	}
	return len(index.Projects)
}

// ActiveAgents returns information about all currently running agents.
func (t *TrayState) ActiveAgents() []tray.AgentInfo {
	running := t.srv.agentManager.ListAgents()
	agents := make([]tray.AgentInfo, 0, len(running))
	for _, a := range running {
		agents = append(agents, tray.AgentInfo{
			ProjectID:    a.ProjectID,
			ProjectName:  a.ProjectName,
			ProjectColor: a.ProjectColor,
			Mode:         string(a.Mode),
			TaskNumber:   a.TaskNumber,
			TaskTitle:    a.TaskTitle,
		})
	}
	return agents
}

// StopAgent stops the agent for the given project.
func (t *TrayState) StopAgent(projectID string) {
	if err := t.srv.agentManager.StopAgentByUser(projectID); err != nil {
		log.Printf("Failed to stop agent from tray: %v", err)
	}
}

// RequestShutdown sends SIGINT to the current process to trigger a graceful shutdown.
func (t *TrayState) RequestShutdown() {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGINT)
}
