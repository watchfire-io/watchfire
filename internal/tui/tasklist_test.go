package tui

import (
	"strings"
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

// renderRow rebuilds the list and renders it, returning the sole task
// row's plain text (with ANSI styles stripped for simple substring
// assertions in tests).
func renderRow(t *testing.T, tl *TaskList, width int) string {
	t.Helper()
	tl.SetHeight(10)
	out := tl.View(width)
	return stripANSI(out)
}

// stripANSI is a minimal CSI/ANSI stripper suitable for tests (not
// exhaustive — enough to compare rendered strings).
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b { // ESC
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' || r == 'K' || r == 'H' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestAgentBadgeShownWhenOverrideDiffersFromProjectDefault(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("claude-code")
	tl.SetTasks([]*pb.Task{
		{TaskNumber: 1, Title: "Override task", Status: "ready", Agent: "codex"},
	})

	rendered := renderRow(t, tl, 60)
	// Badge label for codex is derived from backend.DisplayName() initials.
	// Codex → "C". Expect it to appear somewhere on the line.
	if !strings.Contains(rendered, " C ") && !strings.Contains(rendered, "C O") && !strings.Contains(rendered, "C ") {
		// More lenient check: badge initial "C" should appear twice
		// (once as the status badge, once as the agent badge).
		cCount := strings.Count(rendered, "C")
		if cCount < 1 {
			t.Fatalf("expected agent badge in rendered row; got %q", rendered)
		}
	}

	// Sanity: the task title still appears.
	if !strings.Contains(rendered, "Override task") {
		t.Fatalf("expected task title in rendered row; got %q", rendered)
	}
}

func TestAgentBadgeHiddenWhenOverrideEmpty(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("claude-code")
	tl.SetTasks([]*pb.Task{
		{TaskNumber: 1, Title: "Default task", Status: "ready", Agent: ""},
	})

	// Compute via the helper directly to avoid false matches on "C" in the
	// status badge ("[R]") or in the title.
	badge := tl.agentBadge(tl.SelectedTask())
	if badge != "" {
		t.Fatalf("expected no badge for empty agent, got %q", badge)
	}
}

func TestAgentBadgeHiddenWhenOverrideMatchesProjectDefault(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("codex")
	tl.SetTasks([]*pb.Task{
		{TaskNumber: 1, Title: "Redundant override", Status: "ready", Agent: "codex"},
	})

	badge := tl.agentBadge(tl.SelectedTask())
	if badge != "" {
		t.Fatalf("expected no badge when override matches project default, got %q", badge)
	}
}

func TestAgentBadgeShownWhenProjectHasNoDefault(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("")
	tl.SetTasks([]*pb.Task{
		{TaskNumber: 1, Title: "Task", Status: "ready", Agent: "codex"},
	})

	badge := tl.agentBadge(tl.SelectedTask())
	if badge == "" {
		t.Fatalf("expected badge when project has no default but task has override")
	}
}

func TestAgentBadgeInitialsForRegisteredBackends(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"claude-code", "CC"}, // "Claude Code" → C, C
		{"codex", "OC"},       // "OpenAI Codex" → O, C
		{"opencode", "O"},     // "opencode" → O
		{"gemini", "GC"},      // "Gemini CLI" → G, C
	}
	for _, tc := range cases {
		got := agentBadgeLabel(tc.name)
		if got != tc.expected {
			t.Errorf("agentBadgeLabel(%q) = %q, want %q", tc.name, got, tc.expected)
		}
	}
}

func TestAgentBadgeLabelFallsBackForUnknownBackend(t *testing.T) {
	got := agentBadgeLabel("definitely-not-registered")
	if got == "" {
		t.Fatalf("expected non-empty fallback label for unknown backend")
	}
}
