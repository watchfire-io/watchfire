package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Overlay constants.
const (
	overlayNone     = 0
	overlayHelp     = 1
	overlayAddTask  = 2
	overlayEditTask = 3
)

// renderOverlay renders an overlay centered on top of the base view.
func renderOverlay(base, overlayContent string, width, height int) string {
	// Dim the background
	baseLines := strings.Split(base, "\n")
	for i, line := range baseLines {
		baseLines[i] = overlayDimStyle.Render(line)
	}
	dimmed := strings.Join(baseLines, "\n")

	// Calculate overlay position
	overlayLines := strings.Split(overlayContent, "\n")
	overlayHeight := len(overlayLines)
	overlayWidth := 0
	for _, l := range overlayLines {
		if w := lipgloss.Width(l); w > overlayWidth {
			overlayWidth = w
		}
	}

	// Center
	top := (height - overlayHeight) / 2
	left := (width - overlayWidth) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}

	// Place overlay on top of dimmed background using ANSI-aware slicing
	result := strings.Split(dimmed, "\n")
	for i, line := range overlayLines {
		row := top + i
		if row >= len(result) {
			continue
		}
		bg := result[row]
		bgWidth := lipgloss.Width(bg)

		// Left portion of background (columns 0..left-1)
		leftPart := ansi.Truncate(bg, left, "")

		// Right portion of background (columns left+overlayWidth..)
		rightPart := ""
		rightStart := left + lipgloss.Width(line)
		if rightStart < bgWidth {
			rightPart = ansi.Cut(bg, rightStart, bgWidth)
		}

		// Compose: left background + reset + overlay + reset + right background
		result[row] = leftPart + "\033[0m" + line + "\033[0m" + rightPart
	}

	return strings.Join(result, "\n")
}
