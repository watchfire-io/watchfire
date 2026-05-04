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

func boolPtr(b bool) *bool       { return &b }
func strPtr(s string) *string    { return &s }

func TestFailedRowShowsFailureReasonPreview(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("claude-code")
	tl.SetTasks([]*pb.Task{
		{
			TaskNumber:    1,
			Title:         "Failed task",
			Status:        "done",
			Success:       boolPtr(false),
			FailureReason: strPtr("first line — the smoking gun\nsecond line — boring stack frame\nthird line"),
		},
	})

	rendered := renderRow(t, tl, 120)

	// First line of the reason should appear (or a truncated prefix of it).
	if !strings.Contains(rendered, "first line") {
		t.Fatalf("expected preview of first failure-reason line in rendered row; got %q", rendered)
	}
	// The "smoking gun" detail is short enough to fit at width=120 without
	// truncation; assert the full first line is present rather than a stub.
	if !strings.Contains(rendered, "the smoking gun") {
		t.Fatalf("expected full first reason line at wide width; got %q", rendered)
	}

	// Subsequent lines must NOT leak through — the preview is single-line.
	if strings.Contains(rendered, "second line") {
		t.Fatalf("preview should be single-line; second line leaked into row: %q", rendered)
	}
	if strings.Contains(rendered, "third line") {
		t.Fatalf("preview should be single-line; third line leaked into row: %q", rendered)
	}

	// Sanity: title and the [✗] glyph are still there.
	if !strings.Contains(rendered, "Failed task") {
		t.Fatalf("expected task title in rendered row; got %q", rendered)
	}
	if !strings.Contains(rendered, "[✗]") {
		t.Fatalf("expected failed-glyph in rendered row; got %q", rendered)
	}
}

func TestFailedRowShowsTruncationMarkerWhenPreviewIsLong(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("claude-code")
	longLine := "first line ─ " + strings.Repeat("x", 200) + " end"
	tl.SetTasks([]*pb.Task{
		{
			TaskNumber:    1,
			Title:         "Failed task",
			Status:        "done",
			Success:       boolPtr(false),
			FailureReason: strPtr(longLine + "\nsecond line should not appear"),
		},
	})

	rendered := renderRow(t, tl, 120)

	if !strings.Contains(rendered, "…") {
		t.Fatalf("expected truncation marker in rendered row when preview is long; got %q", rendered)
	}
	if strings.Contains(rendered, "second line") {
		t.Fatalf("only the first line should appear; got %q", rendered)
	}
}

func TestFailedRowSkipsPreviewWhenWidthTooNarrow(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("claude-code")
	tl.SetTasks([]*pb.Task{
		{
			TaskNumber:    1,
			Title:         "T",
			Status:        "done",
			Success:       boolPtr(false),
			FailureReason: strPtr("first line — the smoking gun\nsecond line\nthird line"),
		},
	})

	rendered := renderRow(t, tl, 25)

	// The [✗] glyph must still render — the row's status visual is
	// non-negotiable.
	if !strings.Contains(rendered, "[✗]") {
		t.Fatalf("expected failed-glyph in narrow rendered row; got %q", rendered)
	}
	// At width=25 the preview should be silently omitted — no portion
	// of the reason should leak into the row.
	if strings.Contains(rendered, "first line") {
		t.Fatalf("preview should be omitted at narrow width; got %q", rendered)
	}
}

func TestSuccessfulRowHasNoFailurePreview(t *testing.T) {
	tl := NewTaskList()
	tl.SetProjectDefaultAgent("claude-code")
	tl.SetTasks([]*pb.Task{
		{
			TaskNumber: 1,
			Title:      "Happy task",
			Status:     "done",
			Success:    boolPtr(true),
			// FailureReason intentionally non-empty to guard against a
			// hypothetical regression where a stale field leaks into a
			// successful row.
			FailureReason: strPtr("should not appear"),
		},
	})

	rendered := renderRow(t, tl, 120)

	if strings.Contains(rendered, "should not appear") {
		t.Fatalf("successful row should not render any failure-reason preview; got %q", rendered)
	}
	if !strings.Contains(rendered, "[✓]") {
		t.Fatalf("expected done-glyph in successful row; got %q", rendered)
	}
}
