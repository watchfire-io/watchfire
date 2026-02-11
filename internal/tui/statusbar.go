package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// confirmMode values.
const (
	confirmNone   = 0
	confirmDelete = 1
	confirmQuit   = 2
	confirmStop   = 3
)

func renderStatusBar(m *Model, width int) string {
	// Handle confirm mode
	if m.confirmMode == confirmDelete {
		return renderConfirmBar(
			fmt.Sprintf("Delete task #%04d? (y/n)", m.confirmTaskNum),
			width,
		)
	}
	if m.confirmMode == confirmQuit {
		return renderConfirmBar(
			"Agent running. Quit? (y/n)",
			width,
		)
	}
	if m.confirmMode == confirmStop {
		return renderConfirmBar(
			"Stop agent? (y/n)",
			width,
		)
	}

	// Error display
	if m.err != nil {
		return renderErrorBar(m.err.Error(), width)
	}

	// Saved indicator
	if m.showSaved {
		return renderSavedBar(width)
	}

	// Context-sensitive key hints
	hints := getKeyHints(m)
	left := " " + hints

	// Connection status
	right := ""
	if m.connected {
		right = lipgloss.NewStyle().Foreground(colorGreen).Render("Connected") + " "
	} else {
		right = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("⚠ Disconnected") + " "
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return statusBarStyle.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}

func getKeyHints(m *Model) string {
	if m.activeOverlay != overlayNone {
		return keyHint("Ctrl+s", "save") + "  " + keyHint("Esc", "cancel")
	}

	base := keyHint("Ctrl+q", "quit") + "  " + keyHint("Ctrl+h", "help") + "  " + keyHint("Tab", "switch")

	if m.focusedPanel == 0 {
		switch m.leftTab {
		case 0: // Tasks
			agentRunning := m.agentStatus != nil && m.agentStatus.IsRunning
			hints := base + "  " + keyHint("a", "add") + "  " + keyHint("e", "edit") + "  " +
				keyHint("s", "start") + "  " + keyHint("w", "wildfire") + "  " +
				keyHint("!", "start all")
			if agentRunning {
				hints += "  " + keyHint("S", "stop")
			}
			hints += "  " + keyHint("r", "ready") + "  " + keyHint("d", "done") + "  " +
				keyHint("x", "delete")
			return hints
		case 1: // Definition
			return base + "  " + keyHint("e", "edit in $EDITOR")
		case 2: // Settings
			return base + "  " + keyHint("j/k", "navigate") + "  " +
				keyHint("Enter", "edit") + "  " + keyHint("Space", "toggle")
		}
	} else {
		switch m.rightTab {
		case 0: // Chat/Terminal
			return base + "  " + keyHint("", "(input goes to agent)")
		case 1: // Logs
			return base + "  " + keyHint("Enter", "view") + "  " + keyHint("Esc", "back")
		}
	}

	return base
}

func keyHint(k, desc string) string {
	if k == "" {
		return hintStyle.Render(desc)
	}
	return keyStyle.Render(k) + " " + hintStyle.Render(desc)
}

func renderConfirmBar(msg string, width int) string {
	return statusBarStyle.
		Background(colorYellow).
		Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}).
		Width(width).
		Render(" " + msg)
}

func renderErrorBar(msg string, width int) string {
	return statusBarStyle.
		Background(colorRed).
		Width(width).
		Render(" " + msg)
}

func renderSavedBar(width int) string {
	return statusBarStyle.
		Width(width).
		Render(" " + lipgloss.NewStyle().Foreground(colorGreen).Render("Saved"))
}
