package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// handleKey processes key events.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Disconnected state: only allow quit and retry
	if !m.connected && m.reconnectAttempts > 0 {
		switch msg.String() {
		case "q", "ctrl+q", "ctrl+c":
			return m.doQuit()
		case "r":
			m.reconnectAttempts = 1
			return connectDaemonCmd()
		}
		return nil
	}

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

	case key.Matches(msg, globalKeys.GlobalSettings):
		m.activeOverlay = overlayGlobalSettings
		m.globalSettingsForm.Reset()
		if m.conn != nil {
			return getSettingsCmd(m.conn)
		}
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
	// Search mode: capture input
	if m.taskList.searchMode {
		switch msg.Type {
		case tea.KeyEscape:
			m.taskList.StopSearch()
			return nil
		case tea.KeyEnter:
			m.taskList.ConfirmSearch()
			return nil
		case tea.KeyBackspace:
			q := m.taskList.searchQuery
			if q != "" {
				m.taskList.UpdateSearch(q[:len(q)-1])
			}
			return nil
		case tea.KeyRunes:
			m.taskList.UpdateSearch(m.taskList.searchQuery + string(msg.Runes))
			return nil
		case tea.KeyUp:
			m.taskList.MoveUp()
			return nil
		case tea.KeyDown:
			m.taskList.MoveDown()
			return nil
		}
		return nil
	}

	agentRunning := m.agentStatus != nil && m.agentStatus.IsRunning

	// "/" to start search
	if msg.Type == tea.KeyRunes && string(msg.Runes) == "/" {
		m.taskList.StartSearch()
		return nil
	}

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

	case overlayGlobalSettings:
		return m.handleGlobalSettingsKey(msg)
	}
	return nil
}

func (m *Model) handleGlobalSettingsKey(msg tea.KeyMsg) tea.Cmd {
	g := m.globalSettingsForm
	if g == nil {
		m.activeOverlay = overlayNone
		return nil
	}

	if g.IsEditing() {
		switch msg.Type {
		case tea.KeyEnter:
			changed, agent, path := g.FinishEdit()
			if changed && m.conn != nil {
				return updateGlobalSettingsCmd(m.conn, nil, map[string]string{agent: path})
			}
			return nil
		case tea.KeyEscape:
			g.CancelEdit()
			return nil
		default:
			ti := g.InputModel()
			newTI, _ := ti.Update(msg)
			*ti = newTI
			return nil
		}
	}

	switch msg.String() {
	case "esc":
		m.activeOverlay = overlayNone
		g.Reset()
		return nil
	case "up", "k":
		g.MoveUp()
		return nil
	case "down", "j":
		g.MoveDown()
		return nil
	case "enter":
		if g.StartEdit() {
			return nil
		}
		// Default selector: cycle.
		changed, val := g.CycleDefault()
		if changed && m.conn != nil {
			v := val
			return updateGlobalSettingsCmd(m.conn, &v, nil)
		}
		return nil
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
