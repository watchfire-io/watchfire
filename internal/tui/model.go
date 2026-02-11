package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"

	pb "github.com/watchfire-io/watchfire/proto"
)

// Model is the root Bubbletea model for the TUI.
type Model struct {
	// gRPC connection
	conn      *grpc.ClientConn
	projectID string
	connected bool

	// Project and task data
	project      *pb.Project
	tasks        []*pb.Task
	agentStatus  *pb.AgentStatus
	currentIssue *pb.AgentIssue

	// UI state
	leftTab       int     // 0=Tasks, 1=Definition, 2=Settings
	rightTab      int     // 0=Chat, 1=Logs
	focusedPanel  int     // 0=left, 1=right
	activeOverlay int     // overlayNone, overlayHelp, overlayAddTask, overlayEditTask
	splitRatio    float64 // Default 0.4
	width         int
	height        int

	// Confirm mode
	confirmMode    int
	confirmTaskNum int32

	// Status display
	err       error
	showSaved bool

	// Child components
	taskList       *TaskList
	terminal       *Terminal
	definitionView *DefinitionView
	settingsForm   *SettingsForm
	logViewer      *LogViewer
	taskForm       *TaskForm

	// Program reference for goroutine Send()
	program *programRef

	// Streaming state
	subscribed    bool
	agentPolling  bool
	autoStartDone bool
	streamCtx     context.Context
	streamCancel  context.CancelFunc

	// Resize debounce
	pendingResize  bool
	resizeDebounce time.Time

	// Spinner state
	spinnerRunning bool

	// Dragging state
	dragging bool
}

// NewModel creates the initial TUI model.
func NewModel(projectID string, program *programRef) Model {
	ctx, cancel := context.WithCancel(context.Background())
	return Model{
		projectID:      projectID,
		splitRatio:     0.4,
		taskList:       NewTaskList(),
		terminal:       NewTerminal(),
		definitionView: NewDefinitionView(),
		settingsForm:   NewSettingsForm(),
		logViewer:      NewLogViewer(),
		program:        program,
		streamCtx:      ctx,
		streamCancel:   cancel,
	}
}

// Init returns the initial commands.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		connectDaemonCmd(),
		tea.EnableMouseAllMotion,
	)
}

// Update processes messages and returns an updated model and commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Window resize ──────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateDimensions()
		// Debounced agent resize
		if m.connected && m.agentStatus != nil && m.agentStatus.IsRunning {
			m.pendingResize = true
			m.resizeDebounce = time.Now()
			cmds = append(cmds, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
				return resizeTickMsg{}
			}))
		}
		return m, tea.Batch(cmds...)

	case resizeTickMsg:
		if m.pendingResize && time.Since(m.resizeDebounce) >= 90*time.Millisecond {
			m.pendingResize = false
			rows, cols := m.ptyDimensions()
			cmds = append(cmds, resizeAgentCmd(m.conn, m.projectID, rows, cols))
		}
		return m, tea.Batch(cmds...)

	// ── Key events ─────────────────────────────────────────────────
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	// ── Mouse events ───────────────────────────────────────────────
	case tea.MouseMsg:
		cmd := m.handleMouse(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	// ── Daemon connection ──────────────────────────────────────────
	case DaemonConnectedMsg:
		m.conn = msg.Conn
		m.connected = true
		cmds = append(cmds,
			loadProjectCmd(m.conn, m.projectID),
			loadTasksCmd(m.conn, m.projectID),
			getAgentStatusCmd(m.conn, m.projectID),
		)
		return m, tea.Batch(cmds...)

	case DaemonDisconnectedMsg:
		return m, m.doQuit()

	case ReconnectMsg:
		if !m.connected {
			cmds = append(cmds, connectDaemonCmd())
		}
		return m, tea.Batch(cmds...)

	// ── Project data ───────────────────────────────────────────────
	case ProjectLoadedMsg:
		m.project = msg.Project
		m.definitionView.SetContent(msg.Project.Definition)
		m.settingsForm.LoadFromProject(msg.Project)
		return m, nil

	case ProjectSavedMsg:
		m.project = msg.Project
		m.definitionView.SetContent(msg.Project.Definition)
		m.settingsForm.LoadFromProject(msg.Project)
		m.showSaved = true
		cmds = append(cmds, clearSavedAfter(3*time.Second))
		return m, tea.Batch(cmds...)

	// ── Task data ──────────────────────────────────────────────────
	case TasksLoadedMsg:
		m.tasks = msg.Tasks
		m.taskList.SetTasks(msg.Tasks)
		m.taskList.SetAgentStatus(m.agentStatus)
		return m, nil

	case TaskSavedMsg:
		m.activeOverlay = overlayNone
		m.taskForm = nil
		cmds = append(cmds, loadTasksCmd(m.conn, m.projectID))
		return m, tea.Batch(cmds...)

	case TaskDeletedMsg:
		m.confirmMode = confirmNone
		cmds = append(cmds, loadTasksCmd(m.conn, m.projectID))
		return m, tea.Batch(cmds...)

	// ── Agent status ───────────────────────────────────────────────
	case AgentStatusMsg:
		m.agentStatus = msg.Status
		m.taskList.SetAgentStatus(msg.Status)
		if msg.Status != nil {
			m.terminal.SetAgentStatus(msg.Status)
			if msg.Status.Issue != nil && msg.Status.Issue.IssueType != "" {
				m.currentIssue = msg.Status.Issue
				m.terminal.SetIssue(msg.Status.Issue)
			}
		}

		// Subscribe to streams if agent running and not yet subscribed
		if msg.Status != nil && msg.Status.IsRunning && !m.subscribed && m.program != nil {
			m.subscribed = true
			rows, cols := m.ptyDimensions()
			cmds = append(cmds,
				subscribeScreenCmd(m.streamCtx, m.conn, m.projectID, rows, cols, m.program),
				subscribeAgentIssuesCmd(m.streamCtx, m.conn, m.projectID, m.program),
			)
		}

		// Auto-start chat if no agent running and we haven't done it yet
		if msg.Status != nil && !msg.Status.IsRunning && !m.autoStartDone && m.connected {
			m.autoStartDone = true
			rows, cols := m.ptyDimensions()
			cmds = append(cmds, startAgentCmd(m.conn, m.projectID, "chat", 0, rows, cols))
		}

		// Start polling if agent running
		if msg.Status != nil && msg.Status.IsRunning && !m.agentPolling {
			m.agentPolling = true
			cmds = append(cmds, pollAgentStatusTick())
		}

		// Start spinner if agent running
		if msg.Status != nil && msg.Status.IsRunning && !m.spinnerRunning {
			m.spinnerRunning = true
			cmds = append(cmds, spinnerTick())
		}

		return m, tea.Batch(cmds...)

	case AgentStartedMsg:
		m.agentStatus = msg.Status
		m.terminal.SetAgentStatus(msg.Status)
		m.terminal.Clear()
		m.taskList.SetAgentStatus(msg.Status)
		m.subscribed = false // Will resubscribe on next status check
		cmds = append(cmds, getAgentStatusCmd(m.conn, m.projectID))
		return m, tea.Batch(cmds...)

	case AgentStoppedMsg:
		m.agentStatus = nil
		m.subscribed = false
		m.agentPolling = false
		m.spinnerRunning = false
		m.terminal.SetAgentStatus(nil)
		m.taskList.SetAgentStatus(nil)
		cmds = append(cmds, loadTasksCmd(m.conn, m.projectID))
		return m, tea.Batch(cmds...)

	// ── Terminal output ────────────────────────────────────────────
	case ScreenUpdateMsg:
		m.terminal.SetContent(msg.AnsiContent)
		return m, nil

	case ScreenEndedMsg:
		m.subscribed = false
		// Refresh agent status to check if it's still running
		cmds = append(cmds, getAgentStatusCmd(m.conn, m.projectID))
		return m, tea.Batch(cmds...)

	// ── Agent issues ───────────────────────────────────────────────
	case AgentIssueMsg:
		if msg.Issue.IssueType == "" {
			m.currentIssue = nil
			m.terminal.SetIssue(nil)
		} else {
			m.currentIssue = msg.Issue
			m.terminal.SetIssue(msg.Issue)
		}
		return m, nil

	// ── Spinner tick ──────────────────────────────────────────────
	case spinnerTickMsg:
		if m.agentStatus != nil && m.agentStatus.IsRunning {
			m.taskList.Tick()
			cmds = append(cmds, spinnerTick())
		} else {
			m.spinnerRunning = false
		}
		return m, tea.Batch(cmds...)

	// ── Polling tick ───────────────────────────────────────────────
	case TickMsg:
		if m.connected && m.agentPolling {
			cmds = append(cmds,
				getAgentStatusCmd(m.conn, m.projectID),
				loadTasksCmd(m.conn, m.projectID),
				pollAgentStatusTick(),
			)
		}
		return m, tea.Batch(cmds...)

	// ── Error handling ─────────────────────────────────────────────
	case ErrorMsg:
		m.err = msg.Err
		cmds = append(cmds, clearErrorAfter(5*time.Second))
		return m, tea.Batch(cmds...)

	case ClearErrorMsg:
		m.err = nil
		return m, nil

	case ClearSavedMsg:
		m.showSaved = false
		return m, nil

	// ── Log viewer ────────────────────────────────────────────────
	case LogsLoadedMsg:
		m.logViewer.SetLogs(msg.Logs)
		return m, nil

	case LogContentMsg:
		m.logViewer.SetLogContent(msg.Entry, msg.Content)
		return m, nil

	// ── Editor finished ────────────────────────────────────────────
	case EditorFinishedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			cmds = append(cmds, clearErrorAfter(5*time.Second))
		} else if m.project != nil && msg.Content != m.project.Definition {
			cmds = append(cmds, updateProjectCmd(m.conn, m.projectID, map[string]interface{}{
				"definition": msg.Content,
			}))
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

// resizeTickMsg is used for debounced resize.
type resizeTickMsg struct{}

// handleKey processes key events.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Confirm mode captures everything
	if m.confirmMode != confirmNone {
		return m.handleConfirmKey(msg)
	}

	// Overlay captures everything except global shortcuts
	if m.activeOverlay != overlayNone {
		return m.handleOverlayKey(msg)
	}

	// Global shortcuts (always work)
	switch {
	case key.Matches(msg, globalKeys.Quit):
		if m.agentStatus != nil && m.agentStatus.IsRunning {
			m.confirmMode = confirmQuit
			return nil
		}
		return m.doQuit()

	case msg.Type == tea.KeyCtrlC:
		// Ctrl+C: quit when left panel focused or no agent running.
		// When right panel focused + agent running, fall through to terminal handler.
		if m.focusedPanel == 0 || m.agentStatus == nil || !m.agentStatus.IsRunning {
			if m.agentStatus != nil && m.agentStatus.IsRunning {
				m.confirmMode = confirmQuit
				return nil
			}
			return m.doQuit()
		}
		// Fall through — will be handled by handleRightPanelKey → handleTerminalKey

	case key.Matches(msg, globalKeys.Help):
		if m.activeOverlay == overlayHelp {
			m.activeOverlay = overlayNone
		} else {
			m.activeOverlay = overlayHelp
		}
		return nil

	case key.Matches(msg, globalKeys.Tab):
		m.focusedPanel = 1 - m.focusedPanel
		return nil
	}

	// Tab switching (only when left panel focused and not in terminal)
	if m.focusedPanel == 0 {
		switch {
		case key.Matches(msg, tabSwitchKeys.Tab1):
			m.leftTab = 0
			return nil
		case key.Matches(msg, tabSwitchKeys.Tab2):
			m.leftTab = 1
			return nil
		case key.Matches(msg, tabSwitchKeys.Tab3):
			m.leftTab = 2
			return nil
		}
	}

	// Route to focused panel
	if m.focusedPanel == 0 {
		return m.handleLeftPanelKey(msg)
	}
	return m.handleRightPanelKey(msg)
}

func (m *Model) handleLeftPanelKey(msg tea.KeyMsg) tea.Cmd {
	switch m.leftTab {
	case 0: // Tasks
		return m.handleTaskListKey(msg)
	case 1: // Definition
		return m.handleDefinitionKey(msg)
	case 2: // Settings
		return m.handleSettingsKey(msg)
	}
	return nil
}

func (m *Model) handleRightPanelKey(msg tea.KeyMsg) tea.Cmd {
	switch m.rightTab {
	case 0: // Chat/Terminal
		return m.handleTerminalKey(msg)
	case 1: // Logs
		return m.handleLogKey(msg)
	}
	return nil
}

func (m *Model) handleTaskListKey(msg tea.KeyMsg) tea.Cmd {
	agentRunning := m.agentStatus != nil && m.agentStatus.IsRunning

	switch {
	case key.Matches(msg, taskListKeys.Up):
		m.taskList.MoveUp()
	case key.Matches(msg, taskListKeys.Down):
		m.taskList.MoveDown()
	case key.Matches(msg, taskListKeys.Add):
		m.openAddTaskForm()
	case key.Matches(msg, taskListKeys.Edit), key.Matches(msg, taskListKeys.Enter):
		m.openEditTaskForm()
	case key.Matches(msg, taskListKeys.Stop):
		if agentRunning && m.conn != nil {
			m.confirmMode = confirmStop
		}
		return nil
	case key.Matches(msg, taskListKeys.Wildfire):
		if m.conn != nil {
			rows, cols := m.ptyDimensions()
			return startAgentCmd(m.conn, m.projectID, "wildfire", 0, rows, cols)
		}
	case key.Matches(msg, taskListKeys.StartAll):
		if m.conn != nil {
			rows, cols := m.ptyDimensions()
			return startAgentCmd(m.conn, m.projectID, "start-all", 0, rows, cols)
		}
	case key.Matches(msg, taskListKeys.Start):
		return m.startSelectedTask()
	case key.Matches(msg, taskListKeys.Ready):
		return m.setSelectedTaskStatus("ready")
	case key.Matches(msg, taskListKeys.Draft):
		return m.setSelectedTaskStatus("draft")
	case key.Matches(msg, taskListKeys.Done):
		return m.setSelectedTaskDone()
	case key.Matches(msg, taskListKeys.Delete):
		return m.confirmDeleteTask()
	}
	return nil
}

func (m *Model) handleDefinitionKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, definitionKeys.Edit):
		if m.project != nil {
			return launchEditorCmd(m.project.Definition)
		}
	case key.Matches(msg, definitionKeys.Up):
		m.definitionView.ScrollUp()
	case key.Matches(msg, definitionKeys.Down):
		m.definitionView.ScrollDown()
	}
	return nil
}

func (m *Model) handleSettingsKey(msg tea.KeyMsg) tea.Cmd {
	if m.settingsForm.IsEditing() {
		switch msg.Type {
		case tea.KeyEnter:
			changed, k, v := m.settingsForm.FinishEdit()
			if changed {
				return updateProjectCmd(m.conn, m.projectID, map[string]interface{}{k: v})
			}
			return nil
		case tea.KeyEscape:
			m.settingsForm.CancelEdit()
			return nil
		default:
			// Forward to text input
			ti := m.settingsForm.InputModel()
			newTI, _ := ti.Update(msg)
			*ti = newTI
			return nil
		}
	}

	switch {
	case key.Matches(msg, settingsKeys.Up):
		m.settingsForm.MoveUp()
	case key.Matches(msg, settingsKeys.Down):
		m.settingsForm.MoveDown()
	case key.Matches(msg, settingsKeys.Toggle):
		changed, k, v := m.settingsForm.Toggle()
		if changed {
			return updateProjectCmd(m.conn, m.projectID, map[string]interface{}{k: v})
		}
	case key.Matches(msg, settingsKeys.Enter):
		if m.settingsForm.StartEdit() {
			return nil
		}
		// If it's a toggle field, toggle it
		changed, k, v := m.settingsForm.Toggle()
		if changed {
			return updateProjectCmd(m.conn, m.projectID, map[string]interface{}{k: v})
		}
	}
	return nil
}

func (m *Model) handleTerminalKey(msg tea.KeyMsg) tea.Cmd {
	// Check for resume on issue
	if m.currentIssue != nil && m.currentIssue.IssueType != "" {
		if key.Matches(msg, terminalKeys.Resume) {
			return resumeAgentCmd(m.conn, m.projectID)
		}
	}

	// Special keys
	switch msg.Type {
	case tea.KeyPgUp:
		m.terminal.PageUp()
		return nil
	case tea.KeyPgDown:
		m.terminal.PageDown()
		return nil
	}

	// Forward all other input to agent
	if m.agentStatus != nil && m.agentStatus.IsRunning && m.conn != nil {
		var data []byte
		switch msg.Type {
		case tea.KeyEnter:
			data = []byte{'\r'}
		case tea.KeyBackspace:
			data = []byte{127}
		case tea.KeyCtrlC:
			data = []byte{3}
		case tea.KeyCtrlD:
			data = []byte{4}
		case tea.KeyCtrlZ:
			data = []byte{26}
		case tea.KeyCtrlL:
			data = []byte{12}
		case tea.KeyEsc:
			data = []byte{27}
		case tea.KeyUp:
			data = []byte{27, '[', 'A'}
		case tea.KeyDown:
			data = []byte{27, '[', 'B'}
		case tea.KeyRight:
			data = []byte{27, '[', 'C'}
		case tea.KeyLeft:
			data = []byte{27, '[', 'D'}
		case tea.KeySpace:
			data = []byte{' '}
		default:
			if msg.Type == tea.KeyRunes {
				data = []byte(string(msg.Runes))
			}
		}
		if len(data) > 0 {
			return sendInputCmd(m.conn, m.projectID, data)
		}
	}
	return nil
}

func (m *Model) handleLogKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyUp:
		m.logViewer.MoveUp()
		return nil
	case tea.KeyDown:
		m.logViewer.MoveDown()
		return nil
	case tea.KeyPgUp:
		m.logViewer.PageUp()
		return nil
	case tea.KeyPgDown:
		m.logViewer.PageDown()
		return nil
	case tea.KeyEnter:
		if !m.logViewer.IsViewing() {
			entry := m.logViewer.SelectedLog()
			if entry != nil && m.conn != nil {
				return getLogCmd(m.conn, m.projectID, entry.LogId)
			}
		}
		return nil
	case tea.KeyEscape:
		if m.logViewer.IsViewing() {
			m.logViewer.GoBack()
		}
		return nil
	}

	switch msg.String() {
	case "k":
		m.logViewer.MoveUp()
	case "j":
		m.logViewer.MoveDown()
	}
	return nil
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, confirmKeys.Yes):
		switch m.confirmMode {
		case confirmDelete:
			m.confirmMode = confirmNone
			return deleteTaskCmd(m.conn, m.projectID, m.confirmTaskNum)
		case confirmQuit:
			m.confirmMode = confirmNone
			return m.doQuit()
		case confirmStop:
			m.confirmMode = confirmNone
			return stopAgentCmd(m.conn, m.projectID)
		}
	case key.Matches(msg, confirmKeys.No), key.Matches(msg, confirmKeys.Cancel):
		m.confirmMode = confirmNone
	}
	return nil
}

func (m *Model) handleOverlayKey(msg tea.KeyMsg) tea.Cmd {
	switch m.activeOverlay {
	case overlayHelp:
		// Any key closes help
		if key.Matches(msg, overlayKeys.Cancel) || key.Matches(msg, globalKeys.Help) {
			m.activeOverlay = overlayNone
		}
		return nil

	case overlayAddTask, overlayEditTask:
		return m.handleTaskFormKey(msg)
	}
	return nil
}

func (m *Model) handleTaskFormKey(msg tea.KeyMsg) tea.Cmd {
	if m.taskForm == nil {
		return nil
	}

	switch {
	case key.Matches(msg, overlayKeys.Save):
		return m.saveTaskForm()
	case key.Matches(msg, overlayKeys.Cancel):
		m.activeOverlay = overlayNone
		m.taskForm = nil
		return nil
	case key.Matches(msg, overlayKeys.Tab):
		m.taskForm.FocusNext()
		return nil
	}

	// Status field: toggle on space/enter
	if m.taskForm.FocusIndex() == 3 {
		if msg.Type == tea.KeySpace || msg.Type == tea.KeyEnter {
			m.taskForm.ToggleStatus()
		}
		return nil
	}

	// Forward to active input
	switch m.taskForm.FocusIndex() {
	case 0:
		ti := m.taskForm.TitleInput()
		newTI, _ := ti.Update(msg)
		*ti = newTI
	case 1:
		ta := m.taskForm.PromptArea()
		newTA, _ := ta.Update(msg)
		*ta = newTA
	case 2:
		ta := m.taskForm.CriteriaArea()
		newTA, _ := ta.Update(msg)
		*ta = newTA
	}

	return nil
}

// ── Task actions ─────────────────────────────────────────────────

func (m *Model) openAddTaskForm() {
	formWidth := m.width - 10
	if formWidth > 70 {
		formWidth = 70
	}
	m.taskForm = NewTaskForm("add", formWidth)
	m.activeOverlay = overlayAddTask
}

func (m *Model) openEditTaskForm() {
	t := m.taskList.SelectedTask()
	if t == nil {
		return
	}
	formWidth := m.width - 10
	if formWidth > 70 {
		formWidth = 70
	}
	m.taskForm = NewTaskForm("edit", formWidth)
	m.taskForm.PreFill(t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria, t.Status)
	m.activeOverlay = overlayEditTask
}

func (m *Model) saveTaskForm() tea.Cmd {
	if m.taskForm == nil || m.conn == nil {
		return nil
	}

	title := m.taskForm.Title()
	if title == "" {
		m.err = errTitleRequired
		return clearErrorAfter(3 * time.Second)
	}

	if m.taskForm.mode == "add" {
		return createTaskCmd(
			m.conn, m.projectID,
			title,
			m.taskForm.Prompt(),
			m.taskForm.Criteria(),
			m.taskForm.Status(),
		)
	}

	// Edit mode
	updates := map[string]interface{}{
		"title":    title,
		"prompt":   m.taskForm.Prompt(),
		"criteria": m.taskForm.Criteria(),
		"status":   m.taskForm.Status(),
	}
	return updateTaskCmd(m.conn, m.projectID, m.taskForm.taskNumber, updates)
}

func (m *Model) startSelectedTask() tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil || m.conn == nil {
		return nil
	}
	rows, cols := m.ptyDimensions()
	return startAgentCmd(m.conn, m.projectID, "task", t.TaskNumber, rows, cols)
}

func (m *Model) setSelectedTaskStatus(status string) tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil || m.conn == nil {
		return nil
	}
	return updateTaskCmd(m.conn, m.projectID, t.TaskNumber, map[string]interface{}{
		"status": status,
	})
}

func (m *Model) setSelectedTaskDone() tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil || m.conn == nil {
		return nil
	}
	return updateTaskCmd(m.conn, m.projectID, t.TaskNumber, map[string]interface{}{
		"status":  "done",
		"success": true,
	})
}

func (m *Model) confirmDeleteTask() tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil {
		return nil
	}
	m.confirmMode = confirmDelete
	m.confirmTaskNum = t.TaskNumber
	return nil
}

// doQuit performs clean shutdown: cancel streams, clear program ref, close connection, quit.
func (m *Model) doQuit() tea.Cmd {
	m.streamCancel()
	m.program.Clear()
	if m.conn != nil {
		_ = m.conn.Close()
	}
	return tea.Quit
}

// ── Mouse handling ───────────────────────────────────────────────

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Action {
	case tea.MouseActionPress:
		layout := computeLayout(m.width, m.height, m.splitRatio)
		x := msg.X

		// Check if clicking on divider
		if x >= layout.dividerCol-1 && x <= layout.dividerCol+1 {
			m.dragging = true
			return nil
		}

		// Click on left panel
		if x < layout.dividerCol {
			m.focusedPanel = 0
		} else {
			m.focusedPanel = 1
		}

		// Check if clicking on header (y == 0) for tab switching
		if msg.Y == 0 {
			return m.handleHeaderClick(msg.X)
		}

	case tea.MouseActionRelease:
		m.dragging = false

	case tea.MouseActionMotion:
		if m.dragging {
			ratio := float64(msg.X) / float64(m.width)
			if ratio < 0.2 {
				ratio = 0.2
			}
			if ratio > 0.8 {
				ratio = 0.8
			}
			m.splitRatio = ratio
			m.updateDimensions()
		}
	}

	// Scroll in focused panel
	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.focusedPanel == 0 {
				switch m.leftTab {
				case 0:
					m.taskList.MoveUp()
				case 1:
					m.definitionView.ScrollUp()
				}
			} else {
				m.terminal.ScrollUp(3)
			}
		case tea.MouseButtonWheelDown:
			if m.focusedPanel == 0 {
				switch m.leftTab {
				case 0:
					m.taskList.MoveDown()
				case 1:
					m.definitionView.ScrollDown()
				}
			} else {
				m.terminal.ScrollDown(3)
			}
		}
	}

	return nil
}

func (m *Model) handleHeaderClick(x int) tea.Cmd {
	// Simple heuristic: left tabs are roughly in the first 40% of width
	// Right tabs are in the right portion
	// This is approximate but works for click detection
	layout := computeLayout(m.width, m.height, m.splitRatio)
	if x < layout.dividerCol {
		// Check approximate tab positions in left area
		// "Tasks | Definition | Settings" starts after project name (~15 chars)
		offset := 15
		tabWidth := 12
		tabIdx := (x - offset) / tabWidth
		if tabIdx >= 0 && tabIdx <= 2 {
			m.leftTab = tabIdx
			m.focusedPanel = 0
		}
	} else {
		// Right tabs: "Chat | Logs"
		rightStart := layout.dividerCol
		rightOffset := (x - rightStart)
		if rightOffset < 15 {
			m.rightTab = 0
		} else {
			m.rightTab = 1
			return m.loadLogsIfNeeded()
		}
		m.focusedPanel = 1
	}
	return nil
}

// loadLogsIfNeeded fetches logs from daemon when switching to the Logs tab.
func (m *Model) loadLogsIfNeeded() tea.Cmd {
	if m.conn == nil {
		return nil
	}
	return listLogsCmd(m.conn, m.projectID)
}

// ── Dimension helpers ────────────────────────────────────────────

// ptyDimensions returns the rows/cols the agent PTY should use,
// derived from the current TUI layout.
func (m *Model) ptyDimensions() (rows, cols int) {
	if m.width == 0 || m.height == 0 {
		return 0, 0
	}
	layout := computeLayout(m.width, m.height, m.splitRatio)
	rows = layout.contentHeight - 3 // -2 border, -1 mode header
	cols = layout.rightWidth - 2    // -2 border
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	return rows, cols
}

func (m *Model) updateDimensions() {
	layout := computeLayout(m.width, m.height, m.splitRatio)
	innerHeight := layout.contentHeight - 2
	leftInner := layout.leftWidth - 2
	rightInner := layout.rightWidth - 2

	if innerHeight < 1 {
		innerHeight = 1
	}
	if leftInner < 1 {
		leftInner = 1
	}
	if rightInner < 1 {
		rightInner = 1
	}

	m.taskList.SetHeight(innerHeight)
	m.terminal.SetSize(rightInner, innerHeight)
	m.definitionView.SetSize(leftInner, innerHeight)
	m.settingsForm.SetSize(leftInner, innerHeight)
	m.logViewer.SetSize(rightInner, innerHeight)
}

// ── View ─────────────────────────────────────────────────────────

// View renders the TUI.
func (m Model) View() string {
	// Minimum size check
	if m.width < 80 || m.height < 24 {
		sizeStr := fmt.Sprintf("%dx%d", m.width, m.height)
		msg := lipgloss.NewStyle().
			Width(m.width).
			Height(m.height).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(colorYellow).
			Render(lipgloss.JoinVertical(lipgloss.Center,
				"Terminal too small",
				lipgloss.NewStyle().Foreground(colorDim).Render(
					"Need 80x24, have "+lipgloss.NewStyle().Bold(true).Render(sizeStr),
				),
			))
		return msg
	}

	// Not connected yet
	if !m.connected {
		return lipgloss.NewStyle().
			Width(m.width).
			Height(m.height).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(colorDim).
			Render("Connecting to daemon...")
	}

	// Build layout
	layout := computeLayout(m.width, m.height, m.splitRatio)

	// Header
	header := renderHeader(m.project, m.leftTab, m.rightTab, m.agentStatus, m.width)

	// Left panel content
	leftContent := m.renderLeftPanel(layout.leftWidth - 2)

	// Right panel content
	rightContent := m.renderRightPanel(layout.rightWidth - 2)

	// Panels
	panels := renderPanels(leftContent, rightContent, layout, m.focusedPanel)

	// Status bar
	statusBar := renderStatusBar(&m, m.width)

	// Compose
	view := lipgloss.JoinVertical(lipgloss.Left, header, panels, statusBar)

	// Overlay
	if m.activeOverlay != overlayNone {
		var overlayContent string
		switch m.activeOverlay {
		case overlayHelp:
			overlayContent = renderHelp(m.width)
		case overlayAddTask, overlayEditTask:
			if m.taskForm != nil {
				overlayContent = m.taskForm.View()
			}
		}
		if overlayContent != "" {
			view = renderOverlay(view, overlayContent, m.width, m.height)
		}
	}

	return view
}

func (m Model) renderLeftPanel(width int) string {
	switch m.leftTab {
	case 0:
		return m.taskList.View(width)
	case 1:
		return m.definitionView.View()
	case 2:
		return m.settingsForm.View()
	}
	return ""
}

func (m Model) renderRightPanel(width int) string {
	switch m.rightTab {
	case 0:
		return m.terminal.View()
	case 1:
		return m.logViewer.View()
	}
	return ""
}

// sentinel errors
var errTitleRequired = errString("title is required")

type errString string

func (e errString) Error() string { return string(e) }
