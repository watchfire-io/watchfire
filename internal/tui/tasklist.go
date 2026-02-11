package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	pb "github.com/watchfire-io/watchfire/proto"
)

// Spinner frames for active task animation.
var spinnerFrames = []string{"●", "○"}

// TaskList is the task list component for the left panel.
type TaskList struct {
	tasks        []*pb.Task
	flatItems    []taskItem // Flattened list for cursor navigation
	cursor       int
	scrollOffset int
	height       int
	agentStatus  *pb.AgentStatus
	spinnerFrame int
}

type taskItem struct {
	task      *pb.Task
	isHeader  bool
	headerStr string
}

// NewTaskList creates a new task list.
func NewTaskList() *TaskList {
	return &TaskList{}
}

// SetTasks updates the task list data and rebuilds the flat item list.
func (tl *TaskList) SetTasks(tasks []*pb.Task) {
	tl.tasks = tasks
	tl.rebuild()
	// Keep cursor in bounds
	if tl.cursor >= len(tl.flatItems) {
		tl.cursor = len(tl.flatItems) - 1
	}
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	// Skip headers
	tl.skipHeaders(1)
}

// SetAgentStatus updates the agent status for active task highlighting.
func (tl *TaskList) SetAgentStatus(status *pb.AgentStatus) {
	tl.agentStatus = status
}

// SetHeight sets the visible height.
func (tl *TaskList) SetHeight(h int) {
	tl.height = h
}

// SelectedTask returns the currently selected task, or nil.
func (tl *TaskList) SelectedTask() *pb.Task {
	if tl.cursor < 0 || tl.cursor >= len(tl.flatItems) {
		return nil
	}
	item := tl.flatItems[tl.cursor]
	if item.isHeader {
		return nil
	}
	return item.task
}

// MoveUp moves the cursor up, skipping headers.
func (tl *TaskList) MoveUp() {
	if len(tl.flatItems) == 0 {
		return
	}
	tl.cursor--
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	tl.skipHeaders(-1)
	tl.ensureVisible()
}

// MoveDown moves the cursor down, skipping headers.
func (tl *TaskList) MoveDown() {
	if len(tl.flatItems) == 0 {
		return
	}
	tl.cursor++
	if tl.cursor >= len(tl.flatItems) {
		tl.cursor = len(tl.flatItems) - 1
	}
	tl.skipHeaders(1)
	tl.ensureVisible()
}

func (tl *TaskList) skipHeaders(direction int) {
	for tl.cursor >= 0 && tl.cursor < len(tl.flatItems) && tl.flatItems[tl.cursor].isHeader {
		tl.cursor += direction
	}
	if tl.cursor < 0 {
		tl.cursor = 0
		// Find first non-header
		for tl.cursor < len(tl.flatItems) && tl.flatItems[tl.cursor].isHeader {
			tl.cursor++
		}
	}
	if tl.cursor >= len(tl.flatItems) {
		tl.cursor = len(tl.flatItems) - 1
		for tl.cursor >= 0 && tl.flatItems[tl.cursor].isHeader {
			tl.cursor--
		}
	}
}

func (tl *TaskList) ensureVisible() {
	if tl.cursor < tl.scrollOffset {
		tl.scrollOffset = tl.cursor
	}
	if tl.cursor >= tl.scrollOffset+tl.height {
		tl.scrollOffset = tl.cursor - tl.height + 1
	}
}

func (tl *TaskList) rebuild() {
	var items []taskItem

	// Group tasks by status
	groups := map[string][]*pb.Task{
		"draft": {},
		"ready": {},
		"done":  {},
	}
	for _, t := range tl.tasks {
		groups[t.Status] = append(groups[t.Status], t)
	}

	// Sort within each group by position, then task number
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool {
			if g[i].Position != g[j].Position {
				return g[i].Position < g[j].Position
			}
			return g[i].TaskNumber < g[j].TaskNumber
		})
	}

	// Build flat list: Draft, Ready, Done
	type section struct {
		name  string
		tasks []*pb.Task
	}
	sections := []section{
		{"Draft", groups["draft"]},
		{"Ready", groups["ready"]},
		{"Done", groups["done"]},
	}

	for _, sec := range sections {
		if len(sec.tasks) == 0 {
			continue
		}
		items = append(items, taskItem{
			isHeader:  true,
			headerStr: fmt.Sprintf("%s (%d)", sec.name, len(sec.tasks)),
		})
		for _, t := range sec.tasks {
			items = append(items, taskItem{task: t})
		}
	}

	tl.flatItems = items
}

// View renders the task list.
func (tl *TaskList) View(width int) string {
	if len(tl.flatItems) == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render("No tasks. Press 'a' to add one.")
	}

	var lines []string
	end := tl.scrollOffset + tl.height
	if end > len(tl.flatItems) {
		end = len(tl.flatItems)
	}

	for i := tl.scrollOffset; i < end; i++ {
		item := tl.flatItems[i]

		if item.isHeader {
			line := sectionHeaderStyle.Render(item.headerStr)
			if i > 0 {
				line = "\n" + line
			}
			lines = append(lines, line)
			continue
		}

		t := item.task
		badge := tl.taskBadge(t)
		title := fmt.Sprintf("%s #%04d %s", badge, t.TaskNumber, t.Title)

		// Truncate to fit panel width (2 for indent prefix)
		maxWidth := width - 2
		if maxWidth > 0 {
			title = ansi.Truncate(title, maxWidth, "…")
		}

		var style lipgloss.Style
		switch t.Status {
		case "draft":
			style = taskDraftStyle
		case "ready":
			if tl.isActiveTask(t) {
				style = taskActiveStyle
			} else {
				style = taskReadyStyle
			}
		case "done":
			if t.Success != nil && *t.Success {
				style = taskDoneStyle
			} else {
				style = taskFailedStyle
			}
		default:
			style = taskDraftStyle
		}

		line := style.Render(title)
		if i == tl.cursor {
			line = selectedItemStyle.Width(width).Render(title)
		}
		lines = append(lines, "  "+line)
	}

	// Scroll indicators
	if tl.scrollOffset > 0 {
		lines = append([]string{lipgloss.NewStyle().Foreground(colorDim).Render("  ▲ more")}, lines...)
	}
	if end < len(tl.flatItems) {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("  ▼ more"))
	}

	return strings.Join(lines, "\n")
}

func (tl *TaskList) taskBadge(t *pb.Task) string {
	switch t.Status {
	case "draft":
		return taskDraftStyle.Render("[ ]")
	case "ready":
		if tl.isActiveTask(t) {
			frame := spinnerFrames[tl.spinnerFrame%len(spinnerFrames)]
			return taskActiveStyle.Render("[" + frame + "]")
		}
		return taskReadyStyle.Render("[R]")
	case "done":
		if t.Success != nil && *t.Success {
			return taskDoneStyle.Render("[✓]")
		}
		return taskFailedStyle.Render("[✗]")
	}
	return "[ ]"
}

// Tick advances the spinner frame.
func (tl *TaskList) Tick() {
	tl.spinnerFrame = (tl.spinnerFrame + 1) % len(spinnerFrames)
}

func (tl *TaskList) isActiveTask(t *pb.Task) bool {
	if tl.agentStatus == nil || !tl.agentStatus.IsRunning {
		return false
	}
	return tl.agentStatus.TaskNumber == t.TaskNumber && t.Status == "ready"
}
