package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	pb "github.com/watchfire-io/watchfire/proto"
)

// Terminal is the terminal viewport component for the right panel.
//
// The TUI runs its own vt10x emulator (vtBuffer) fed by the daemon's
// SubscribeRawOutput stream so it can layer scrollback on top — the
// daemon's vt10x has a fixed-size grid and no history, so consuming
// the rendered-snapshot stream gave the user nowhere to scroll. Now
// the viewport's content = scrollback + current screen, and PgUp /
// Shift+arrows / wheel-up actually go somewhere.
type Terminal struct {
	viewport     viewport.Model
	buf          *vtBuffer
	agentStatus  *pb.AgentStatus
	issue        *pb.AgentIssue
	width        int
	height       int
	rows         int
	cols         int
	hasContent   bool
	userScrolled bool // true when user has scrolled away from bottom
}

// NewTerminal creates a new terminal component.
func NewTerminal() *Terminal {
	vp := viewport.New(80, 24)
	vp.Style = lipgloss.NewStyle()
	return &Terminal{
		viewport: vp,
		buf:      newVTBuffer(24, 80),
		rows:     24,
		cols:     80,
	}
}

// SetInputForwarder wires the emulator's terminal-query responses
// (DA1, DSR, focus, mouse, …) back to the daemon's PTY. Without this,
// the agent emits a query and then blocks waiting for an answer that
// never comes — claude in particular sends a DA1 on startup.
func (t *Terminal) SetInputForwarder(fn func([]byte)) {
	if t.buf != nil {
		t.buf.SetForwarder(fn)
	}
}

// SetSize updates terminal dimensions. Both the viewport (visible
// window) and the internal vt10x emulator (grid size) get reshaped so
// the agent's view of the terminal matches what's painted.
func (t *Terminal) SetSize(width, height int) {
	t.width = width
	t.height = height

	issueHeight := 0
	if t.issue != nil && t.issue.IssueType != "" {
		issueHeight = 1
	}

	// Always reserve 1 line for the mode header when agent is running
	modeHeight := 0
	if t.agentStatus != nil && t.agentStatus.IsRunning {
		modeHeight = 1
	}

	vpHeight := height - issueHeight - modeHeight
	if vpHeight < 1 {
		vpHeight = 1
	}
	t.viewport.Width = width
	t.viewport.Height = vpHeight

	t.rows = vpHeight
	t.cols = width
	if t.buf != nil {
		t.buf.Resize(t.rows, t.cols)
		t.refresh()
	}
}

// PTYDimensions returns (rows, cols) the daemon should size the agent
// PTY to. Equals the visible vt-buffer grid since scrollback is
// layered on top of the current screen — the agent itself only sees a
// rows×cols window.
func (t *Terminal) PTYDimensions() (rows, cols int) {
	return t.rows, t.cols
}

// SetAgentStatus updates the agent status for wildfire phase display.
func (t *Terminal) SetAgentStatus(status *pb.AgentStatus) {
	t.agentStatus = status
	t.SetSize(t.width, t.height)
}

// SetIssue updates the current agent issue.
func (t *Terminal) SetIssue(issue *pb.AgentIssue) {
	t.issue = issue
	t.SetSize(t.width, t.height)
}

// WriteRaw feeds a chunk of raw PTY bytes from the daemon's
// SubscribeRawOutput stream into the local emulator and refreshes the
// viewport.
func (t *Terminal) WriteRaw(data []byte) {
	if t.buf == nil || len(data) == 0 {
		return
	}
	t.hasContent = true
	t.buf.Write(data)
	t.refresh()
}

// refresh repaints the viewport with the buffer's current rendered
// state (scrollback + screen). Auto-bottom is preserved unless the
// user has scrolled up.
func (t *Terminal) refresh() {
	if t.buf == nil {
		return
	}
	t.viewport.SetContent(t.buf.Render())
	if !t.userScrolled {
		t.viewport.GotoBottom()
	}
}

// Clear resets the terminal buffer.
func (t *Terminal) Clear() {
	t.hasContent = false
	t.userScrolled = false
	if t.buf != nil {
		t.buf.Clear()
	}
	t.viewport.SetContent("")
}

// ScrollUp scrolls the viewport up.
func (t *Terminal) ScrollUp(n int) {
	t.viewport.ScrollUp(n)
	t.userScrolled = !t.viewport.AtBottom()
}

// ScrollDown scrolls the viewport down.
func (t *Terminal) ScrollDown(n int) {
	t.viewport.ScrollDown(n)
	t.userScrolled = !t.viewport.AtBottom()
}

// PageUp scrolls a full page up.
func (t *Terminal) PageUp() {
	t.viewport.HalfPageUp()
	t.userScrolled = !t.viewport.AtBottom()
}

// PageDown scrolls a full page down.
func (t *Terminal) PageDown() {
	t.viewport.HalfPageDown()
	t.userScrolled = !t.viewport.AtBottom()
}

// View renders the terminal.
func (t *Terminal) View() string {
	var parts []string

	// Mode header — always shown when agent is running
	if t.agentStatus != nil && t.agentStatus.IsRunning {
		parts = append(parts, t.renderModeHeader())
	}

	// Issue banner
	if t.issue != nil && t.issue.IssueType != "" {
		banner := t.renderIssueBanner()
		parts = append(parts, banner)
	}

	// Agent state - empty/stopped
	if t.agentStatus == nil || !t.agentStatus.IsRunning {
		if !t.hasContent {
			// No agent, no output
			msg := lipgloss.NewStyle().
				Foreground(colorDim).
				Width(t.width).
				Height(t.height).
				Align(lipgloss.Center).
				AlignVertical(lipgloss.Center).
				Render("No agent running. Press 's' on a task to start.")
			parts = append(parts, msg)
			return strings.Join(parts, "\n")
		}
		// Agent stopped but output exists
		parts = append(parts, t.viewport.View())
		stopped := lipgloss.NewStyle().Foreground(colorDim).Render("Agent stopped.")
		parts = append(parts, stopped)
		return strings.Join(parts, "\n")
	}

	parts = append(parts, t.viewport.View())
	return strings.Join(parts, "\n")
}

func (t *Terminal) renderModeHeader() string {
	s := t.agentStatus
	var label string
	style := modeHeaderStyle

	switch s.Mode {
	case "chat":
		label = "Chat"
	case "task":
		label = fmt.Sprintf("Task #%04d", s.TaskNumber)
		if s.TaskTitle != "" {
			label += ": " + s.TaskTitle
		}
	case "wildfire":
		style = modeHeaderWildfireStyle
		label = "Wildfire"
		switch s.WildfirePhase {
		case "execute":
			label = fmt.Sprintf("Wildfire — Execute #%04d", s.TaskNumber)
		case "refine":
			label = "Wildfire — Refine"
		case "generate":
			label = "Wildfire — Generate"
		}
	case "start-all":
		label = fmt.Sprintf("Start All — #%04d", s.TaskNumber)
	case "generate-definition":
		label = "Generate Definition"
	case "generate-tasks":
		label = "Generate Tasks"
	default:
		label = s.Mode
	}

	return style.Width(t.width).Render(label)
}

func (t *Terminal) renderIssueBanner() string {
	issue := t.issue
	var text string
	switch issue.IssueType {
	case "auth_required":
		text = "⚠ Authentication required — switch to Chat and run /login"
	case "rate_limited":
		text = "⚠ Rate limited"
		if issue.ResetAt != nil {
			text += fmt.Sprintf(" — resets at %s. Press R to resume.", issue.ResetAt.AsTime().Format("15:04:05"))
		} else {
			text += ". Press R to resume."
		}
	default:
		text = "⚠ " + issue.Message
	}
	return issueBannerStyle.Width(t.width).Render(text)
}
