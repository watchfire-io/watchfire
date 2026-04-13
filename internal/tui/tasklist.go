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
var spinnerFrames = []string{"●", "◕", "○", "◔"}

// TaskList is the task list component for the left panel.
type TaskList struct {
	tasks         []*pb.Task
	flatItems     []taskItem // Flattened list for cursor navigation
	filteredItems []taskItem // Filtered list during search
	cursor        int
	scrollOffset  int
	height        int
	agentStatus   *pb.AgentStatus
	spinnerFrame  int

	// Search state
	searchMode  bool
	searchQuery string
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

// activeItems returns the currently visible item list (filtered or full).
func (tl *TaskList) activeItems() []taskItem {
	if tl.searchMode && tl.searchQuery != "" {
		return tl.filteredItems
	}
	return tl.flatItems
}

// SelectedTask returns the currently selected task, or nil.
func (tl *TaskList) SelectedTask() *pb.Task {
	items := tl.activeItems()
	if tl.cursor < 0 || tl.cursor >= len(items) {
		return nil
	}
	item := items[tl.cursor]
	if item.isHeader {
		return nil
	}
	return item.task
}

// StartSearch enters search mode.
func (tl *TaskList) StartSearch() {
	tl.searchMode = true
	tl.searchQuery = ""
	tl.filteredItems = nil
}

// StopSearch exits search mode and restores the full list.
func (tl *TaskList) StopSearch() {
	tl.searchMode = false
	tl.searchQuery = ""
	tl.filteredItems = nil
	// Keep cursor in bounds of full list
	if tl.cursor >= len(tl.flatItems) {
		tl.cursor = len(tl.flatItems) - 1
	}
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	tl.skipHeaders(1)
}

// ConfirmSearch exits search input but keeps the filter active.
func (tl *TaskList) ConfirmSearch() {
	tl.searchMode = false
	// Keep filteredItems and searchQuery so the filter persists until next SetTasks or StopSearch
}

// UpdateSearch filters flatItems by query (case-insensitive title match).
func (tl *TaskList) UpdateSearch(query string) {
	tl.searchQuery = query
	if query == "" {
		tl.filteredItems = nil
		return
	}
	lower := strings.ToLower(query)
	var filtered []taskItem
	for _, item := range tl.flatItems {
		if item.isHeader {
			continue
		}
		if strings.Contains(strings.ToLower(item.task.Title), lower) {
			filtered = append(filtered, item)
		}
	}
	tl.filteredItems = filtered
	// Reset cursor
	tl.cursor = 0
	tl.scrollOffset = 0
}

// MoveUp moves the cursor up, skipping headers.
func (tl *TaskList) MoveUp() {
	items := tl.activeItems()
	if len(items) == 0 {
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
	items := tl.activeItems()
	if len(items) == 0 {
		return
	}
	tl.cursor++
	if tl.cursor >= len(items) {
		tl.cursor = len(items) - 1
	}
	tl.skipHeaders(1)
	tl.ensureVisible()
}

func (tl *TaskList) skipHeaders(direction int) {
	items := tl.activeItems()
	for tl.cursor >= 0 && tl.cursor < len(items) && items[tl.cursor].isHeader {
		tl.cursor += direction
	}
	if tl.cursor < 0 {
		tl.cursor = 0
		for tl.cursor < len(items) && items[tl.cursor].isHeader {
			tl.cursor++
		}
	}
	if tl.cursor >= len(items) {
		tl.cursor = len(items) - 1
		for tl.cursor >= 0 && items[tl.cursor].isHeader {
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
	items := make([]taskItem, 0, len(tl.tasks)+3)

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
	items := tl.activeItems()
	if len(items) == 0 {
		if tl.searchMode || tl.searchQuery != "" {
			return lipgloss.NewStyle().Foreground(colorDim).Render("No matching tasks.")
		}
		return lipgloss.NewStyle().Foreground(colorDim).Render("No tasks. Press 'a' to add one.")
	}

	// Calculate available height: reserve space for scroll indicators and
	// section header blank lines (each non-first header has a "\n" prefix = +1 visual line).
	available := tl.height
	hasUpIndicator := tl.scrollOffset > 0
	if hasUpIndicator {
		available-- // reserve 1 line for "▲ more"
	}

	// Determine visible items, accounting for headers that consume an extra line
	var lines []string
	end := tl.scrollOffset
	visualLines := 0
	for end < len(items) && visualLines < available {
		item := items[end]
		cost := 1
		if item.isHeader && end > 0 {
			cost = 2 // blank line + header text
		}
		// Peek: will a "▼ more" indicator be needed after this item?
		// If so, reserve 1 line (unless this is the last item).
		remaining := available - visualLines
		if end+1 < len(items) && cost >= remaining {
			break // not enough room for this item + the ▼ indicator
		}
		visualLines += cost
		end++
	}

	// Reserve space for ▼ indicator if there are more items
	hasDownIndicator := end < len(items)
	if hasDownIndicator && visualLines >= available {
		// Remove last item to make room for the indicator
		end--
	}

	for i := tl.scrollOffset; i < end; i++ {
		item := items[i]

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
		numStr := lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf("#%04d", t.TaskNumber))
		title := fmt.Sprintf("%s %s %s", badge, numStr, t.Title)

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
	if hasUpIndicator {
		lines = append([]string{lipgloss.NewStyle().Foreground(colorDim).Render("  ▲ more")}, lines...)
	}
	if hasDownIndicator {
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
