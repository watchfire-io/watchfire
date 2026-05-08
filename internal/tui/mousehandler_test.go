package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestResolveWheelTarget asserts the wheel-routing predicate picks the
// scrollable component under the cursor purely from (x, dividerCol,
// leftTab, rightTab). This is the v6 fix for the bug where wheel events
// were routed by m.focusedPanel — hover-and-scroll over the right panel
// would scroll the left task list when focus had not been clicked over.
func TestResolveWheelTarget(t *testing.T) {
	cases := []struct {
		name       string
		x          int
		dividerCol int
		leftTab    int
		rightTab   int
		want       wheelTarget
	}{
		// Cursor on left side → left tab decides target.
		{"left tasks", 5, 50, 0, 0, wheelTargetTaskList},
		{"left definition", 5, 50, 1, 0, wheelTargetDefinition},
		{"left settings", 5, 50, 2, 0, wheelTargetSettings},

		// Cursor on right side → right tab decides target.
		{"right terminal at boundary", 50, 50, 0, 0, wheelTargetTerminal},
		{"right terminal far", 100, 50, 0, 0, wheelTargetTerminal},
		{"right logs", 100, 50, 0, 1, wheelTargetLogs},

		// Routing is independent of the OTHER panel's tab — proves the
		// predicate doesn't mistakenly cross the divider.
		{"left side ignores rightTab", 5, 50, 0, 1, wheelTargetTaskList},
		{"right side ignores leftTab", 100, 50, 1, 0, wheelTargetTerminal},

		// Boundary case: x == dividerCol counts as right (matches the
		// click-handler convention in handleHeaderClick).
		{"boundary x == dividerCol", 50, 50, 0, 0, wheelTargetTerminal},

		// Out-of-range tab values fall back to None rather than panicking.
		{"unknown leftTab", 5, 50, 99, 0, wheelTargetNone},
		{"unknown rightTab", 100, 50, 0, 99, wheelTargetNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveWheelTarget(tc.x, tc.dividerCol, tc.leftTab, tc.rightTab)
			if got != tc.want {
				t.Errorf("resolveWheelTarget(x=%d, divider=%d, leftTab=%d, rightTab=%d) = %v, want %v",
					tc.x, tc.dividerCol, tc.leftTab, tc.rightTab, got, tc.want)
			}
		})
	}
}

// TestDispatchWheel_DoesNotChangeFocus verifies the wheel branch never
// mutates m.focusedPanel — wheel scroll must not steal focus, otherwise
// hover-and-scroll over the terminal clobbers an in-progress edit on a
// left-panel form.
func TestDispatchWheel_DoesNotChangeFocus(t *testing.T) {
	m := &Model{
		width:        100,
		height:       24,
		splitRatio:   0.5,
		focusedPanel: 0,
		leftTab:      0,
		rightTab:     0,
		terminal:     NewTerminal(),
	}
	layout := computeLayout(m.width, m.height, m.splitRatio)

	// Synthesise a wheel-up event in the right panel (x past dividerCol).
	// The dispatcher should scroll without touching focusedPanel.
	msg := tea.MouseMsg(tea.MouseEvent{
		X:      layout.dividerCol + 5,
		Y:      5,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	_ = m.dispatchWheel(msg, layout)

	if m.focusedPanel != 0 {
		t.Fatalf("focusedPanel mutated by wheel event: got %d, want 0", m.focusedPanel)
	}
}
