package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	pb "github.com/watchfire-io/watchfire/proto"
)

// ── Task actions ─────────────────────────────────────────────────

func (m *Model) openAddTaskForm() {
	formWidth := m.width - 10
	if formWidth > 70 {
		formWidth = 70
	}
	m.taskForm = NewTaskForm("add", formWidth)
	m.taskForm.SetProjectDefaultAgent(m.projectDefaultAgent())
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
	m.taskForm.SetProjectDefaultAgent(m.projectDefaultAgent())
	m.taskForm.PreFill(t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria, t.Status, t.Agent)
	m.activeOverlay = overlayEditTask
}

func (m *Model) projectDefaultAgent() string {
	if m.project == nil {
		return ""
	}
	return m.project.DefaultAgent
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
			m.taskForm.Agent(),
		)
	}

	// Edit mode. Always include agent so clearing the override (empty string)
	// is transmitted.
	agent := m.taskForm.Agent()
	updates := map[string]interface{}{
		"title":    title,
		"prompt":   m.taskForm.Prompt(),
		"criteria": m.taskForm.Criteria(),
		"status":   m.taskForm.Status(),
		"agent":    agent,
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

// restoreSelectedTask fires the RestoreTask RPC for the currently-selected
// soft-deleted task. The watcher's debounced file event triggers the
// follow-up ListTasks reload; in the meantime the TUI optimistically
// schedules its own load so the user sees the row return without waiting
// for the watcher tick.
func (m *Model) restoreSelectedTask() tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil || m.conn == nil {
		return nil
	}
	return restoreTaskCmd(m.conn, m.projectID, t.TaskNumber)
}

// confirmPermanentDeleteTask arms the y/N prompt for hard-deleting the
// selected trash row. The actual RPC fires from handleConfirmKey.
func (m *Model) confirmPermanentDeleteTask() tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil {
		return nil
	}
	m.confirmMode = confirmPermanentDelete
	m.confirmTaskNum = t.TaskNumber
	return nil
}

// confirmDeleteLog arms the confirmation prompt for deleting the selected log.
// Returns nil if no log is selected or if a delete is already in flight.
func (m *Model) confirmDeleteLog() tea.Cmd {
	if m.logViewer.IsDeleting() {
		return nil
	}
	entry := m.logViewer.SelectedLog()
	if entry == nil {
		return nil
	}
	m.confirmMode = confirmDeleteLog
	m.confirmLogID = entry.LogId
	return nil
}

// moveTaskUp / moveTaskDown swap the focused active task with its
// in-bounds same-status neighbour and dispatch a TaskService.ReorderTasks
// RPC with the full new ordering. Local state mutates optimistically so
// the cursor stays glued to the moved row across repeated presses; on
// RPC failure the previous order is restored from preReorderTasks and
// the error bar shows a one-shot toast.
func (m *Model) moveTaskUp() tea.Cmd   { return m.moveSelectedTask(-1) }
func (m *Model) moveTaskDown() tea.Cmd { return m.moveSelectedTask(+1) }

func (m *Model) moveSelectedTask(direction int) tea.Cmd {
	if m.taskList == nil || m.taskList.TrashMode() {
		return nil
	}
	t := m.taskList.SelectedTask()
	if t == nil || t.GetDeletedAt() != nil {
		return nil
	}
	if m.conn == nil {
		return nil
	}

	active := activeTasksInDisplayOrder(m.tasks)
	idx := indexOfTaskNumber(active, t.TaskNumber)
	if idx < 0 {
		return nil
	}
	newOrder, ok := reorderActiveTasks(active, idx, direction)
	if !ok {
		return nil
	}

	nums := make([]int32, len(newOrder))
	for i, task := range newOrder {
		nums[i] = task.TaskNumber
	}

	// Snapshot pre-swap state once per gesture so a chain of moves
	// followed by a single failure still reverts to the user's
	// pre-gesture view rather than the last optimistic intermediate.
	if !m.inFlightReorder {
		m.preReorderTasks = append([]*pb.Task(nil), m.tasks...)
	}

	m.tasks = mergeActiveWithDeleted(newOrder, m.tasks)
	m.taskList.SetTasks(m.tasks)
	m.taskList.SelectTaskByNumber(t.TaskNumber)
	m.inFlightReorder = true

	return reorderTasksCmd(m.conn, m.projectID, nums, t.TaskNumber)
}

// activeTasksInDisplayOrder filters out soft-deleted tasks and groups
// the remaining set into the same Draft → Ready → Done shape the
// TaskList renders. Within each status group the input slice's order is
// preserved — m.tasks comes from the server sorted by canonical
// (position ASC, task_number ASC), which is what the user sees on
// screen.
func activeTasksInDisplayOrder(tasks []*pb.Task) []*pb.Task {
	var draft, ready, done []*pb.Task
	for _, t := range tasks {
		if t == nil || t.GetDeletedAt() != nil {
			continue
		}
		switch t.Status {
		case "draft":
			draft = append(draft, t)
		case "ready":
			ready = append(ready, t)
		case "done":
			done = append(done, t)
		}
	}
	out := make([]*pb.Task, 0, len(draft)+len(ready)+len(done))
	out = append(out, draft...)
	out = append(out, ready...)
	out = append(out, done...)
	return out
}

// reorderActiveTasks returns the active list with `idx` swapped against
// its same-status neighbour in `direction`. Returns (nil, false) when
// the swap is out of bounds or would cross a status section — both
// shapes need to be silent no-ops at the call site (no toast, no flash)
// so the caller can hold Shift+↑/↓ at a boundary without churn.
func reorderActiveTasks(active []*pb.Task, idx, direction int) ([]*pb.Task, bool) {
	if idx < 0 || idx >= len(active) {
		return nil, false
	}
	neighbour := idx + direction
	if neighbour < 0 || neighbour >= len(active) {
		return nil, false
	}
	if active[idx].Status != active[neighbour].Status {
		return nil, false
	}
	out := make([]*pb.Task, len(active))
	copy(out, active)
	out[idx], out[neighbour] = out[neighbour], out[idx]
	return out, true
}

// mergeActiveWithDeleted produces an m.tasks-shaped slice: the new
// active ordering followed by every soft-deleted entry from the
// original list in its original order. The deleted tail is irrelevant
// to active-mode rendering but keeps trash-mode round-trips lossless.
func mergeActiveWithDeleted(active []*pb.Task, original []*pb.Task) []*pb.Task {
	out := make([]*pb.Task, 0, len(original))
	out = append(out, active...)
	for _, t := range original {
		if t != nil && t.GetDeletedAt() != nil {
			out = append(out, t)
		}
	}
	return out
}

func indexOfTaskNumber(tasks []*pb.Task, num int32) int {
	for i, t := range tasks {
		if t != nil && t.TaskNumber == num {
			return i
		}
	}
	return -1
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
