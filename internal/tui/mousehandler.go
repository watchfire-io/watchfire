package tui

import tea "github.com/charmbracelet/bubbletea"

// wheelTarget identifies which scrollable component a wheel event should
// drive. Resolved purely from cursor X + tab state — independent of
// m.focusedPanel — so hover-and-scroll follows the cursor, matching
// macOS Terminal.app, iTerm2, and every browser.
type wheelTarget int

const (
	wheelTargetNone wheelTarget = iota
	wheelTargetTaskList
	wheelTargetDefinition
	wheelTargetSettings
	wheelTargetTerminal
	wheelTargetLogs
)

// isWheelMsg returns true if the mouse button on the event is one of
// the four wheel directions. tea.MouseMsg is a defined type over
// MouseEvent and doesn't inherit MouseEvent.IsWheel as a method, so we
// duplicate the check here against the same four constants.
func isWheelMsg(msg tea.MouseMsg) bool {
	return msg.Button == tea.MouseButtonWheelUp ||
		msg.Button == tea.MouseButtonWheelDown ||
		msg.Button == tea.MouseButtonWheelLeft ||
		msg.Button == tea.MouseButtonWheelRight
}

// resolveWheelTarget picks the scrollable component under the cursor.
// Pure function — exposed for unit testing.
func resolveWheelTarget(x, dividerCol, leftTab, rightTab int) wheelTarget {
	if x < dividerCol {
		switch leftTab {
		case 0:
			return wheelTargetTaskList
		case 1:
			return wheelTargetDefinition
		case 2:
			return wheelTargetSettings
		}
		return wheelTargetNone
	}
	switch rightTab {
	case 0:
		return wheelTargetTerminal
	case 1:
		return wheelTargetLogs
	}
	return wheelTargetNone
}

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	layout := computeLayout(m.width, m.height, m.splitRatio)

	// Wheel events scroll the panel under the cursor without changing
	// focus or engaging divider drag. Routed first so the click-press
	// branch below never sees them — otherwise wheel would steal focus
	// every tick while hovering.
	if isWheelMsg(msg) && msg.Action == tea.MouseActionPress {
		return m.dispatchWheel(msg, layout)
	}

	switch msg.Action {
	case tea.MouseActionPress:
		x := msg.X

		// Check if clicking on divider
		if x >= layout.dividerCol-1 && x <= layout.dividerCol+1 {
			m.dragging = true
			return nil
		}

		// Click on left panel
		if x < layout.dividerCol {
			m.focusedPanel = 0
		} else {
			m.focusedPanel = 1
		}

		// Check if clicking on header (y == 0) for tab switching
		if msg.Y == 0 {
			return m.handleHeaderClick(msg.X)
		}

	case tea.MouseActionRelease:
		m.dragging = false

	case tea.MouseActionMotion:
		if m.dragging {
			ratio := float64(msg.X) / float64(m.width)
			if ratio < 0.2 {
				ratio = 0.2
			}
			if ratio > 0.8 {
				ratio = 0.8
			}
			m.splitRatio = ratio
			m.updateDimensions()
		}
	}

	return nil
}

// dispatchWheel maps a wheel event to the scrollable component under
// the cursor. Does not mutate focusedPanel.
func (m *Model) dispatchWheel(msg tea.MouseMsg, layout panelLayout) tea.Cmd {
	target := resolveWheelTarget(msg.X, layout.dividerCol, m.leftTab, m.rightTab)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		switch target {
		case wheelTargetTaskList:
			m.taskList.MoveUp()
		case wheelTargetDefinition:
			m.definitionView.ScrollUp()
		case wheelTargetSettings:
			m.settingsForm.MoveUp()
		case wheelTargetTerminal:
			m.terminal.ScrollUp(3)
		case wheelTargetLogs:
			m.logViewer.MoveUp()
		}
	case tea.MouseButtonWheelDown:
		switch target {
		case wheelTargetTaskList:
			m.taskList.MoveDown()
		case wheelTargetDefinition:
			m.definitionView.ScrollDown()
		case wheelTargetSettings:
			m.settingsForm.MoveDown()
		case wheelTargetTerminal:
			m.terminal.ScrollDown(3)
		case wheelTargetLogs:
			m.logViewer.MoveDown()
		}
	}
	return nil
}

func (m *Model) handleHeaderClick(x int) tea.Cmd {
	layout := computeLayout(m.width, m.height, m.splitRatio)
	if x < layout.dividerCol {
		offset := 15
		tabWidth := 12
		tabIdx := (x - offset) / tabWidth
		if tabIdx >= 0 && tabIdx <= 2 {
			m.leftTab = tabIdx
			m.focusedPanel = 0
		}
	} else {
		rightStart := layout.dividerCol
		rightOffset := (x - rightStart)
		if rightOffset < 15 {
			m.rightTab = 0
		} else {
			m.rightTab = 1
			return m.loadLogsIfNeeded()
		}
		m.focusedPanel = 1
	}
	return nil
}

// loadLogsIfNeeded fetches logs from daemon when switching to the Logs tab.
func (m *Model) loadLogsIfNeeded() tea.Cmd {
	if m.conn == nil {
		return nil
	}
	return listLogsCmd(m.conn, m.projectID)
}
