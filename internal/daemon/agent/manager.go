// Package agent handles agent lifecycle management for the daemon.
package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/prompts"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

// Manager-level constants.
const (
	pollTaskStatusInterval = 5 * time.Second
	pollSignalFileInterval = 3 * time.Second
	maxTaskRestarts        = 3 // Stop chaining after this many consecutive restarts of the same task
)

// Mode defines the mode an agent runs in.
type Mode string

// Agent modes.
const (
	ModeChat               Mode = "chat"
	ModeTask               Mode = "task"
	ModeStartAll           Mode = "start-all"
	ModeWildfire           Mode = "wildfire"
	ModeGenerateDefinition Mode = "generate-definition"
	ModeGenerateTasks      Mode = "generate-tasks"
)

// WildfirePhase identifies the current phase within wildfire mode.
type WildfirePhase string

// WildfirePhase values.
const (
	WildfirePhaseNone     WildfirePhase = ""
	WildfirePhaseExecute  WildfirePhase = "execute"
	WildfirePhaseRefine   WildfirePhase = "refine"
	WildfirePhaseGenerate WildfirePhase = "generate"
)

// RunningAgent tracks a currently running agent session.
type RunningAgent struct {
	ProjectID     string
	ProjectName   string
	ProjectPath   string
	ProjectColor  string
	Mode          Mode
	WildfirePhase WildfirePhase
	TaskNumber    int
	TaskTitle     string
	SessionName   string // --name value passed to claude (used for transcript lookup)
	WorktreePath  string
	BackendName   string // resolved agent backend name (e.g. "claude-code", "codex")
	Process       *Process
	userStopped   bool // set by StopAgentByUser to prevent chaining in wildfire/start-all
}

// StartOptions contains options for starting an agent.
type StartOptions struct {
	ProjectID        string
	ProjectName      string
	ProjectPath      string
	ProjectColor     string
	Mode             Mode
	WildfirePhase    WildfirePhase
	TaskNumber       int
	TaskTitle        string
	TaskPrompt       string // Simple positional argument: "Implement Task #0001: ..."
	TaskSystemPrompt string // Full system prompt with task details
	Rows             int
	Cols             int
	Sandbox          string // "auto" | "seatbelt" | "landlock" | "bwrap" | "none"
}

// Manager handles agent lifecycle operations.
type Manager struct {
	mu             sync.RWMutex
	agents         map[string]*RunningAgent // keyed by ProjectID
	taskRestarts   map[string]int           // keyed by ProjectID — consecutive restarts of the same task
	onChangeFn     func()                   // called when agent state changes (for tray updates)
	nextTaskFn     func(projectID, projectPath string, mode Mode, phase WildfirePhase, rows, cols int) (*StartOptions, error)
	onTaskDoneFn   func(projectPath string, taskNumber int, worktreePath string) bool // called after agent exits for a task; returns true to continue chaining
	watchProjectFn func(projectID, projectPath string)                                // called to ensure project watcher is active
}

// NewManager creates a new agent manager.
func NewManager() *Manager {
	return &Manager{
		agents:       make(map[string]*RunningAgent),
		taskRestarts: make(map[string]int),
	}
}

// SetOnChange sets a callback that is invoked whenever the agent state changes
// (agent started, stopped, or process exited). Used to notify the system tray.
func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChangeFn = fn
}

// SetNextTaskFn sets a callback used by start-all and wildfire modes to resolve the next task.
func (m *Manager) SetNextTaskFn(fn func(projectID, projectPath string, mode Mode, phase WildfirePhase, rows, cols int) (*StartOptions, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextTaskFn = fn
}

// SetOnTaskDoneFn sets a callback invoked after an agent exits for a task.
// Used for git merge + worktree cleanup. Returns true if chaining should continue.
func (m *Manager) SetOnTaskDoneFn(fn func(projectPath string, taskNumber int, worktreePath string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onTaskDoneFn = fn
}

// SetWatchProjectFn sets a callback used to ensure the project watcher is active.
// Called from StartAgent so that chained agents (wildfire/start-all) re-watch the project,
// picking up directories (like .watchfire/tasks/) that may not have existed at initial watch time.
func (m *Manager) SetWatchProjectFn(fn func(projectID, projectPath string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watchProjectFn = fn
}

// StartAgent starts an agent for the given project.
// If an agent is already running for this project, it is stopped first.
func (m *Manager) StartAgent(opts StartOptions) (*RunningAgent, error) {
	m.mu.Lock()

	// If an agent is already running, stop it before starting a new one.
	if existing, ok := m.agents[opts.ProjectID]; ok {
		existing.userStopped = true // prevent wildfire/start-all chaining
		proc := existing.Process
		m.mu.Unlock() // release lock — monitorProcess needs it for cleanup

		proc.Stop() // blocking: sends SIGTERM, waits for exit

		// Poll until monitorProcess finishes cleanup (removes from map)
		for i := 0; i < 100; i++ { // 10s max (100 × 100ms)
			time.Sleep(100 * time.Millisecond)
			m.mu.RLock()
			_, stillRunning := m.agents[opts.ProjectID]
			m.mu.RUnlock()
			if !stillRunning {
				break
			}
		}

		m.mu.Lock() // re-acquire for rest of StartAgent
		// If still present after timeout, bail
		if _, ok := m.agents[opts.ProjectID]; ok {
			m.mu.Unlock()
			return nil, fmt.Errorf("timed out waiting for previous agent to stop")
		}
	}

	// Ensure the project watcher is active (re-watches directories like .watchfire/tasks/
	// that may have been created since the initial watch).
	if m.watchProjectFn != nil {
		m.watchProjectFn(opts.ProjectID, opts.ProjectPath)
	}

	// Load project config for definition and agent selection
	project, err := config.LoadProject(opts.ProjectPath)
	if err != nil {
		log.Printf("Warning: could not load project config: %v", err)
	}

	// Resolve which agent backend to use for this project. Falls back to
	// Claude only when neither the project nor global settings specify one.
	settings, _ := config.LoadSettings()
	be, err := resolveBackend(project, settings)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	// Resolve agent binary path via the backend.
	agentPath, err := be.ResolveExecutable(settings)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to find %s binary: %w", be.Name(), err)
	}

	// Determine working directory, system prompt, and positional args
	workDir := opts.ProjectPath
	composedPrompt := prompts.ComposePrompt(project) // default: chat mode
	var taskArgs []string
	var worktreePath string

	if (opts.Mode == ModeTask || opts.Mode == ModeStartAll ||
		(opts.Mode == ModeWildfire && opts.WildfirePhase == WildfirePhaseExecute)) && opts.TaskNumber > 0 {
		// 1. Create git worktree
		wt, wtErr := EnsureWorktree(opts.ProjectPath, opts.TaskNumber)
		if wtErr != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("failed to create worktree: %w", wtErr)
		}
		workDir = wt
		worktreePath = wt

		// 2. Mark task as started
		taskMgr := task.NewManager()
		t, taskErr := taskMgr.GetTask(opts.ProjectPath, opts.TaskNumber)
		if taskErr == nil && t != nil {
			t.Start() // increments AgentSessions, sets StartedAt
			if t.Status == models.TaskStatusDraft {
				t.Status = models.TaskStatusReady
			}
			_ = config.SaveTask(opts.ProjectPath, t)
		}

		// 3. Use task-specific system prompt (includes task details)
		if opts.TaskSystemPrompt != "" {
			composedPrompt = opts.TaskSystemPrompt
		}

		// 4. Simple positional argument: "Implement Task #0001: Title"
		if opts.TaskPrompt != "" {
			taskArgs = append(taskArgs, opts.TaskPrompt)
		}
	}

	// Wildfire refine/generate phases: run at project root (no worktree)
	if opts.Mode == ModeWildfire &&
		(opts.WildfirePhase == WildfirePhaseRefine || opts.WildfirePhase == WildfirePhaseGenerate) {
		if opts.TaskSystemPrompt != "" {
			composedPrompt = opts.TaskSystemPrompt
		}
		if opts.TaskPrompt != "" {
			taskArgs = append(taskArgs, opts.TaskPrompt)
		}
	}

	// Generate definition/tasks modes: run at project root (no worktree)
	if opts.Mode == ModeGenerateDefinition || opts.Mode == ModeGenerateTasks {
		if opts.TaskSystemPrompt != "" {
			composedPrompt = opts.TaskSystemPrompt
		}
		if opts.TaskPrompt != "" {
			taskArgs = append(taskArgs, opts.TaskPrompt)
		}
	}

	// Build command via the resolved backend.
	sessionName := buildSessionName(opts.ProjectName, opts.Mode, opts.WildfirePhase, opts.TaskNumber, opts.TaskTitle)

	// Deliver the composed system prompt via the backend. For Claude this
	// is a no-op (the prompt rides the CLI flag); for Codex this materialises
	// a per-session CODEX_HOME with AGENTS.md plus auth symlinks.
	if err := installSystemPrompt(be, workDir, sessionName, composedPrompt); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to install system prompt: %w", err)
	}

	var initialPrompt string
	if len(taskArgs) > 0 {
		initialPrompt = taskArgs[0]
	}
	built, err := be.BuildCommand(backend.CommandOpts{
		WorkDir:       workDir,
		SessionName:   sessionName,
		SystemPrompt:  composedPrompt,
		InitialPrompt: initialPrompt,
	})
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to build agent command: %w", err)
	}
	args := built.Args

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Spawn sandboxed command — sandbox scoped to project root (covers worktrees + task files)
	sandbox := opts.Sandbox
	if sandbox == "" {
		sandbox = SandboxAuto
	}
	cmd, sandboxTmp, err := SpawnSandboxedWith(sandbox, homeDir, opts.ProjectPath, be.SandboxExtras(), agentPath, args...)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to create sandboxed command: %w", err)
	}
	// Override working directory to worktree for task mode
	cmd.Dir = workDir
	// Merge backend-contributed env vars (e.g. CODEX_HOME) into the sandbox
	// env without losing sandbox-driven changes (StripEnv, TERM, COLORTERM).
	cmd.Env = mergeBackendEnv(cmd.Env, built.Env)

	// Start in PTY
	proc, err := NewProcess(ProcessOptions{
		ProjectID:  opts.ProjectID,
		Cmd:        cmd,
		Rows:       opts.Rows,
		Cols:       opts.Cols,
		SandboxTmp: sandboxTmp,
	})
	if err != nil {
		_ = os.Remove(sandboxTmp)
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to start agent process: %w", err)
	}

	ra := &RunningAgent{
		ProjectID:     opts.ProjectID,
		ProjectName:   opts.ProjectName,
		ProjectPath:   opts.ProjectPath,
		ProjectColor:  opts.ProjectColor,
		Mode:          opts.Mode,
		WildfirePhase: opts.WildfirePhase,
		TaskNumber:    opts.TaskNumber,
		TaskTitle:     opts.TaskTitle,
		SessionName:   sessionName,
		WorktreePath:  worktreePath,
		BackendName:   be.Name(),
		Process:       proc,
	}

	m.agents[opts.ProjectID] = ra
	m.persistStateLocked()

	log.Printf("Agent started for project %s (%s mode)", opts.ProjectName, opts.Mode)

	// Monitor process in background
	go m.monitorProcess(opts.ProjectID, proc)

	// Poll task status as a safety net for missed watcher events
	if opts.TaskNumber > 0 {
		go m.pollTaskStatus(opts.ProjectID, opts.ProjectPath, opts.TaskNumber, proc)
	}

	// Poll signal file as a safety net for wildfire generate/refine phases
	if opts.Mode == ModeWildfire &&
		(opts.WildfirePhase == WildfirePhaseGenerate || opts.WildfirePhase == WildfirePhaseRefine) {
		go m.pollSignalFile(opts.ProjectID, opts.ProjectPath, opts.WildfirePhase, proc)
	}

	m.mu.Unlock()
	return ra, nil
}

// monitorProcess waits for an agent process to exit and cleans up.
// In start-all and wildfire modes, it chains to the next task instead of just cleaning up.
func (m *Manager) monitorProcess(projectID string, proc *Process) {
	<-proc.Done()

	m.mu.Lock()
	ag, ok := m.agents[projectID]
	if !ok || ag.Process != proc {
		m.mu.Unlock()
		return
	}

	log.Printf("Agent for project %s exited (mode: %s)", ag.ProjectName, ag.Mode)

	// Persist scrollback to log file
	m.writeSessionLog(ag, proc)

	// Run post-task cleanup (merge + worktree removal) for any mode with a task
	taskDoneOK := true
	if ag.TaskNumber > 0 && m.onTaskDoneFn != nil {
		log.Printf("[chain] Running onTaskDoneFn for task #%04d (mode=%s)", ag.TaskNumber, ag.Mode)
		taskDoneFn := m.onTaskDoneFn
		taskNum := ag.TaskNumber
		projPath := ag.ProjectPath
		wtPath := ag.WorktreePath
		m.mu.Unlock()
		taskDoneOK = taskDoneFn(projPath, taskNum, wtPath)
		log.Printf("[chain] onTaskDoneFn returned taskDoneOK=%v for task #%04d", taskDoneOK, taskNum)
		m.mu.Lock()
		// Re-check agent is still ours after releasing lock
		if curr, ok := m.agents[projectID]; !ok || curr.Process != proc {
			log.Printf("[chain] Agent replaced or removed during onTaskDoneFn — aborting chain")
			m.mu.Unlock()
			return
		}
	}

	// Before chaining, check if there's an active issue (auth error, rate limit)
	hasIssue := proc.GetIssue() != nil

	log.Printf("[chain] Decision: taskDoneOK=%v userStopped=%v hasIssue=%v mode=%s nextTaskFn=%v",
		taskDoneOK, ag.userStopped, hasIssue, ag.Mode, m.nextTaskFn != nil)

	if hasIssue {
		log.Printf("[chain] Chaining blocked: active issue detected (type=%s) — stopping automatic mode", proc.GetIssue().Type)
	}

	if taskDoneOK && !ag.userStopped && !hasIssue && (ag.Mode == ModeStartAll || ag.Mode == ModeWildfire) && m.nextTaskFn != nil {
		agentMode := ag.Mode
		agentPhase := ag.WildfirePhase
		projectPath := ag.ProjectPath
		projectName := ag.ProjectName
		projectColor := ag.ProjectColor
		prevTaskNumber := ag.TaskNumber
		rows, cols := proc.TerminalSize()

		// Clean up old process resources before chaining
		proc.Cleanup()

		// Remove current agent before starting next (avoids "already running" guard)
		delete(m.agents, projectID)
		m.persistStateLocked()
		m.mu.Unlock()

		nextOpts, err := m.nextTaskFn(projectID, projectPath, agentMode, agentPhase, rows, cols)
		if err != nil {
			log.Printf("%s: error finding next task: %v", agentMode, err)
			return
		}
		if nextOpts != nil {
			// Restart protection: if the same task is returned again, track consecutive restarts.
			// After maxTaskRestarts, stop chaining and transition to chat mode.
			if nextOpts.TaskNumber == prevTaskNumber && prevTaskNumber > 0 {
				m.mu.Lock()
				m.taskRestarts[projectID]++
				count := m.taskRestarts[projectID]
				m.mu.Unlock()

				if count >= maxTaskRestarts {
					log.Printf("[chain] Restart limit reached: task #%04d restarted %d times without completing — switching to chat mode", prevTaskNumber, count)
					m.mu.Lock()
					delete(m.taskRestarts, projectID)
					m.mu.Unlock()

					chatOpts := StartOptions{
						ProjectID:    projectID,
						ProjectName:  projectName,
						ProjectPath:  projectPath,
						ProjectColor: projectColor,
						Mode:         ModeChat,
						Rows:         rows,
						Cols:         cols,
					}
					if _, err := m.StartAgent(chatOpts); err != nil {
						log.Printf("[chain] Failed to start chat mode after restart limit: %v", err)
					}
					return
				}
				log.Printf("[chain] Task #%04d restart %d/%d", prevTaskNumber, count, maxTaskRestarts)
			} else {
				// Different task — reset counter (successful progression)
				m.mu.Lock()
				m.taskRestarts[projectID] = 0
				m.mu.Unlock()
			}

			log.Printf("%s: starting next — mode=%s phase=%s task=#%04d", agentMode, nextOpts.Mode, nextOpts.WildfirePhase, nextOpts.TaskNumber)
			if _, err := m.StartAgent(*nextOpts); err != nil {
				log.Printf("%s: failed to start next: %v", agentMode, err)
			}
			return
		}

		log.Printf("%s: no more tasks for project %s", agentMode, projectID)
		return
	}

	// Non-chaining mode: just clean up
	proc.Cleanup()
	delete(m.agents, projectID)
	delete(m.taskRestarts, projectID)
	m.persistStateLocked()
	m.mu.Unlock()
}

// pollTaskStatus periodically checks whether a task has been marked done.
// This is a safety net for cases where the file watcher misses an event
// (e.g., tasks directory not watched yet, kqueue buffer overflow).
func (m *Manager) pollTaskStatus(projectID, projectPath string, taskNumber int, proc *Process) {
	ticker := time.NewTicker(pollTaskStatusInterval)
	defer ticker.Stop()
	for {
		select {
		case <-proc.Done():
			return
		case <-ticker.C:
			t, err := config.LoadTask(projectPath, taskNumber)
			if err != nil || t == nil {
				continue
			}
			if t.Status == models.TaskStatusDone {
				log.Printf("[poll] Task #%04d done — stopping agent for project %s", taskNumber, projectID)
				_ = m.StopAgentForTask(projectID, taskNumber)
				return
			}
		}
	}
}

// pollSignalFile periodically checks for a wildfire phase signal file.
// This is a safety net for cases where the file watcher misses the event.
func (m *Manager) pollSignalFile(projectID, projectPath string, phase WildfirePhase, proc *Process) {
	var signalFile string
	switch phase {
	case WildfirePhaseGenerate:
		signalFile = "generate_done.yaml"
	case WildfirePhaseRefine:
		signalFile = "refine_done.yaml"
	default:
		return
	}

	signalPath := filepath.Join(config.ProjectDir(projectPath), signalFile)
	ticker := time.NewTicker(pollSignalFileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-proc.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(signalPath); err == nil {
				log.Printf("[poll] Signal file %s found — stopping agent for project %s", signalFile, projectID)
				// Remove signal file before stopping
				_ = os.Remove(signalPath)
				_ = m.StopAgent(projectID)
				return
			}
		}
	}
}

// StopAgent stops a running agent for the given project.
func (m *Manager) StopAgent(projectID string) error {
	m.mu.Lock()
	agent, ok := m.agents[projectID]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("no agent running for project: %s", projectID)
	}

	// Stop is blocking — do it outside lock. monitorProcess will clean up the map.
	agent.Process.Stop()
	return nil
}

// StopAgentByUser stops a running agent and marks it as user-stopped so that
// wildfire/start-all mode will NOT chain to the next task.
func (m *Manager) StopAgentByUser(projectID string) error {
	m.mu.Lock()
	ag, ok := m.agents[projectID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no agent running for project: %s", projectID)
	}
	ag.userStopped = true
	m.mu.Unlock()

	ag.Process.Stop()
	return nil
}

// StopAgentForTask atomically checks that the agent for projectID is working on
// the given taskNumber before stopping it. This prevents a race where the agent
// has already chained to a different task between the caller's check and the stop.
func (m *Manager) StopAgentForTask(projectID string, taskNumber int) error {
	m.mu.Lock()
	ag, ok := m.agents[projectID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no agent running for project: %s", projectID)
	}
	if ag.TaskNumber != taskNumber {
		m.mu.Unlock()
		return fmt.Errorf("agent working on task #%04d, not #%04d", ag.TaskNumber, taskNumber)
	}
	proc := ag.Process
	m.mu.Unlock()

	proc.Stop()
	return nil
}

// StopAll stops all running agents. Used during daemon shutdown.
func (m *Manager) StopAll() {
	m.mu.Lock()
	agents := make([]*RunningAgent, 0, len(m.agents))
	for _, a := range m.agents {
		a.userStopped = true
		agents = append(agents, a)
	}
	m.mu.Unlock()

	for _, a := range agents {
		log.Printf("Stopping agent for project %s", a.ProjectName)
		a.Process.Stop()
	}
}

// GetAgent returns a running agent by project ID.
func (m *Manager) GetAgent(projectID string) (*RunningAgent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[projectID]
	return agent, ok
}

// ListAgents returns all running agents.
func (m *Manager) ListAgents() []*RunningAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agents := make([]*RunningAgent, 0, len(m.agents))
	for _, a := range m.agents {
		agents = append(agents, a)
	}
	return agents
}

// ActiveCount returns the number of running agents.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.agents)
}

// writeSessionLog persists the agent's scrollback buffer to a log file.
// Called from monitorProcess while holding m.mu.
func (m *Manager) writeSessionLog(ag *RunningAgent, proc *Process) {
	scrollback := proc.GetFullScrollback()
	if len(scrollback) == 0 {
		return
	}

	// Strip ANSI escape sequences for clean text logs
	cleaned := make([]string, 0, len(scrollback))
	for _, line := range scrollback {
		stripped := stripAnsi(line)
		if stripped != "" {
			cleaned = append(cleaned, stripped)
		}
	}
	if len(cleaned) == 0 {
		return
	}
	scrollback = cleaned

	status := "completed"
	if proc.ExitErr() != nil {
		status = "interrupted"
	}

	backendName := ag.BackendName
	if backendName == "" {
		backendName = backend.ClaudeBackendName
	}
	entry, err := config.WriteLog(
		ag.ProjectID,
		ag.TaskNumber,
		0, // session number — could track this per-task but 0 is fine for now
		backendName,
		string(ag.Mode),
		status,
		proc.StartedAt(),
		scrollback,
	)
	if err != nil {
		log.Printf("Failed to write session log for project %s: %v", ag.ProjectName, err)
		return
	}
	log.Printf("Session log written: %s", entry.LogID)

	// Try to find and copy the Claude Code JSONL transcript
	workDir := ag.WorktreePath
	if workDir == "" {
		workDir = ag.ProjectPath
	}
	if ag.SessionName != "" && workDir != "" {
		be, ok := backend.Get(backendName)
		if !ok {
			log.Printf("backend %q not registered — skipping transcript lookup", backendName)
			return
		}
		transcriptPath, findErr := be.LocateTranscript(workDir, proc.StartedAt(), ag.SessionName)
		if findErr != nil {
			log.Printf("No transcript found for session %q: %v", ag.SessionName, findErr)
		} else {
			if copyErr := config.CopyTranscript(ag.ProjectID, entry.LogID, transcriptPath); copyErr != nil {
				log.Printf("Failed to copy transcript for session %q: %v", ag.SessionName, copyErr)
			} else {
				log.Printf("Transcript copied: %s.jsonl", entry.LogID)
			}
		}
	}
}

// persistStateLocked writes the current agent state to ~/.watchfire/agents.yaml.
// Must be called while holding m.mu.
func (m *Manager) persistStateLocked() {
	state := models.NewAgentState()
	for _, a := range m.agents {
		state.Agents = append(state.Agents, models.RunningAgentInfo{
			ProjectID:   a.ProjectID,
			ProjectName: a.ProjectName,
			ProjectPath: a.ProjectPath,
			Mode:        string(a.Mode),
			TaskNumber:  a.TaskNumber,
			TaskTitle:   a.TaskTitle,
		})
	}
	if err := config.SaveAgentState(state); err != nil {
		log.Printf("Failed to persist agent state: %v", err)
	}

	// Notify tray/listeners of state change in a goroutine.
	// Must use goroutine because onChangeFn calls ListAgents() which needs m.mu.RLock(),
	// and persistStateLocked is called while m.mu is write-locked.
	if m.onChangeFn != nil {
		go m.onChangeFn()
	}
}

// resolveBackend picks the agent backend for a project using the chain:
//  1. project.DefaultAgent (from .watchfire/project.yaml) if non-empty.
//  2. Global settings.Defaults.DefaultAgent (from ~/.watchfire/settings.yaml)
//     if non-empty and not the "ask per project" sentinel (empty string).
//  3. Fallback to the Claude backend for backwards compatibility.
//
// An unknown agent name returns ErrUnknownBackend rather than silently
// falling back, so misconfiguration surfaces to the caller instead of
// spawning a different agent than the user intended.
func resolveBackend(project *models.Project, settings *models.Settings) (backend.Backend, error) {
	name := ""
	if project != nil {
		name = strings.TrimSpace(project.DefaultAgent)
	}
	if name == "" && settings != nil {
		name = strings.TrimSpace(settings.Defaults.DefaultAgent)
	}
	if name == "" {
		name = backend.ClaudeBackendName
	}

	be, ok := backend.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", backend.ErrUnknownBackend, name)
	}
	return be, nil
}

// installSystemPrompt delivers the composed system prompt to the backend.
// Some backends (Codex) need the session name to key the per-session config
// directory; if the backend exposes InstallSystemPromptForSession we prefer
// that path over the workDir-derived fallback.
func installSystemPrompt(be backend.Backend, workDir, sessionName, composedPrompt string) error {
	if ext, ok := be.(interface {
		InstallSystemPromptForSession(sessionName, composedPrompt string) error
	}); ok {
		return ext.InstallSystemPromptForSession(sessionName, composedPrompt)
	}
	return be.InstallSystemPrompt(workDir, composedPrompt)
}

// mergeBackendEnv folds backend-contributed env vars into the sandbox-computed
// env. A backend's BuildCommand typically returns os.Environ() plus a few
// additions (e.g. CODEX_HOME); we only adopt the additions so sandbox changes
// to TERM/COLORTERM and StripEnv deletions are preserved.
func mergeBackendEnv(sandboxEnv, backendEnv []string) []string {
	if len(backendEnv) == 0 {
		return sandboxEnv
	}
	parent := os.Environ()
	parentSet := make(map[string]string, len(parent))
	for _, e := range parent {
		if i := strings.IndexByte(e, '='); i >= 0 {
			parentSet[e[:i]] = e[i+1:]
		}
	}
	sandboxKeys := make(map[string]int, len(sandboxEnv))
	for i, e := range sandboxEnv {
		if j := strings.IndexByte(e, '='); j >= 0 {
			sandboxKeys[e[:j]] = i
		}
	}
	out := sandboxEnv
	for _, e := range backendEnv {
		j := strings.IndexByte(e, '=')
		if j < 0 {
			continue
		}
		key, val := e[:j], e[j+1:]
		if pv, inParent := parentSet[key]; inParent && pv == val {
			// Unchanged from parent env — sandbox env already has it (or
			// deliberately stripped it); don't re-add.
			continue
		}
		if idx, exists := sandboxKeys[key]; exists {
			out[idx] = e
		} else {
			out = append(out, e)
		}
	}
	return out
}

// buildSessionName constructs a structured --name value for Claude Code sessions.
// Format: {project}:{mode}[:{task}]
func buildSessionName(projectName string, mode Mode, phase WildfirePhase, taskNum int, taskTitle string) string {
	slug := slugify(projectName, 30)

	var modePart string
	switch mode {
	case ModeChat:
		modePart = "chat"
	case ModeTask:
		modePart = "task"
	case ModeStartAll:
		modePart = "start-all"
	case ModeWildfire:
		modePart = "wildfire-" + strings.ToLower(string(phase))
	case ModeGenerateDefinition:
		modePart = "gen-definition"
	case ModeGenerateTasks:
		modePart = "gen-tasks"
	default:
		modePart = "agent"
	}

	name := slug + ":" + modePart

	if taskNum > 0 && taskTitle != "" {
		taskSlug := fmt.Sprintf("#%04d-%s", taskNum, slugify(taskTitle, 30))
		name += ":" + taskSlug
	}

	if len(name) > 80 {
		name = name[:80]
		name = strings.TrimRight(name, "-:")
	}

	return name
}

// slugify converts a string to a URL-friendly slug.
func slugify(s string, maxLen int) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return strings.TrimRight(s, "-")
}
