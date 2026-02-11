package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type helpSection struct {
	title string
	keys  []helpKey
}

type helpKey struct {
	key  string
	desc string
}

var helpSections = []helpSection{
	{
		title: "Global",
		keys: []helpKey{
			{"Ctrl+q", "Quit"},
			{"Ctrl+h", "Toggle help"},
			{"Tab", "Switch panel focus"},
			{"1/2/3", "Switch left panel tab"},
		},
	},
	{
		title: "Task List",
		keys: []helpKey{
			{"j/k ↑/↓", "Navigate tasks"},
			{"a", "Add new task"},
			{"e / Enter", "Edit task"},
			{"s", "Start agent on task"},
			{"S", "Stop running agent"},
			{"w", "Start wildfire mode"},
			{"!", "Start all ready tasks"},
			{"r", "Set task to Ready"},
			{"t", "Set task to Draft"},
			{"d", "Mark task Done"},
			{"x", "Delete task (soft)"},
		},
	},
	{
		title: "Terminal",
		keys: []helpKey{
			{"(type)", "Input goes to agent"},
			{"PgUp/PgDn", "Scroll terminal"},
			{"R", "Resume agent (on issue)"},
		},
	},
	{
		title: "Logs",
		keys: []helpKey{
			{"j/k ↑/↓", "Navigate logs"},
			{"Enter", "View log content"},
			{"Esc", "Back to list"},
			{"PgUp/PgDn", "Scroll content"},
		},
	},
	{
		title: "Definition",
		keys: []helpKey{
			{"e / Enter", "Edit in $EDITOR"},
			{"j/k", "Scroll"},
		},
	},
	{
		title: "Settings",
		keys: []helpKey{
			{"j/k", "Navigate fields"},
			{"Enter", "Edit text field"},
			{"Space", "Toggle boolean"},
		},
	},
	{
		title: "Overlays",
		keys: []helpKey{
			{"Ctrl+s", "Save"},
			{"Esc", "Cancel / Close"},
			{"Tab", "Next field"},
		},
	},
}

// renderHelp renders the help overlay content.
func renderHelp(width int) string {
	maxWidth := 60
	if width-4 < maxWidth {
		maxWidth = width - 4
	}
	if maxWidth < 30 {
		maxWidth = 30
	}

	title := overlayTitleStyle.Render("Keyboard Shortcuts")
	sections := make([]string, 0, len(helpSections)*4+3)
	sections = append(sections, title)

	for _, sec := range helpSections {
		header := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render(sec.title)
		sections = append(sections, "", header)

		for _, k := range sec.keys {
			keyCol := lipgloss.NewStyle().
				Width(14).
				Foreground(colorWhite).
				Bold(true).
				Render(k.key)
			descCol := lipgloss.NewStyle().
				Foreground(colorDim).
				Render(k.desc)
			sections = append(sections, "  "+keyCol+descCol)
		}
	}

	sections = append(sections, "", lipgloss.NewStyle().Foreground(colorDim).Render("Press Esc or Ctrl+h to close"))

	content := strings.Join(sections, "\n")
	return overlayStyle.Width(maxWidth).Render(content)
}
