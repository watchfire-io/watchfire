package cli

import "github.com/charmbracelet/lipgloss"

// Adaptive colors matching the TUI palette.
var (
	colorWhite  = lipgloss.AdaptiveColor{Light: "0", Dark: "15"}
	colorDim    = lipgloss.AdaptiveColor{Light: "242", Dark: "240"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "28", Dark: "40"}
	colorRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "196"}
	colorYellow = lipgloss.AdaptiveColor{Light: "136", Dark: "220"}
	colorOrange = lipgloss.AdaptiveColor{Light: "166", Dark: "208"}
	colorCyan   = lipgloss.AdaptiveColor{Light: "30", Dark: "45"}
)

// Semantic styles for CLI output.
var (
	styleBrand   = lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	styleVersion = lipgloss.NewStyle().Foreground(colorGreen)
	styleLabel   = lipgloss.NewStyle().Foreground(colorDim)
	styleValue   = lipgloss.NewStyle().Foreground(colorWhite)
	styleSuccess = lipgloss.NewStyle().Foreground(colorGreen)
	styleWarning = lipgloss.NewStyle().Bold(true).Foreground(colorYellow)
	styleError   = lipgloss.NewStyle().Bold(true).Foreground(colorRed)
	styleHint    = lipgloss.NewStyle().Foreground(colorDim)
	styleCommand = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	styleUpdate  = lipgloss.NewStyle().Bold(true).Foreground(colorOrange)
)

// Task status badge styles.
var (
	badgeDraft = lipgloss.NewStyle().Foreground(colorDim)
	badgeReady = lipgloss.NewStyle().Foreground(colorCyan)
	badgeDone  = lipgloss.NewStyle().Foreground(colorGreen)
)
