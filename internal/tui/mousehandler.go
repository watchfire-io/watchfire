package tui

import tea "github.com/charmbracelet/bubbletea"

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Action {
	case tea.MouseActionPress:
		layout := computeLayout(m.width, m.height, m.splitRatio)
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

	// Scroll in focused panel
	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.focusedPanel == 0 {
				switch m.leftTab {
				case 0:
					m.taskList.MoveUp()
				case 1:
					m.definitionView.ScrollUp()
				case 2:
					m.settingsForm.MoveUp()
				}
			} else {
				switch m.rightTab {
				case 0:
					m.terminal.ScrollUp(3)
				case 1:
					m.logViewer.MoveUp()
				}
			}
		case tea.MouseButtonWheelDown:
			if m.focusedPanel == 0 {
				switch m.leftTab {
				case 0:
					m.taskList.MoveDown()
				case 1:
					m.definitionView.ScrollDown()
				case 2:
					m.settingsForm.MoveDown()
				}
			} else {
				switch m.rightTab {
				case 0:
					m.terminal.ScrollDown(3)
				case 1:
					m.logViewer.MoveDown()
				}
			}
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
