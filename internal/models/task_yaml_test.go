package models

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestTaskUnmarshalEmptyStartedAt locks in the v7.2.0 fix. Before the fix,
// the strict gopkg.in/yaml.v3 time decoder rejected `started_at: ""` with
// `cannot parse "" as "2006"`, which propagated up through LoadTask →
// LoadAllTasks → taskMgr.ListTasks → wildfire nextTaskFn and silently
// halted the wildfire chain after a generate phase whenever the agent
// emitted that exact form.
func TestTaskUnmarshalEmptyStartedAt(t *testing.T) {
	src := `version: 1
task_id: cmpslg43
task_number: 143
title: dummy
prompt: dummy
status: ready
position: 0
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
started_at: ""
updated_at: 2026-05-19T18:00:00Z
`
	var task Task
	if err := yaml.Unmarshal([]byte(src), &task); err != nil {
		t.Fatalf("expected empty started_at to decode to nil, got %v", err)
	}
	if task.StartedAt != nil {
		t.Errorf("expected nil StartedAt, got %v", task.StartedAt)
	}
	if task.TaskID != "cmpslg43" || task.TaskNumber != 143 {
		t.Errorf("rest of task decoded incorrectly: %+v", task)
	}
}

// TestTaskUnmarshalAllEmptyTimes covers every time-typed field at once. The
// non-pointer ones (created_at, updated_at) must land at the zero time
// rather than erroring.
func TestTaskUnmarshalAllEmptyTimes(t *testing.T) {
	src := `version: 1
task_id: presskt4
task_number: 144
title: dummy
prompt: dummy
status: ready
position: 0
agent_sessions: 0
created_at: ""
started_at: ""
completed_at: ""
updated_at: ""
deleted_at: ""
`
	var task Task
	if err := yaml.Unmarshal([]byte(src), &task); err != nil {
		t.Fatalf("expected all-empty timestamps to decode cleanly, got %v", err)
	}
	if !task.CreatedAt.IsZero() {
		t.Errorf("expected zero CreatedAt, got %v", task.CreatedAt)
	}
	if !task.UpdatedAt.IsZero() {
		t.Errorf("expected zero UpdatedAt, got %v", task.UpdatedAt)
	}
	if task.StartedAt != nil || task.CompletedAt != nil || task.DeletedAt != nil {
		t.Errorf("expected nil pointer times, got Started=%v Completed=%v Deleted=%v",
			task.StartedAt, task.CompletedAt, task.DeletedAt)
	}
}

// TestTaskUnmarshalNullStartedAt confirms the explicit-null branch still
// works (agents that emit `started_at: null` rather than `""`).
func TestTaskUnmarshalNullStartedAt(t *testing.T) {
	src := `version: 1
task_id: nullsat0
task_number: 1
title: dummy
prompt: dummy
status: ready
position: 0
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
started_at: null
updated_at: 2026-05-19T18:00:00Z
`
	var task Task
	if err := yaml.Unmarshal([]byte(src), &task); err != nil {
		t.Fatalf("explicit null should still decode: %v", err)
	}
	if task.StartedAt != nil {
		t.Errorf("expected nil StartedAt from explicit null, got %v", task.StartedAt)
	}
}

// TestTaskUnmarshalOmittedStartedAt confirms the well-formed case
// (started_at field omitted entirely) is unaffected by the new fix.
func TestTaskUnmarshalOmittedStartedAt(t *testing.T) {
	src := `version: 1
task_id: omitsat0
task_number: 1
title: dummy
prompt: dummy
status: ready
position: 0
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
updated_at: 2026-05-19T18:00:00Z
`
	var task Task
	if err := yaml.Unmarshal([]byte(src), &task); err != nil {
		t.Fatalf("well-formed task without started_at must still decode: %v", err)
	}
	if task.StartedAt != nil {
		t.Errorf("expected nil StartedAt when field is omitted, got %v", task.StartedAt)
	}
}

// TestTaskUnmarshalValidStartedAtUnchanged confirms a real timestamp still
// decodes normally — the fix only kicks in for empty-string scalars.
func TestTaskUnmarshalValidStartedAtUnchanged(t *testing.T) {
	src := `version: 1
task_id: realsat0
task_number: 1
title: dummy
prompt: dummy
status: ready
position: 0
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
started_at: 2026-05-19T12:00:00Z
updated_at: 2026-05-19T18:00:00Z
`
	var task Task
	if err := yaml.Unmarshal([]byte(src), &task); err != nil {
		t.Fatalf("real timestamp must decode: %v", err)
	}
	if task.StartedAt == nil {
		t.Fatalf("expected non-nil StartedAt")
	}
	want := "2026-05-19 12:00:00 +0000 UTC"
	if got := task.StartedAt.UTC().String(); !strings.HasPrefix(got, "2026-05-19 12:00:00") {
		t.Errorf("StartedAt mismatch: got %q want prefix %q", got, want)
	}
}
