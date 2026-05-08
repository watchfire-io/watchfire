package tui

import (
	"reflect"
	"runtime"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// cmdName resolves the function pointer behind a tea.Cmd to its
// fully-qualified name so tests can assert "this is the
// tea.DisableMouse function" without invoking it (calling these
// commands inside a unit test does nothing useful — the program loop
// is what consumes the resulting Msg). Returns "" for nil cmds.
func cmdName(c tea.Cmd) string {
	if c == nil {
		return ""
	}
	return runtime.FuncForPC(reflect.ValueOf(c).Pointer()).Name()
}

// TestToggleTextSelectMode_FromInteractive verifies the first toggle
// flips the flag on and returns tea.DisableMouse so the program drops
// mouse capture for the host terminal.
func TestToggleTextSelectMode_FromInteractive(t *testing.T) {
	m := &Model{}

	cmd := m.toggleTextSelectMode()

	if !m.textSelectMode {
		t.Fatalf("textSelectMode = false after first toggle, want true")
	}
	got := cmdName(cmd)
	want := cmdName(tea.Cmd(tea.DisableMouse))
	if got != want {
		t.Fatalf("first toggle returned %q, want %q (tea.DisableMouse)", got, want)
	}
}

// TestToggleTextSelectMode_FromTextSelect verifies the second toggle
// flips the flag back off and returns tea.EnableMouseCellMotion so
// click + wheel + drag dispatch resumes inside the program.
func TestToggleTextSelectMode_FromTextSelect(t *testing.T) {
	m := &Model{textSelectMode: true}

	cmd := m.toggleTextSelectMode()

	if m.textSelectMode {
		t.Fatalf("textSelectMode = true after toggle from on, want false")
	}
	got := cmdName(cmd)
	want := cmdName(tea.Cmd(tea.EnableMouseCellMotion))
	if got != want {
		t.Fatalf("second toggle returned %q, want %q (tea.EnableMouseCellMotion)", got, want)
	}
}

// TestToggleTextSelectMode_RoundTrip walks the full cycle and confirms
// each step lands on the right command — guards against a future
// regression where the flag flips without the command swapping.
func TestToggleTextSelectMode_RoundTrip(t *testing.T) {
	m := &Model{}

	// off → on
	c1 := m.toggleTextSelectMode()
	if !m.textSelectMode {
		t.Fatalf("step 1: textSelectMode = false, want true")
	}
	if cmdName(c1) != cmdName(tea.Cmd(tea.DisableMouse)) {
		t.Fatalf("step 1: cmd = %q, want tea.DisableMouse", cmdName(c1))
	}

	// on → off
	c2 := m.toggleTextSelectMode()
	if m.textSelectMode {
		t.Fatalf("step 2: textSelectMode = true, want false")
	}
	if cmdName(c2) != cmdName(tea.Cmd(tea.EnableMouseCellMotion)) {
		t.Fatalf("step 2: cmd = %q, want tea.EnableMouseCellMotion", cmdName(c2))
	}

	// off → on again — confirms the toggle isn't a one-shot
	c3 := m.toggleTextSelectMode()
	if !m.textSelectMode {
		t.Fatalf("step 3: textSelectMode = false, want true")
	}
	if cmdName(c3) != cmdName(tea.Cmd(tea.DisableMouse)) {
		t.Fatalf("step 3: cmd = %q, want tea.DisableMouse", cmdName(c3))
	}
}
