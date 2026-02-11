package tui

import "github.com/charmbracelet/lipgloss"

// Colors using AdaptiveColor for light/dark terminal support.
var (
	colorWhite  = lipgloss.AdaptiveColor{Light: "0", Dark: "15"}
	colorDim    = lipgloss.AdaptiveColor{Light: "242", Dark: "240"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "28", Dark: "40"}
	colorRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "196"}
	colorYellow = lipgloss.AdaptiveColor{Light: "136", Dark: "220"}
	colorOrange = lipgloss.AdaptiveColor{Light: "166", Dark: "208"}
	colorCyan   = lipgloss.AdaptiveColor{Light: "30", Dark: "45"}
)

// Layout styles.
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(lipgloss.AdaptiveColor{Light: "235", Dark: "236"})

	focusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorWhite)

	unfocusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorDim)
)

// Tab styles.
var (
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Underline(true).
			Foreground(colorWhite)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	tabSepStyle = lipgloss.NewStyle().
			Foreground(colorDim)
)

// Task list styles.
var (
	taskDraftStyle  = lipgloss.NewStyle().Foreground(colorDim)
	taskReadyStyle  = lipgloss.NewStyle().Foreground(colorCyan)
	taskActiveStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	taskDoneStyle   = lipgloss.NewStyle().Foreground(colorGreen)
	taskFailedStyle = lipgloss.NewStyle().Foreground(colorRed)

	sectionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorWhite)

	selectedItemStyle = lipgloss.NewStyle().
				Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"})
)

// Agent badge styles.
var (
	badgeIdleStyle     = lipgloss.NewStyle().Foreground(colorDim)
	badgeActiveStyle   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	badgeWildfireStyle = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
	badgeIssueStyle    = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
)

// Overlay styles.
var (
	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorWhite).
			Padding(1, 2)

	overlayTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorWhite).
				MarginBottom(1)

	overlayDimStyle = lipgloss.NewStyle().
			Foreground(colorDim)
)

// Mode header styles (terminal panel).
var (
	modeHeaderStyle = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "237", Dark: "237"}).
			Foreground(colorGreen).
			Bold(true).
			Padding(0, 1)

	modeHeaderWildfireStyle = lipgloss.NewStyle().
				Background(lipgloss.AdaptiveColor{Light: "237", Dark: "237"}).
				Foreground(colorOrange).
				Bold(true).
				Padding(0, 1)
)

// Issue banner style.
var issueBannerStyle = lipgloss.NewStyle().
	Background(colorYellow).
	Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}).
	Bold(true).
	Padding(0, 1)

// Key hint styles for status bar.
var (
	keyStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	hintStyle = lipgloss.NewStyle().Foreground(colorDim)
)

// Settings form styles.
var (
	settingsLabelStyle = lipgloss.NewStyle().
				Width(20).
				Foreground(colorDim)

	settingsValueStyle = lipgloss.NewStyle().
				Foreground(colorWhite)

	settingsToggleOn = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	settingsToggleOff = lipgloss.NewStyle().
				Foreground(colorRed)

	settingsCursorStyle = lipgloss.NewStyle().
				Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"})
)
