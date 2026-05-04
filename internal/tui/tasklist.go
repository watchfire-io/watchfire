package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
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

	// Project default agent (empty = built-in claude-code). Used to decide
	// whether a per-task override is shown as a badge.
	projectDefaultAgent string

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

// SetProjectDefaultAgent records the project's default agent so the
// per-task override badge can decide whether to render.
func (tl *TaskList) SetProjectDefaultAgent(name string) {
	tl.projectDefaultAgent = strings.TrimSpace(name)
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

	// Group tasks by status. Preserve the order the task manager gave us
	// (canonical: descending by task_number) so within-group order remains
	// newest-first without re-sorting.
	groups := map[string][]*pb.Task{
		"draft": {},
		"ready": {},
		"done":  {},
	}
	for _, t := range tl.tasks {
		groups[t.Status] = append(groups[t.Status], t)
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
		agentBadge := tl.agentBadge(t)

		// Compute width budgets: reserve space for the (untruncated) fixed
		// prefix + agent badge so the title — never the badge — is the part
		// that gets shortened under pressure.
		maxWidth := width - 2 // indent prefix
		prefix := fmt.Sprintf("%s %s ", badge, numStr)
		prefixWidth := ansi.StringWidth(prefix)
		agentWidth := ansi.StringWidth(agentBadge)
		if agentBadge != "" {
			agentWidth++ // trailing space between badge and title
		}
		titleStr := t.Title
		if maxWidth > 0 {
			budget := maxWidth - prefixWidth - agentWidth
			if budget < 1 {
				budget = 1
			}
			titleStr = ansi.Truncate(titleStr, budget, "…")
		}
		var title string
		if agentBadge != "" {
			title = prefix + agentBadge + " " + titleStr
		} else {
			title = prefix + titleStr
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
			switch {
			case t.GetMergeFailureReason() != "":
				// v5.0 — merge failure is rendered with the same red style
				// as an agent failure; the badge ("[!]") + the detail view
				// reason text disambiguate the two.
				style = taskFailedStyle
			case t.Success != nil && *t.Success:
				style = taskDoneStyle
			default:
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
		switch {
		case t.GetMergeFailureReason() != "":
			// v5.0 — distinct glyph for "agent finished, merge failed" so a
			// silent run-all halt is visible at a glance from the task
			// list.
			return taskFailedStyle.Render("[!]")
		case t.Success != nil && *t.Success:
			return taskDoneStyle.Render("[✓]")
		default:
			return taskFailedStyle.Render("[✗]")
		}
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

// agentBadge returns a compact rendered badge for a task whose agent
// override differs from the project default. Empty string means no badge.
func (tl *TaskList) agentBadge(t *pb.Task) string {
	override := strings.TrimSpace(t.Agent)
	if override == "" {
		return ""
	}
	if override == tl.projectDefaultAgent {
		return ""
	}
	return agentBadgeStyle.Render(agentBadgeLabel(override))
}

// agentBadgeLabel returns the 1-3 character label for a backend name. It
// uses the backend's DisplayName() when registered, falling back to the
// raw name so unknown overrides still render something meaningful.
func agentBadgeLabel(name string) string {
	display := name
	if b, ok := backend.Get(name); ok {
		display = b.DisplayName()
	}
	initials := badgeInitials(display)
	if initials != "" {
		return initials
	}
	// Last resort: first three runes of the raw name, upper-cased.
	return truncateRunes(strings.ToUpper(name), 3)
}

// badgeInitials derives up to 3 letters from the provided display name
// (e.g. "Claude Code" → "CC", "Codex" → "C", "OpenAI Codex" → "OC").
func badgeInitials(display string) string {
	fields := strings.FieldsFunc(display, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var b strings.Builder
	for _, f := range fields {
		for _, r := range f {
			b.WriteRune(unicode.ToUpper(r))
			break
		}
		if b.Len() >= 3 {
			break
		}
	}
	return b.String()
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
