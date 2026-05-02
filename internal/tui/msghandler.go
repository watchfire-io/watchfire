package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleMessages processes non-key, non-mouse messages in the Update loop.
// Returns the updated model and commands, and a bool indicating if the message was handled.
func (m *Model) handleMessage(msg tea.Msg) (bool, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Daemon connection ──────────────────────────────────────────
	case DaemonConnectedMsg:
		m.conn = msg.Conn
		m.connected = true
		m.reconnectAttempts = 0
		m.streamCancel()
		m.streamCtx, m.streamCancel = context.WithCancel(context.Background())
		cmds = append(cmds,
			loadProjectCmd(m.conn, m.projectID),
			loadTasksCmd(m.conn, m.projectID),
			getAgentStatusCmd(m.conn, m.projectID),
			checkDaemonUpdateCmd(m.conn),
			fetchGitInfoCmd(m.conn, m.projectID),
		)
		return true, tea.Batch(cmds...)

	case UpdateAvailableMsg:
		m.updateVersion = msg.Version
		return true, nil

	case DaemonDisconnectedMsg:
		m.connected = false
		m.subscribed = false
		m.agentPolling = false
		m.spinnerRunning = false
		m.agentStatus = nil
		m.reconnectAttempts++
		if m.conn != nil {
			_ = m.conn.Close()
			m.conn = nil
		}
		return true, reconnectTick()

	case ReconnectMsg:
		if !m.connected {
			cmds = append(cmds, connectDaemonCmd())
		}
		return true, tea.Batch(cmds...)

	// ── Project data ───────────────────────────────────────────────
	case GitInfoMsg:
		m.gitInfo = msg.Info
		return true, nil

	case ProjectLoadedMsg:
		m.project = msg.Project
		m.definitionView.SetContent(msg.Project.Definition)
		m.settingsForm.LoadFromProject(msg.Project)
		m.taskList.SetProjectDefaultAgent(msg.Project.DefaultAgent)
		return true, nil

	case ProjectSavedMsg:
		m.project = msg.Project
		m.definitionView.SetContent(msg.Project.Definition)
		m.settingsForm.LoadFromProject(msg.Project)
		m.taskList.SetProjectDefaultAgent(msg.Project.DefaultAgent)
		m.showSaved = true
		cmds = append(cmds, clearSavedAfter(clearSavedTimeout))
		return true, tea.Batch(cmds...)

	// ── Task data ──────────────────────────────────────────────────
	case TasksLoadedMsg:
		m.tasks = msg.Tasks
		m.taskList.SetTasks(msg.Tasks)
		m.taskList.SetAgentStatus(m.agentStatus)
		return true, nil

	case TaskSavedMsg:
		m.activeOverlay = overlayNone
		m.taskForm = nil
		cmds = append(cmds, loadTasksCmd(m.conn, m.projectID))
		return true, tea.Batch(cmds...)

	case TaskDeletedMsg:
		m.confirmMode = confirmNone
		cmds = append(cmds, loadTasksCmd(m.conn, m.projectID))
		return true, tea.Batch(cmds...)

	// ── Agent status ───────────────────────────────────────────────
	case AgentStatusMsg:
		return true, m.handleAgentStatus(msg)

	case AgentStartedMsg:
		m.agentStatus = msg.Status
		m.terminal.SetAgentStatus(msg.Status)
		m.terminal.Clear()
		m.taskList.SetAgentStatus(msg.Status)
		m.subscribed = false
		cmds = append(cmds, getAgentStatusCmd(m.conn, m.projectID))
		return true, tea.Batch(cmds...)

	case AgentStoppedMsg:
		m.agentStatus = nil
		m.subscribed = false
		m.agentPolling = false
		m.spinnerRunning = false
		m.autoStartDone = false // allow chat to auto-start again after wildfire/task ends
		m.terminal.SetAgentStatus(nil)
		m.taskList.SetAgentStatus(nil)
		cmds = append(cmds, loadTasksCmd(m.conn, m.projectID))
		return true, tea.Batch(cmds...)

	// ── Terminal output ────────────────────────────────────────────
	case ScreenUpdateMsg:
		m.terminal.SetContent(msg.AnsiContent)
		return true, nil

	case ScreenEndedMsg:
		m.subscribed = false
		cmds = append(cmds, getAgentStatusCmd(m.conn, m.projectID))
		return true, tea.Batch(cmds...)

	// ── Agent issues ───────────────────────────────────────────────
	case AgentIssueMsg:
		if msg.Issue.IssueType == "" {
			m.currentIssue = nil
			m.terminal.SetIssue(nil)
		} else {
			m.currentIssue = msg.Issue
			m.terminal.SetIssue(msg.Issue)
		}
		return true, nil

	// ── Spinner tick ──────────────────────────────────────────────
	case spinnerTickMsg:
		if m.agentStatus != nil && m.agentStatus.IsRunning {
			m.taskList.Tick()
			cmds = append(cmds, spinnerTick())
		} else {
			m.spinnerRunning = false
		}
		return true, tea.Batch(cmds...)

	// ── Polling tick ───────────────────────────────────────────────
	case TickMsg:
		if m.connected && m.agentPolling {
			cmds = append(cmds,
				getAgentStatusCmd(m.conn, m.projectID),
				loadTasksCmd(m.conn, m.projectID),
				pollAgentStatusTick(),
			)
		}
		return true, tea.Batch(cmds...)

	// ── Error handling ─────────────────────────────────────────────
	case ErrorMsg:
		if !m.connected && m.reconnectAttempts > 0 {
			m.reconnectAttempts++
			return true, reconnectTick()
		}
		m.err = msg.Err
		cmds = append(cmds, clearErrorAfter(5*time.Second))
		return true, tea.Batch(cmds...)

	case ClearErrorMsg:
		m.err = nil
		return true, nil

	case ClearSavedMsg:
		m.showSaved = false
		m.statusMessage = ""
		return true, nil

	// ── Export (v6.0 Ember) ───────────────────────────────────────
	case ExportCompletedMsg:
		m.statusMessage = "Exported " + msg.Filename
		m.showSaved = true
		cmds = append(cmds, clearSavedAfter(clearSavedTimeout))
		return true, tea.Batch(cmds...)

	case ExportFailedMsg:
		m.err = fmt.Errorf("export failed: %w", msg.Err)
		cmds = append(cmds, clearErrorAfter(5*time.Second))
		return true, tea.Batch(cmds...)

	// ── Fleet insights overlay (v6.0 Ember) ───────────────────────
	case FleetInsightsLoadedMsg:
		// Drop late responses for stale windows — the user may have
		// already cycled to a different preset before the RPC returned.
		if msg.Insights.Window == m.fleetInsightsWindow {
			m.fleetInsights = msg.Insights
		}
		return true, nil

	// ── Log viewer ────────────────────────────────────────────────
	case LogsLoadedMsg:
		m.logViewer.SetLogs(msg.Logs)
		return true, nil

	case LogContentMsg:
		m.logViewer.SetLogContent(msg.Entry, msg.Content)
		return true, nil

	case LogDeletedMsg:
		m.logViewer.ClearDeleting()
		if msg.Err != nil {
			m.err = fmt.Errorf("failed to delete log: %w", msg.Err)
			cmds = append(cmds, clearErrorAfter(5*time.Second))
			return true, tea.Batch(cmds...)
		}
		m.logViewer.RemoveLog(msg.LogID)
		return true, nil

	// ── Global settings ────────────────────────────────────────────
	case SettingsLoadedMsg:
		m.globalSettingsForm.Load(msg.Settings)
		return true, nil

	case SettingsSavedMsg:
		m.globalSettingsForm.Load(msg.Settings)
		m.showSaved = true
		cmds = append(cmds, clearSavedAfter(clearSavedTimeout))
		return true, tea.Batch(cmds...)

	// ── Editor finished ────────────────────────────────────────────
	case EditorFinishedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			cmds = append(cmds, clearErrorAfter(5*time.Second))
		} else if m.project != nil && msg.Content != m.project.Definition {
			cmds = append(cmds, updateProjectCmd(m.conn, m.projectID, map[string]interface{}{
				"definition": msg.Content,
			}))
		}
		return true, tea.Batch(cmds...)
	}

	return false, nil
}

// handleAgentStatus processes agent status messages.
func (m *Model) handleAgentStatus(msg AgentStatusMsg) tea.Cmd {
	var cmds []tea.Cmd

	m.agentStatus = msg.Status
	m.taskList.SetAgentStatus(msg.Status)
	if msg.Status != nil {
		m.terminal.SetAgentStatus(msg.Status)
		if msg.Status.Issue != nil && msg.Status.Issue.IssueType != "" {
			m.currentIssue = msg.Status.Issue
			m.terminal.SetIssue(msg.Status.Issue)
		}
	}

	// Subscribe to streams if agent running and not yet subscribed
	if msg.Status != nil && msg.Status.IsRunning && !m.subscribed && m.program != nil {
		m.subscribed = true
		rows, cols := m.ptyDimensions()
		cmds = append(cmds,
			subscribeScreenCmd(m.streamCtx, m.conn, m.projectID, rows, cols, m.program),
			subscribeAgentIssuesCmd(m.streamCtx, m.conn, m.projectID, m.program),
		)
	}

	// Auto-start chat if no agent running and we haven't done it yet
	if msg.Status != nil && !msg.Status.IsRunning && !m.autoStartDone && m.connected {
		m.autoStartDone = true
		rows, cols := m.ptyDimensions()
		cmds = append(cmds, startAgentCmd(m.conn, m.projectID, "chat", 0, rows, cols))
	}

	// Start polling if agent running
	if msg.Status != nil && msg.Status.IsRunning && !m.agentPolling {
		m.agentPolling = true
		cmds = append(cmds, pollAgentStatusTick())
	}

	// Start spinner if agent running
	if msg.Status != nil && msg.Status.IsRunning && !m.spinnerRunning {
		m.spinnerRunning = true
		cmds = append(cmds, spinnerTick())
	}

	return tea.Batch(cmds...)
}
