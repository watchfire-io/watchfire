// Package agent handles agent lifecycle management for the daemon.
package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/prompts"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
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
	WorktreePath  string
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
}

// Manager handles agent lifecycle operations.
type Manager struct {
	mu             sync.RWMutex
	agents         map[string]*RunningAgent // keyed by ProjectID
	onChangeFn     func()                   // called when agent state changes (for tray updates)
	nextTaskFn     func(projectID, projectPath string, mode Mode, phase WildfirePhase, rows, cols int) (*StartOptions, error)
	onTaskDoneFn   func(projectPath string, taskNumber int, worktreePath string) bool // called after agent exits for a task; returns true to continue chaining
	watchProjectFn func(projectID, projectPath string)                           // called to ensure project watcher is active
}

// NewManager creates a new agent manager.
func NewManager() *Manager {
	return &Manager{
		agents: make(map[string]*RunningAgent),
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

	// Resolve agent binary path
	agentPath, err := resolveAgentPath()
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to find claude: %w", err)
	}

	// Load project config for definition
	project, err := config.LoadProject(opts.ProjectPath)
	if err != nil {
		log.Printf("Warning: could not load project config: %v", err)
	}

	// Determine working directory, system prompt, and positional args
	workDir := opts.ProjectPath
	composedPrompt := prompts.ComposePrompt(project) // default: chat mode
	var taskArgs []string
	var worktreePath string

	if (opts.Mode == ModeTask || opts.Mode == ModeStartAll ||
		(opts.Mode == ModeWildfire && opts.WildfirePhase == WildfirePhaseExecute)) && opts.TaskNumber > 0 {
		// 1. Create git worktree
		wt, err := EnsureWorktree(opts.ProjectPath, opts.TaskNumber)
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("failed to create worktree: %w", err)
		}
		workDir = wt
		worktreePath = wt

		// 2. Mark task as started
		taskMgr := task.NewManager()
		t, err := taskMgr.GetTask(opts.ProjectPath, opts.TaskNumber)
		if err == nil && t != nil {
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

	// Build args
	args := []string{
		"--append-system-prompt", composedPrompt,
		"--dangerously-skip-permissions",
	}
	args = append(args, taskArgs...)

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Spawn sandboxed command — sandbox scoped to project root (covers worktrees + task files)
	cmd, sandboxTmp, err := SpawnSandboxed(homeDir, opts.ProjectPath, agentPath, args...)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to create sandboxed command: %w", err)
	}
	// Override working directory to worktree for task mode
	cmd.Dir = workDir

	// Start in PTY
	proc, err := NewProcess(ProcessOptions{
		ProjectID:  opts.ProjectID,
		Cmd:        cmd,
		Rows:       opts.Rows,
		Cols:       opts.Cols,
		SandboxTmp: sandboxTmp,
	})
	if err != nil {
		os.Remove(sandboxTmp)
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
		WorktreePath:  worktreePath,
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
		taskDoneFn := m.onTaskDoneFn
		taskNum := ag.TaskNumber
		projPath := ag.ProjectPath
		wtPath := ag.WorktreePath
		m.mu.Unlock()
		taskDoneOK = taskDoneFn(projPath, taskNum, wtPath)
		m.mu.Lock()
		// Re-check agent is still ours after releasing lock
		if curr, ok := m.agents[projectID]; !ok || curr.Process != proc {
			m.mu.Unlock()
			return
		}
	}

	if taskDoneOK && !ag.userStopped && (ag.Mode == ModeStartAll || ag.Mode == ModeWildfire) && m.nextTaskFn != nil {
		agentMode := ag.Mode
		agentPhase := ag.WildfirePhase
		projectPath := ag.ProjectPath
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
	m.persistStateLocked()
	m.mu.Unlock()
}

// pollTaskStatus periodically checks whether a task has been marked done.
// This is a safety net for cases where the file watcher misses an event
// (e.g., tasks directory not watched yet, kqueue buffer overflow).
func (m *Manager) pollTaskStatus(projectID, projectPath string, taskNumber int, proc *Process) {
	ticker := time.NewTicker(5 * time.Second)
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
	ticker := time.NewTicker(3 * time.Second)
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

	status := "completed"
	if proc.ExitErr() != nil {
		status = "interrupted"
	}

	entry, err := config.WriteLog(
		ag.ProjectID,
		ag.TaskNumber,
		0, // session number — could track this per-task but 0 is fine for now
		"claude-code",
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

	// Notify tray/listeners of state change
	if m.onChangeFn != nil {
		go m.onChangeFn()
	}
}

// resolveAgentPath finds the claude binary.
// Check order: settings.yaml → exec.LookPath → platform-specific fallbacks.
func resolveAgentPath() (string, error) {
	// 1. Check settings.yaml for configured path
	settings, err := config.LoadSettings()
	if err == nil && settings != nil {
		if agentCfg, ok := settings.Agents["claude-code"]; ok && agentCfg.Path != "" {
			if _, err := os.Stat(agentCfg.Path); err == nil {
				return agentCfg.Path, nil
			}
		}
	}

	// 2. Try exec.LookPath
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	// 3. Platform-specific fallbacks
	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		homeDir + "/.claude/local/claude",
	}

	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/claude",
			"/usr/local/bin/claude",
		)
	}

	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("claude binary not found. Install Claude Code or set path in ~/.watchfire/settings.yaml")
}
