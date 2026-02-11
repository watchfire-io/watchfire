package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// panelLayout holds computed dimensions for the two-panel layout.
type panelLayout struct {
	leftWidth     int
	rightWidth    int
	contentHeight int
	dividerCol    int // x position of the divider for mouse hit testing
}

func computeLayout(width, height int, splitRatio float64) panelLayout {
	// Reserve: 1 line header, 1 line status bar, 2 lines for borders top/bottom
	contentHeight := height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Panel widths including borders (2 chars each for left+right border)
	// Divider takes 1 char
	usable := width - 1 // 1 for divider
	leftWidth := int(float64(usable) * splitRatio)
	rightWidth := usable - leftWidth

	if leftWidth < 10 {
		leftWidth = 10
	}
	if rightWidth < 10 {
		rightWidth = 10
	}

	return panelLayout{
		leftWidth:     leftWidth,
		rightWidth:    rightWidth,
		contentHeight: contentHeight,
		dividerCol:    leftWidth,
	}
}

func renderPanels(leftContent, rightContent string, layout panelLayout, focusedPanel int) string {
	// Choose border styles based on focus
	leftStyle := unfocusedBorderStyle
	rightStyle := unfocusedBorderStyle
	if focusedPanel == 0 {
		leftStyle = focusedBorderStyle
	} else {
		rightStyle = focusedBorderStyle
	}

	// Inner dimensions (subtract 2 for border on each side)
	leftInner := layout.leftWidth - 2
	rightInner := layout.rightWidth - 2
	innerHeight := layout.contentHeight - 2 // subtract 2 for top+bottom border

	if leftInner < 1 {
		leftInner = 1
	}
	if rightInner < 1 {
		rightInner = 1
	}
	if innerHeight < 1 {
		innerHeight = 1
	}

	// Render panels with fixed dimensions
	left := leftStyle.
		Width(leftInner).
		Height(innerHeight).
		Render(truncateContent(leftContent, leftInner, innerHeight))

	right := rightStyle.
		Width(rightInner).
		Height(innerHeight).
		Render(truncateContent(rightContent, rightInner, innerHeight))

	// Divider
	divider := lipgloss.NewStyle().
		Foreground(colorDim).
		Render(strings.Repeat("│\n", lipgloss.Height(left)))
	if divider != "" && divider[len(divider)-1] == '\n' {
		divider = divider[:len(divider)-1]
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

// truncateContent ensures content fits within the given dimensions.
func truncateContent(content string, width, height int) string {
	lines := strings.Split(content, "\n")

	// Limit to height
	if len(lines) > height {
		lines = lines[:height]
	}

	// Truncate long lines (ANSI-aware)
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = ansi.Truncate(line, width, "")
		}
	}

	return strings.Join(lines, "\n")
}
