package prompts

import (
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/models"
)

func boolPtr(b bool) *bool { return &b }

func TestComposeRetrofitDefinitionSystemPrompt(t *testing.T) {
	project := &models.Project{
		Name:       "demo",
		Definition: "# Demo\n\nA CLI tool for demos.\n\n## Shipped: v1.0\nInitial release.",
	}
	tasks := []RetrofitTask{
		NewRetrofitTask(3, "Add export command", "Implement `demo export` writing JSON.", boolPtr(true), ""),
		NewRetrofitTask(5, "Fix crash on empty input", "Guard the parser.", boolPtr(false), "blocked on upstream"),
	}

	got := ComposeRetrofitDefinitionSystemPrompt(project, tasks)

	// The current definition rides along via the base prompt.
	for _, want := range []string{
		"## Project Instructions",
		"A CLI tool for demos.",
		"## Retrofit Definition Mode",
		// Both tasks with number, title, and outcome.
		"Task #0003: Add export command** — succeeded",
		"Implement `demo export` writing JSON.",
		"Task #0005: Fix crash on empty input** — failed: blocked on upstream",
		// The durable-document instructions and the completion signal.
		"Only modify the `definition` field",
		".watchfire/retrofit_done.yaml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestComposeRetrofitDefinitionUserPrompt(t *testing.T) {
	got := ComposeRetrofitDefinitionUserPrompt()
	if !strings.Contains(got, "Retrofit the project definition") {
		t.Errorf("unexpected user prompt: %q", got)
	}
}

func TestNewRetrofitTaskTruncatesLongPrompts(t *testing.T) {
	long := strings.Repeat("x", maxRetrofitPromptLen+500)
	rt := NewRetrofitTask(1, "big", long, nil, "")
	if len(rt.Prompt) >= len(long) {
		t.Fatalf("prompt not truncated: len=%d", len(rt.Prompt))
	}
	if !strings.Contains(rt.Prompt, "[... prompt truncated ...]") {
		t.Errorf("missing truncation marker")
	}
	if rt.Outcome != "completed" {
		t.Errorf("nil success should read as %q, got %q", "completed", rt.Outcome)
	}
}
