package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	pb "github.com/watchfire-io/watchfire/proto"
)

// LogViewer displays past agent session logs with list and detail views.
type LogViewer struct {
	logs          []*pb.LogEntry
	selectedIndex int
	viewing       bool // true = showing log content, false = showing list
	viewport      viewport.Model
	width         int
	height        int
	scrollOffset  int
	logContent    string
	logEntry      *pb.LogEntry
	loaded        bool // whether logs have been fetched at least once
}

// NewLogViewer creates a new log viewer.
func NewLogViewer() *LogViewer {
	vp := viewport.New(80, 24)
	return &LogViewer{
		viewport: vp,
	}
}

// SetSize updates dimensions.
func (l *LogViewer) SetSize(width, height int) {
	l.width = width
	l.height = height
	l.viewport.Width = width
	l.viewport.Height = height
}

// SetLogs updates the log list.
func (l *LogViewer) SetLogs(logs []*pb.LogEntry) {
	l.logs = logs
	l.loaded = true
	if l.selectedIndex >= len(logs) {
		l.selectedIndex = len(logs) - 1
	}
	if l.selectedIndex < 0 {
		l.selectedIndex = 0
	}
}

// SetLogContent sets the content for the detail view.
func (l *LogViewer) SetLogContent(entry *pb.LogEntry, content string) {
	l.logEntry = entry
	l.logContent = content
	l.viewing = true
	l.viewport.SetContent(content)
	l.viewport.GotoTop()
}

// IsViewing returns whether we're in detail view.
func (l *LogViewer) IsViewing() bool {
	return l.viewing
}

// SelectedLog returns the currently selected log entry, or nil.
func (l *LogViewer) SelectedLog() *pb.LogEntry {
	if l.selectedIndex < 0 || l.selectedIndex >= len(l.logs) {
		return nil
	}
	return l.logs[l.selectedIndex]
}

// MoveUp moves cursor up in list view.
func (l *LogViewer) MoveUp() {
	if l.viewing {
		l.viewport.LineUp(1)
		return
	}
	if l.selectedIndex > 0 {
		l.selectedIndex--
		l.ensureVisible()
	}
}

// MoveDown moves cursor down in list view.
func (l *LogViewer) MoveDown() {
	if l.viewing {
		l.viewport.LineDown(1)
		return
	}
	if l.selectedIndex < len(l.logs)-1 {
		l.selectedIndex++
		l.ensureVisible()
	}
}

// PageUp scrolls the detail viewport up.
func (l *LogViewer) PageUp() {
	if l.viewing {
		l.viewport.HalfViewUp()
	}
}

// PageDown scrolls the detail viewport down.
func (l *LogViewer) PageDown() {
	if l.viewing {
		l.viewport.HalfViewDown()
	}
}

// GoBack returns to list view from detail view.
func (l *LogViewer) GoBack() {
	l.viewing = false
	l.logContent = ""
	l.logEntry = nil
}

// Loaded returns whether logs have been fetched at least once.
func (l *LogViewer) Loaded() bool {
	return l.loaded
}

func (l *LogViewer) ensureVisible() {
	if l.selectedIndex < l.scrollOffset {
		l.scrollOffset = l.selectedIndex
	}
	if l.selectedIndex >= l.scrollOffset+l.height {
		l.scrollOffset = l.selectedIndex - l.height + 1
	}
}

// View renders the log viewer.
func (l *LogViewer) View() string {
	if l.viewing {
		return l.viewDetail()
	}
	return l.viewList()
}

func (l *LogViewer) viewList() string {
	if !l.loaded {
		return lipgloss.NewStyle().Foreground(colorDim).Width(l.width).Align(lipgloss.Center).
			Render("\nLoading logs...")
	}

	if len(l.logs) == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Width(l.width).Align(lipgloss.Center).
			Render("\nNo session logs yet.")
	}

	var lines []string
	end := l.scrollOffset + l.height
	if end > len(l.logs) {
		end = len(l.logs)
	}

	for i := l.scrollOffset; i < end; i++ {
		entry := l.logs[i]
		line := l.formatLogLine(entry)

		if i == l.selectedIndex {
			line = selectedItemStyle.Width(l.width).Render(line)
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}

	// Scroll indicators
	if l.scrollOffset > 0 {
		lines = append([]string{lipgloss.NewStyle().Foreground(colorDim).Render("  ▲ more")}, lines...)
	}
	if end < len(l.logs) {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("  ▼ more"))
	}

	return strings.Join(lines, "\n")
}

func (l *LogViewer) formatLogLine(entry *pb.LogEntry) string {
	// Format: "Task #0001 — chat — 2026-02-10 14:30 (completed)"
	var label string
	if entry.TaskNumber > 0 {
		label = fmt.Sprintf("Task #%04d", entry.TaskNumber)
	} else {
		label = capitalizeFirst(entry.Mode)
	}

	// Parse and format the timestamp
	startTime := entry.StartedAt
	if len(startTime) >= 16 {
		startTime = startTime[:10] + " " + startTime[11:16]
	}

	statusStyle := lipgloss.NewStyle().Foreground(colorDim)
	switch entry.Status {
	case "completed":
		statusStyle = lipgloss.NewStyle().Foreground(colorGreen)
	case "interrupted":
		statusStyle = lipgloss.NewStyle().Foreground(colorYellow)
	}

	return fmt.Sprintf("%s — %s — %s %s",
		lipgloss.NewStyle().Foreground(colorWhite).Bold(true).Render(label),
		lipgloss.NewStyle().Foreground(colorDim).Render(entry.Mode),
		lipgloss.NewStyle().Foreground(colorDim).Render(startTime),
		statusStyle.Render("("+entry.Status+")"),
	)
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (l *LogViewer) viewDetail() string {
	if l.logEntry == nil {
		return ""
	}

	// Header with log info
	var header string
	if l.logEntry.TaskNumber > 0 {
		header = fmt.Sprintf("Task #%04d — %s", l.logEntry.TaskNumber, l.logEntry.Mode)
	} else {
		header = capitalizeFirst(l.logEntry.Mode)
	}

	headerLine := lipgloss.NewStyle().Bold(true).Foreground(colorWhite).Render(header)
	timeLine := lipgloss.NewStyle().Foreground(colorDim).Render(
		fmt.Sprintf("%s → %s", l.logEntry.StartedAt, l.logEntry.EndedAt),
	)
	backHint := lipgloss.NewStyle().Foreground(colorDim).Render("Esc to go back · PgUp/PgDn to scroll")

	info := headerLine + "\n" + timeLine + "\n" + backHint + "\n" +
		lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", l.width)) + "\n"

	// Viewport takes remaining space
	infoLines := 4
	vpHeight := l.height - infoLines
	if vpHeight < 1 {
		vpHeight = 1
	}
	l.viewport.Height = vpHeight
	l.viewport.Width = l.width

	return info + l.viewport.View()
}
