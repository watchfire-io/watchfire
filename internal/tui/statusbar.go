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

	// Update notice + connection status
	right := ""
	if m.updateVersion != "" {
		right += lipgloss.NewStyle().Foreground(colorYellow).Render(
			fmt.Sprintf("⬆ v%s available", m.updateVersion),
		) + "  "
	}
	if m.connected {
		right += lipgloss.NewStyle().Foreground(colorGreen).Render("● Connected") + " "
	} else {
		right += lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("⚠ Disconnected") + " "
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return statusBarStyle.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}

type hint struct {
	key  string
	desc string
}

func getKeyHints(m *Model) string {
	if m.activeOverlay != overlayNone {
		return keyHint("Ctrl+s", "save") + "  " + keyHint("Esc", "cancel")
	}

	// Task search mode
	if m.focusedPanel == 0 && m.leftTab == 0 && m.taskList.searchMode {
		searchDisplay := "/" + m.taskList.searchQuery
		return keyHint("", searchDisplay) + "  " + keyHint("Esc", "clear") + "  " + keyHint("Enter", "confirm")
	}

	base := []hint{
		{"Ctrl+q", "quit"},
		{"Ctrl+h", "help"},
		{"Tab", "switch"},
	}

	var context []hint

	if m.focusedPanel == 0 {
		switch m.leftTab {
		case 0: // Tasks
			agentRunning := m.agentStatus != nil && m.agentStatus.IsRunning
			context = []hint{
				{"/", "search"},
				{"a", "add"},
				{"e", "edit"},
				{"s", "start"},
				{"w", "wildfire"},
				{"!", "start all"},
			}
			if agentRunning {
				context = append(context, hint{"S", "stop"})
			}
			context = append(context,
				hint{"r", "ready"},
				hint{"d", "done"},
				hint{"x", "delete"},
			)
		case 1: // Definition
			context = []hint{{"e", "edit in $EDITOR"}}
		case 2: // Settings
			context = []hint{
				{"j/k", "navigate"},
				{"Enter", "edit"},
				{"Space", "toggle"},
			}
		}
	} else {
		switch m.rightTab {
		case 0: // Chat/Terminal
			context = []hint{{"", "(input goes to agent)"}}
		case 1: // Logs
			context = []hint{{"Enter", "view"}, {"Esc", "back"}}
		}
	}

	hints := append(base, context...)
	return renderHintsProgressive(hints, m.width)
}

// renderHintsProgressive renders hints, progressively truncating for narrow terminals.
func renderHintsProgressive(hints []hint, width int) string {
	// Reserve space for right section (~25 chars for "Connected" + update notice)
	available := width - 30

	// Level 0: Full hints
	full := renderHintSlice(hints)
	if lipgloss.Width(full) <= available {
		return full
	}

	// Level 1 (<120 cols): Abbreviate key names
	abbreviated := make([]hint, len(hints))
	for i, h := range hints {
		abbreviated[i] = hint{abbreviateKey(h.key), h.desc}
	}
	abbr := renderHintSlice(abbreviated)
	if lipgloss.Width(abbr) <= available {
		return abbr
	}

	// Level 2: Abbreviated with descriptions, truncated from the right
	var result string
	for _, h := range abbreviated {
		part := keyHint(h.key, h.desc)
		candidate := result
		if candidate != "" {
			candidate += "  "
		}
		candidate += part
		if lipgloss.Width(candidate) > available {
			break
		}
		result = candidate
	}
	return result
}

func renderHintSlice(hints []hint) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, keyHint(h.key, h.desc))
	}
	return strings.Join(parts, "  ")
}

func abbreviateKey(k string) string {
	switch k {
	case "Ctrl+q":
		return "^q"
	case "Ctrl+h":
		return "^h"
	case "Ctrl+s":
		return "^s"
	case "Tab":
		return "⇥"
	case "Enter":
		return "↵"
	case "Space":
		return "␣"
	case "Esc":
		return "⎋"
	default:
		return k
	}
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
		Render(" ✗ " + msg)
}

func renderSavedBar(width int) string {
	return statusBarStyle.
		Width(width).
		Render(" " + lipgloss.NewStyle().Foreground(colorGreen).Render("✓ Saved"))
}
