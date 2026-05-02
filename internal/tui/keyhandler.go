package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	pb "github.com/watchfire-io/watchfire/proto"
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

	case key.Matches(msg, globalKeys.Export):
		// Open the v6.0 Ember export picker, scoped to the current
		// project. The picker opens with Markdown highlighted; Enter
		// triggers the daemon RPC and writes the file to the project
		// root. Esc cancels.
		if m.project == nil || m.exportPicker == nil {
			return nil
		}
		m.exportPicker.OpenForProject(m.projectID, m.project.Path)
		m.activeOverlay = overlayExport
		return nil

	case key.Matches(msg, globalKeys.FleetInsights):
		// Open the v6.0 Ember fleet rollup overlay. The overlay starts
		// in a "loading" state and dispatches the gRPC fetch so the
		// box pops up immediately.
		return m.openFleetInsightsOverlay("30d")

	case key.Matches(msg, globalKeys.Integrations):
		m.activeOverlay = overlayIntegrations
		if m.integrationsForm != nil {
			m.integrationsForm.Reset()
		}
		if m.conn != nil {
			// Fetch both outbound endpoints and v8.0 Echo inbound status
			// so a Tab into the Inbound tab finds the data already loaded.
			return tea.Batch(listIntegrationsCmd(m.conn), getInboundStatusCmd(m.conn))
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
		// On a completed task, `d` opens the inline diff viewer overlay
		// (v6.0 Ember). Otherwise it sets the task to done as usual.
		if t := m.taskList.SelectedTask(); t != nil && t.Status == "done" {
			return m.openTaskDiffOverlay()
		}
		return m.setSelectedTaskDone()
	case key.Matches(msg, taskListKeys.Delete):
		return m.confirmDeleteTask()
	case key.Matches(msg, taskListKeys.Insights):
		return m.openProjectInsightsOverlay("30d")
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

	if key.Matches(msg, logKeys.DeleteLog) {
		return m.confirmDeleteLog()
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
		case confirmDeleteLog:
			m.confirmMode = confirmNone
			logID := m.confirmLogID
			m.confirmLogID = ""
			if logID == "" || m.conn == nil {
				return nil
			}
			m.logViewer.MarkDeleting(logID)
			return deleteLogCmd(m.conn, m.projectID, logID)
		}
	case key.Matches(msg, confirmKeys.No), key.Matches(msg, confirmKeys.Cancel):
		m.confirmMode = confirmNone
		m.confirmLogID = ""
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

	case overlayExport:
		return m.handleExportPickerKey(msg)

	case overlayFleetInsights:
		return m.handleFleetInsightsKey(msg)

	case overlayProjectInsights:
		return m.handleProjectInsightsKey(msg)

	case overlayTaskDiff:
		return m.handleTaskDiffKey(msg)

	case overlayIntegrations:
		return m.handleIntegrationsKey(msg)
	}
	return nil
}

// handleIntegrationsKey handles input while the integrations overlay is
// open. List mode: a/e/d/t shortcuts (v7.0 outbound) or row navigation
// (v8.0 inbound, when the Inbound tab is active). Add mode: stacked
// form with Tab-to-advance / Esc-to-cancel. Confirm-delete mode: y/n.
func (m *Model) handleIntegrationsKey(msg tea.KeyMsg) tea.Cmd {
	f := m.integrationsForm
	if f == nil {
		m.activeOverlay = overlayNone
		return nil
	}

	switch f.Mode() {
	case integrationsModeAdd:
		return m.handleIntegrationsAddKey(msg)
	case integrationsModeConfirmDelete:
		return m.handleIntegrationsConfirmDeleteKey(msg)
	}

	if f.Tab() == integrationsTabInbound {
		return m.handleIntegrationsInboundKey(msg)
	}

	// Tab toggles between Outbound and Inbound when not editing —
	// applies to the outbound list mode only (the inbound handler
	// owns Tab in its own mode).
	if msg.Type == tea.KeyTab {
		f.SwitchTab()
		if m.conn != nil {
			return getInboundStatusCmd(m.conn)
		}
		return nil
	}

	switch msg.String() {
	case "esc", "q":
		m.activeOverlay = overlayNone
		f.Reset()
		return nil
	case "up", "k":
		f.MoveUp()
		return nil
	case "down", "j":
		f.MoveDown()
		return nil
	case "a":
		f.StartAdd()
		return nil
	case "e":
		row := f.CurrentRow()
		if row.Kind == integrationsRowGitHub {
			// Edit-as-add: pre-populate the add form with current
			// GitHub state. A future polish task can add a dedicated
			// edit step that updates the existing entry in place.
			f.StartAdd()
			f.addKind = integrationsRowGitHub
			f.addStep = addFieldEvents
			gh := f.cfg.GetGithub()
			if gh != nil {
				f.addEvents.TaskFailed = gh.GetEnabled()
				f.addEvents.RunComplete = gh.GetDraftDefault()
			}
		} else {
			f.StartAdd()
			f.addKind = row.Kind
		}
		return nil
	case "d":
		f.StartDeleteConfirm()
		return nil
	case "t":
		row := f.CurrentRow()
		if m.conn == nil {
			return nil
		}
		f.SetStatus("Sending test…")
		switch row.Kind {
		case integrationsRowWebhook:
			return testIntegrationCmd(m.conn, pb.IntegrationKind_WEBHOOK, row.ID)
		case integrationsRowSlack:
			return testIntegrationCmd(m.conn, pb.IntegrationKind_SLACK, row.ID)
		case integrationsRowDiscord:
			return testIntegrationCmd(m.conn, pb.IntegrationKind_DISCORD, row.ID)
		case integrationsRowGitHub:
			return testIntegrationCmd(m.conn, pb.IntegrationKind_GITHUB, "")
		}
	}
	return nil
}

func (m *Model) handleIntegrationsAddKey(msg tea.KeyMsg) tea.Cmd {
	f := m.integrationsForm
	switch msg.String() {
	case "esc":
		f.CancelAdd()
		return nil
	case "left", "h":
		f.CycleAddKind(-1)
		return nil
	case "right", "l":
		f.CycleAddKind(+1)
		return nil
	case "1":
		if f.addStep == addFieldEvents {
			f.ToggleAddEvent(0)
			return nil
		}
	case "2":
		if f.addStep == addFieldEvents {
			f.ToggleAddEvent(1)
			return nil
		}
	case "3":
		if f.addStep == addFieldEvents {
			f.ToggleAddEvent(2)
			return nil
		}
	}

	switch msg.Type {
	case tea.KeyTab, tea.KeyEnter:
		done := f.AdvanceAdd()
		if done {
			return m.dispatchIntegrationsAdd()
		}
		return nil
	}

	// While editing URL / label, forward the keystroke to the textinput.
	if f.addStep == addFieldURL || f.addStep == addFieldLabel {
		ti := f.InputModel()
		newTI, _ := ti.Update(msg)
		*ti = newTI
	}
	return nil
}

// handleIntegrationsInboundKey owns key dispatch while the v8.0 Echo
// Inbound tab is active. Selection mode: ↑↓ navigate, Enter edits or
// toggles, Tab returns to the outbound tab. Edit mode: Enter commits +
// dispatches saveInboundConfigCmd, Esc cancels.
func (m *Model) handleIntegrationsInboundKey(msg tea.KeyMsg) tea.Cmd {
	f := m.integrationsForm
	if f.IsInboundEditing() {
		switch msg.Type {
		case tea.KeyEnter:
			cfg := f.CommitInboundEdit()
			if cfg == nil || m.conn == nil {
				return nil
			}
			f.SetStatus("Saving inbound config…")
			return saveInboundConfigCmd(m.conn, cfg)
		case tea.KeyEsc:
			f.CancelInboundEdit()
			return nil
		}
		ti := f.InboundInputModel()
		newTI, _ := ti.Update(msg)
		*ti = newTI
		return nil
	}

	switch msg.String() {
	case "esc", "q":
		m.activeOverlay = overlayNone
		f.Reset()
		return nil
	case "up", "k":
		f.MoveInboundCursor(-1)
		return nil
	case "down", "j":
		f.MoveInboundCursor(+1)
		return nil
	case "enter":
		f.StartInboundEdit()
		// If the row was a toggle (Enabled), the draft mutated in place;
		// flush so the daemon picks up the change without an extra Enter.
		if !f.IsInboundEditing() && m.conn != nil {
			f.SetStatus("Saving inbound config…")
			return saveInboundConfigCmd(m.conn, f.FlushInboundDraft())
		}
		return nil
	}

	if msg.Type == tea.KeyTab {
		f.SwitchTab()
		return nil
	}
	return nil
}

func (m *Model) handleIntegrationsConfirmDeleteKey(msg tea.KeyMsg) tea.Cmd {
	f := m.integrationsForm
	switch msg.String() {
	case "y", "Y":
		row, ok := f.FinishDeleteConfirm(true)
		if !ok || m.conn == nil {
			return nil
		}
		var kind pb.IntegrationKind
		switch row.Kind {
		case integrationsRowWebhook:
			kind = pb.IntegrationKind_WEBHOOK
		case integrationsRowSlack:
			kind = pb.IntegrationKind_SLACK
		case integrationsRowDiscord:
			kind = pb.IntegrationKind_DISCORD
		case integrationsRowGitHub:
			kind = pb.IntegrationKind_GITHUB
		}
		return deleteIntegrationCmd(m.conn, kind, row.ID)
	case "n", "N", "esc":
		f.FinishDeleteConfirm(false)
	}
	return nil
}

func (m *Model) dispatchIntegrationsAdd() tea.Cmd {
	f := m.integrationsForm
	kind, url, label, events, mutes := f.AddSnapshot()
	f.CancelAdd()
	if m.conn == nil {
		return nil
	}
	switch kind {
	case integrationsRowWebhook:
		return saveIntegrationCmd(m.conn, &pb.WebhookIntegration{
			Label:          label,
			Url:            url,
			EnabledEvents:  events,
			ProjectMuteIds: mutes,
		})
	case integrationsRowSlack:
		return saveIntegrationCmd(m.conn, &pb.SlackIntegration{
			Label:          label,
			Url:            url,
			EnabledEvents:  events,
			ProjectMuteIds: mutes,
		})
	case integrationsRowDiscord:
		return saveIntegrationCmd(m.conn, &pb.DiscordIntegration{
			Label:          label,
			Url:            url,
			EnabledEvents:  events,
			ProjectMuteIds: mutes,
		})
	case integrationsRowGitHub:
		return saveIntegrationCmd(m.conn, &pb.GitHubIntegration{
			Enabled:       events.GetTaskFailed(),
			DraftDefault:  events.GetRunComplete(),
			ProjectScopes: mutes,
		})
	}
	return nil
}

// handleFleetInsightsKey handles input while the v6.0 Ember fleet rollup
// overlay is open. 1 / 3 / 9 / 0 cycle the window; Esc / q close.
func (m *Model) handleFleetInsightsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		m.activeOverlay = overlayNone
		return nil
	case "1":
		return m.openFleetInsightsOverlay("7d")
	case "3":
		return m.openFleetInsightsOverlay("30d")
	case "9":
		return m.openFleetInsightsOverlay("90d")
	case "0":
		return m.openFleetInsightsOverlay("all")
	}
	return nil
}

// openFleetInsightsOverlay flips the overlay on, resets the in-flight
// state, and dispatches the gRPC fetch.
func (m *Model) openFleetInsightsOverlay(window string) tea.Cmd {
	m.fleetInsightsWindow = window
	m.fleetInsights = FleetInsights{Window: window}
	m.activeOverlay = overlayFleetInsights
	if m.conn == nil {
		return nil
	}
	return loadFleetInsightsCmd(m.conn, window)
}

// handleProjectInsightsKey routes input while the v6.0 Ember per-project
// overlay is open. 1 / 3 / 9 / 0 cycle the window; Esc / q close.
func (m *Model) handleProjectInsightsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		m.activeOverlay = overlayNone
		return nil
	case "1":
		return m.openProjectInsightsOverlay("7d")
	case "3":
		return m.openProjectInsightsOverlay("30d")
	case "9":
		return m.openProjectInsightsOverlay("90d")
	case "0":
		return m.openProjectInsightsOverlay("all")
	}
	return nil
}

// openProjectInsightsOverlay flips the overlay on, resets the in-flight
// state, and dispatches the gRPC fetch for the current project.
func (m *Model) openProjectInsightsOverlay(window string) tea.Cmd {
	m.projectInsightsWindow = window
	m.projectInsights = ProjectInsights{Window: window}
	m.activeOverlay = overlayProjectInsights
	if m.conn == nil || m.projectID == "" {
		return nil
	}
	return loadProjectInsightsCmd(m.conn, m.projectID, window)
}

// handleExportPickerKey routes keys for the v6.0 Ember export overlay.
// ↑/↓ swap between Markdown / CSV; Enter triggers the export and prints
// a status-bar confirmation; Esc cancels. The picker stores the scope
// (project / single-task / global) — the same handler runs all three.
func (m *Model) handleExportPickerKey(msg tea.KeyMsg) tea.Cmd {
	if m.exportPicker == nil {
		m.activeOverlay = overlayNone
		return nil
	}
	switch msg.String() {
	case "esc":
		m.activeOverlay = overlayNone
		return nil
	case "up", "k":
		m.exportPicker.MoveUp()
		return nil
	case "down", "j":
		m.exportPicker.MoveDown()
		return nil
	case "enter":
		picker := *m.exportPicker
		m.activeOverlay = overlayNone
		return runExport(m.conn, picker)
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
			res := g.FinishEdit()
			if res.Err != "" {
				m.err = fmt.Errorf("%s", res.Err)
				return nil
			}
			switch res.Kind {
			case EditAgentPath:
				if m.conn != nil {
					return updateGlobalSettingsCmd(m.conn, nil, map[string]string{res.AgentName: res.Path}, nil, nil)
				}
			case EditNotify:
				if m.conn != nil {
					return updateGlobalSettingsCmd(m.conn, nil, nil, g.NotificationsProto(), nil)
				}
			case EditTerminalShell:
				if m.conn != nil {
					ts := res.TerminalShell
					return updateGlobalSettingsCmd(m.conn, nil, nil, nil, &ts)
				}
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
	case " ":
		if g.ToggleNotify() && m.conn != nil {
			return updateGlobalSettingsCmd(m.conn, nil, nil, g.NotificationsProto(), nil)
		}
		return nil
	case "enter":
		if g.StartEdit() {
			return nil
		}
		// Notify toggles also accept Enter for consistency.
		if g.ToggleNotify() && m.conn != nil {
			return updateGlobalSettingsCmd(m.conn, nil, nil, g.NotificationsProto(), nil)
		}
		// Default selector: cycle.
		changed, val := g.CycleDefault()
		if changed && m.conn != nil {
			v := val
			return updateGlobalSettingsCmd(m.conn, &v, nil, nil, nil)
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

	// Agent cycler: space/enter/right cycles forward, left cycles back.
	if m.taskForm.FocusIndex() == taskFormFocusAgent {
		switch msg.Type {
		case tea.KeySpace, tea.KeyEnter, tea.KeyRight:
			m.taskForm.CycleAgentNext()
		case tea.KeyLeft:
			m.taskForm.CycleAgentPrev()
		}
		return nil
	}

	// Status field: toggle on space/enter
	if m.taskForm.FocusIndex() == taskFormFocusStatus {
		if msg.Type == tea.KeySpace || msg.Type == tea.KeyEnter {
			m.taskForm.ToggleStatus()
		}
		return nil
	}

	// Forward to active input
	switch m.taskForm.FocusIndex() {
	case taskFormFocusTitle:
		ti := m.taskForm.TitleInput()
		newTI, _ := ti.Update(msg)
		*ti = newTI
	case taskFormFocusPrompt:
		ta := m.taskForm.PromptArea()
		newTA, _ := ta.Update(msg)
		*ta = newTA
	case taskFormFocusCriteria:
		ta := m.taskForm.CriteriaArea()
		newTA, _ := ta.Update(msg)
		*ta = newTA
	}

	return nil
}
