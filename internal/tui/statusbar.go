package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// confirmMode values.
const (
	confirmNone                   = 0
	confirmDelete                 = 1
	confirmQuit                   = 2
	confirmStop                   = 3
	confirmDeleteLog              = 4
	confirmPermanentDelete        = 5
	confirmSettingsArchive        = 6
	confirmSettingsRegenID        = 7
	confirmSettingsResetNumbering = 8
	confirmSettingsPruneBranches  = 9
	confirmSettingsUnregister     = 10
)

func renderStatusBar(m *Model, width int) string {
	// Text-select mode supersedes every other status-bar surface — when
	// the program has dropped mouse capture, the loud banner is the
	// user's only reminder of how to get back into interactive mode.
	if m.textSelectMode {
		return renderTextSelectBar(width)
	}

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
	if m.confirmMode == confirmDeleteLog {
		return renderConfirmBar(
			"Delete this session log? (y/n)",
			width,
		)
	}
	if m.confirmMode == confirmPermanentDelete {
		return renderConfirmBar(
			fmt.Sprintf("Permanently delete task #%04d? (y/N)", m.confirmTaskNum),
			width,
		)
	}
	switch m.confirmMode {
	case confirmSettingsArchive:
		label := "Archive project? Daemon will stop auto-starting tasks. (y/N)"
		if m.project != nil && m.project.Status == "archived" {
			label = "Unarchive project? (y/N)"
		}
		return renderConfirmBar(label, width)
	case confirmSettingsRegenID:
		return renderConfirmBar("Regenerate project ID? Existing references stay valid. (y/N)", width)
	case confirmSettingsResetNumbering:
		return renderConfirmBar("Reset next_task_number to highest+1? (y/N)", width)
	case confirmSettingsPruneBranches:
		return renderConfirmBar("Prune merged-orphan watchfire/* branches? (y/N)", width)
	case confirmSettingsUnregister:
		return renderConfirmBar("Unregister project from global index? Local files stay. (y/N)", width)
	}

	// Trash mode banner — supersedes the normal hint line so the user
	// always sees they're operating on the deleted set, not the active
	// list. Count is the deleted population (the rendered list may be
	// shorter under a stale snapshot).
	if m.focusedPanel == 0 && m.leftTab == 0 && m.taskList != nil && m.taskList.TrashMode() {
		n := m.taskList.DeletedCount()
		return renderTrashBar(
			fmt.Sprintf("Trash mode — %d deleted task(s) · u restore · x delete · D back", n),
			width,
		)
	}

	// Error display
	if m.err != nil {
		return renderErrorBar(m.err.Error(), width)
	}

	// Saved indicator (or status message — exports use this for the
	// "Exported watchfire-project-foo-2026-05-02.md" confirmation).
	if m.showSaved {
		if m.statusMessage != "" {
			return renderStatusMessageBar(m.statusMessage, width)
		}
		return renderSavedBar(width)
	}

	// Context-sensitive key hints
	hints := getKeyHints(m)
	left := " " + hints

	// Update notice + connection status
	right := ""
	// Malformed task files sitting on disk — surface a persistent warning so a
	// broken task file is visible instead of silently vanishing from the list.
	if n := len(m.malformedTasks); n > 0 {
		right += lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(
			fmt.Sprintf("⚠ %d task file(s) failed to load", n),
		) + "  "
	}
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
			context = []hint{{"Enter", "view"}, {"d", "delete"}, {"Esc", "back"}}
		}
	}

	allHints := make([]hint, 0, len(base)+len(context))
	allHints = append(allHints, base...)
	allHints = append(allHints, context...)
	return renderHintsProgressive(allHints, m.width)
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

// renderStatusMessageBar renders an arbitrary one-line success message
// (the v6.0 Ember export confirmation lands here). Same green check as
// the Saved bar so the visual language stays consistent.
func renderStatusMessageBar(msg string, width int) string {
	return statusBarStyle.
		Width(width).
		Render(" " + lipgloss.NewStyle().Foreground(colorGreen).Render("✓ "+msg))
}

// renderTrashBar paints the trash-mode banner across the status bar so the
// user always sees they're operating on the deleted set rather than the
// live task list.
func renderTrashBar(msg string, width int) string {
	return statusBarStyle.
		Background(colorRed).
		Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}).
		Bold(true).
		Width(width).
		Render(" " + msg)
}

// renderTextSelectBar paints a high-contrast banner across the full
// status bar while the program is in text-select mode. The cyan
// background flips the bar's normal palette so the banner reads at a
// glance — the user needs to know how to get mouse capture back.
func renderTextSelectBar(width int) string {
	return lipgloss.NewStyle().
		Background(colorCyan).
		Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}).
		Bold(true).
		Width(width).
		Render(" ▎TEXT SELECT — drag to select · Ctrl+T to resume mouse")
}
