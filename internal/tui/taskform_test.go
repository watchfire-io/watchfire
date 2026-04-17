package tui

import (
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// TestTaskFormAgentOptionsIncludeProjectDefault verifies the agent cycler
// begins with a "Project default (<name>)" sentinel (empty value) and
// then every registered backend, in stable order.
func TestTaskFormAgentOptionsIncludeProjectDefault(t *testing.T) {
	tf := NewTaskForm("add", 70)
	tf.SetProjectDefaultAgent("claude-code")

	if len(tf.agentOptions) == 0 {
		t.Fatalf("expected at least one agent option")
	}
	if tf.agentOptions[0].Value != "" {
		t.Fatalf("expected first option to be project default (empty value), got %q", tf.agentOptions[0].Value)
	}
	if !strings.HasPrefix(tf.agentOptions[0].Display, "Project default") {
		t.Fatalf("expected first option display to start with 'Project default', got %q", tf.agentOptions[0].Display)
	}
	if !strings.Contains(tf.agentOptions[0].Display, "Claude Code") {
		t.Fatalf("expected project default label to reference resolved display name, got %q", tf.agentOptions[0].Display)
	}

	// Every registered backend should appear after the sentinel.
	registered := backend.List()
	if len(tf.agentOptions) != 1+len(registered) {
		t.Fatalf("expected %d options, got %d", 1+len(registered), len(tf.agentOptions))
	}
	for i, b := range registered {
		got := tf.agentOptions[i+1]
		if got.Value != b.Name() {
			t.Errorf("option %d: expected value %q, got %q", i+1, b.Name(), got.Value)
		}
		if got.Display != b.DisplayName() {
			t.Errorf("option %d: expected display %q, got %q", i+1, b.DisplayName(), got.Display)
		}
	}
}

// TestTaskFormAgentCycleRoundTrip verifies that cycling to a backend and
// reading Agent() returns the backend's Name(), and that PreFill()
// restores the correct selection on edit.
func TestTaskFormAgentCycleRoundTrip(t *testing.T) {
	tf := NewTaskForm("add", 70)
	tf.SetProjectDefaultAgent("claude-code")

	// Locate the Codex option (guaranteed to be registered).
	codex, ok := backend.Get("codex")
	if !ok {
		t.Skip("codex backend not registered in this build")
	}

	// Cycle forward until we land on codex.
	var found bool
	for i := 0; i < len(tf.agentOptions); i++ {
		if tf.Agent() == codex.Name() {
			found = true
			break
		}
		tf.CycleAgentNext()
	}
	if !found {
		t.Fatalf("failed to cycle to codex; current=%q options=%v", tf.Agent(), tf.agentOptions)
	}

	// Now simulate editing an existing task: PreFill re-selects codex.
	tf2 := NewTaskForm("edit", 70)
	tf2.SetProjectDefaultAgent("claude-code")
	tf2.PreFill(42, "title", "prompt", "criteria", "ready", codex.Name())
	if got := tf2.Agent(); got != codex.Name() {
		t.Fatalf("expected PreFill to select %q, got %q", codex.Name(), got)
	}
}

// TestTaskFormAgentDefaultsToProjectDefaultOnPrefill verifies that an
// empty agent field round-trips to the project-default sentinel
// (empty value, index 0).
func TestTaskFormAgentDefaultsToProjectDefaultOnPrefill(t *testing.T) {
	tf := NewTaskForm("edit", 70)
	tf.SetProjectDefaultAgent("claude-code")
	tf.PreFill(1, "t", "p", "c", "draft", "")

	if got := tf.Agent(); got != "" {
		t.Fatalf("expected agent empty (= project default), got %q", got)
	}
	if tf.agentIndex != 0 {
		t.Fatalf("expected agentIndex 0 (project default), got %d", tf.agentIndex)
	}
}

// TestTaskFormFocusCyclesThroughFiveFields ensures FocusNext cycles
// through title, prompt, criteria, agent, status and back to title.
func TestTaskFormFocusCyclesThroughFiveFields(t *testing.T) {
	tf := NewTaskForm("add", 70)
	if tf.FocusIndex() != taskFormFocusTitle {
		t.Fatalf("initial focus expected title, got %d", tf.FocusIndex())
	}
	expected := []int{
		taskFormFocusPrompt,
		taskFormFocusCriteria,
		taskFormFocusAgent,
		taskFormFocusStatus,
		taskFormFocusTitle,
	}
	for i, want := range expected {
		tf.FocusNext()
		if got := tf.FocusIndex(); got != want {
			t.Fatalf("step %d: expected focus %d, got %d", i+1, want, got)
		}
	}
}

// TestTaskFormCycleAgentPrevWraps verifies backward cycling wraps around.
func TestTaskFormCycleAgentPrevWraps(t *testing.T) {
	tf := NewTaskForm("add", 70)
	tf.SetProjectDefaultAgent("claude-code")

	n := len(tf.agentOptions)
	if n < 2 {
		t.Skip("not enough options to test wrap")
	}
	if tf.agentIndex != 0 {
		t.Fatalf("expected initial index 0, got %d", tf.agentIndex)
	}
	tf.CycleAgentPrev()
	if tf.agentIndex != n-1 {
		t.Fatalf("expected wrap to %d, got %d", n-1, tf.agentIndex)
	}
}
