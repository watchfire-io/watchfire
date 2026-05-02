package agent

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// ptrBool is a small helper to take the address of a bool literal (the model
// stores Success as *bool to distinguish "not set" from "explicit false").
func ptrBool(b bool) *bool { return &b }

// TestRunCompleteMixedRun simulates a run with a mix of successful and failed
// tasks completed inside the run window. The body must reflect both counts
// and the helper must agree the run is RUN_COMPLETE-eligible. This is the
// "exactly one RUN_COMPLETE with the correct body" case from the spec.
func TestRunCompleteMixedRun(t *testing.T) {
	runStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	inWindow := runStart.Add(2 * time.Minute)
	beforeWindow := runStart.Add(-1 * time.Hour)

	tasks := []*models.Task{
		// Two successful completions inside the run window.
		{TaskNumber: 1, Status: models.TaskStatusDone, Success: ptrBool(true), UpdatedAt: inWindow},
		{TaskNumber: 2, Status: models.TaskStatusDone, Success: ptrBool(true), UpdatedAt: inWindow},
		// One failure inside the run window.
		{TaskNumber: 3, Status: models.TaskStatusDone, Success: ptrBool(false), UpdatedAt: inWindow},
		// A pre-existing completion that predates the run window — must be
		// excluded from the count, even though it's status=done success=true.
		{TaskNumber: 4, Status: models.TaskStatusDone, Success: ptrBool(true), UpdatedAt: beforeWindow},
		// Still ready / draft work — never counted regardless of UpdatedAt.
		{TaskNumber: 5, Status: models.TaskStatusReady, UpdatedAt: inWindow},
	}

	got := runCompleteBody(tasks, runStart)
	want := "2 tasks done · 1 failed"
	if got != want {
		t.Errorf("runCompleteBody mixed: got %q, want %q", got, want)
	}

	if !runCompleteApplicable(ModeStartAll) {
		t.Errorf("runCompleteApplicable(ModeStartAll) = false, want true")
	}
	if !runCompleteApplicable(ModeWildfire) {
		t.Errorf("runCompleteApplicable(ModeWildfire) = false, want true")
	}
	if !runCompleteApplicable(ModeTask) {
		t.Errorf("runCompleteApplicable(ModeTask) = false, want true (single-task runs still emit)")
	}
}

// TestRunCompleteChatModeSkipped verifies that a chat-mode session never
// emits RUN_COMPLETE — chat mode has no autonomous chaining, so the falling
// edge of a chat session is not the falling edge of a "run" in the v5.0
// Pulse sense.
func TestRunCompleteChatModeSkipped(t *testing.T) {
	if runCompleteApplicable(ModeChat) {
		t.Errorf("ModeChat must not be RUN_COMPLETE-eligible")
	}
	if runCompleteApplicable(ModeGenerateDefinition) {
		t.Errorf("ModeGenerateDefinition must not be RUN_COMPLETE-eligible")
	}
	if runCompleteApplicable(ModeGenerateTasks) {
		t.Errorf("ModeGenerateTasks must not be RUN_COMPLETE-eligible")
	}
}

// TestRunCompleteEmptyWindowSkipped verifies that a run that completed zero
// tasks (e.g. user-aborted before the agent did anything) does not emit
// RUN_COMPLETE — runCompleteBody returns the empty string, which the
// emitRunComplete gate uses to short-circuit.
func TestRunCompleteEmptyWindowSkipped(t *testing.T) {
	runStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	beforeWindow := runStart.Add(-30 * time.Minute)

	cases := []struct {
		name  string
		tasks []*models.Task
	}{
		{"no tasks at all", nil},
		{"only ready / draft tasks", []*models.Task{
			{TaskNumber: 1, Status: models.TaskStatusReady, UpdatedAt: runStart.Add(time.Minute)},
			{TaskNumber: 2, Status: models.TaskStatusDraft, UpdatedAt: runStart.Add(time.Minute)},
		}},
		{"only completions outside the window", []*models.Task{
			{TaskNumber: 1, Status: models.TaskStatusDone, Success: ptrBool(true), UpdatedAt: beforeWindow},
			{TaskNumber: 2, Status: models.TaskStatusDone, Success: ptrBool(false), UpdatedAt: beforeWindow},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runCompleteBody(tc.tasks, runStart); got != "" {
				t.Errorf("expected empty body for %s, got %q", tc.name, got)
			}
		})
	}
}
