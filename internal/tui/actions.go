package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// doQuit performs clean shutdown: cancel streams, clear program ref, close connection, quit.
func (m *Model) doQuit() tea.Cmd {
	m.streamCancel()
	m.program.Clear()
	if m.conn != nil {
		_ = m.conn.Close()
	}
	return tea.Quit
}
