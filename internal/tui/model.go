package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"

	pb "github.com/watchfire-io/watchfire/proto"
)

// Layout constants
const (
	defaultSplitRatio      = 0.4
	resizeDebounceInterval = 100 * time.Millisecond
	clearSavedTimeout      = 3 * time.Second
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

	// Git info
	gitInfo *pb.GitInfo

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
	confirmLogID   string

	// Status display
	err       error
	showSaved bool

	// Child components
	taskList           *TaskList
	terminal           *Terminal
	definitionView     *DefinitionView
	settingsForm       *SettingsForm
	logViewer          *LogViewer
	taskForm           *TaskForm
	globalSettingsForm *GlobalSettingsForm
	exportPicker       *ExportPicker

	// Status-bar message (e.g. "Exported watchfire-project-foo-2026-05-02.md")
	// — replaces the showSaved indicator while present, cleared by ClearSavedMsg.
	statusMessage string

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

	// Update notification
	updateVersion string

	// Reconnection state
	reconnectAttempts int
}

// NewModel creates the initial TUI model.
func NewModel(projectID string, program *programRef) Model {
	ctx, cancel := context.WithCancel(context.Background())
	return Model{
		projectID:          projectID,
		splitRatio:         defaultSplitRatio,
		taskList:           NewTaskList(),
		terminal:           NewTerminal(),
		definitionView:     NewDefinitionView(),
		settingsForm:       NewSettingsForm(),
		logViewer:          NewLogViewer(),
		globalSettingsForm: NewGlobalSettingsForm(),
		exportPicker:       NewExportPicker(),
		program:            program,
		streamCtx:          ctx,
		streamCancel:       cancel,
	}
}

// Init returns the initial commands.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		connectDaemonCmd(),
		tea.EnableMouseCellMotion,
	)
}

// resizeTickMsg is used for debounced resize.
type resizeTickMsg struct{}

// Update processes messages and returns an updated model and commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Window resize ──────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateDimensions()
		if m.connected && m.agentStatus != nil && m.agentStatus.IsRunning {
			m.pendingResize = true
			m.resizeDebounce = time.Now()
			cmds = append(cmds, tea.Tick(resizeDebounceInterval, func(_ time.Time) tea.Msg {
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
	}

	// ── All other messages ─────────────────────────────────────────
	if handled, cmd := m.handleMessage(msg); handled {
		return m, cmd
	}

	return m, nil
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
		msg := "Connecting to daemon..."
		if m.reconnectAttempts > 0 {
			if m.reconnectAttempts >= 10 {
				msg = fmt.Sprintf("Daemon unreachable (%d attempts).\nPress q to quit or r to retry.", m.reconnectAttempts)
			} else {
				msg = fmt.Sprintf("Reconnecting... (attempt %d)", m.reconnectAttempts)
			}
		}
		return lipgloss.NewStyle().
			Width(m.width).
			Height(m.height).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(colorYellow).
			Render(msg)
	}

	// Build layout
	layout := computeLayout(m.width, m.height, m.splitRatio)

	// Header
	header := renderHeader(m.project, m.leftTab, m.rightTab, m.agentStatus, m.gitInfo, m.width)

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
		case overlayGlobalSettings:
			if m.globalSettingsForm != nil {
				width := m.width / 2
				if width < 50 {
					width = 50
				}
				if width > m.width-4 {
					width = m.width - 4
				}
				m.globalSettingsForm.SetWidth(width)
				overlayContent = overlayStyle.Width(width).Render(m.globalSettingsForm.View())
			}
		case overlayExport:
			if m.exportPicker != nil {
				overlayContent = m.exportPicker.View()
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
