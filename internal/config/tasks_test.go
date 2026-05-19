package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadAllTasksSkipsCorruptFile locks in the v7.2.0 fix: a single
// malformed task YAML must not prevent the rest of the directory from
// loading. Before the fix, LoadAllTasks returned the first parse error,
// nextTaskFn propagated it, and the wildfire chain silently halted.
func TestLoadAllTasksSkipsCorruptFile(t *testing.T) {
	projectDir := t.TempDir()
	tasksDir := filepath.Join(projectDir, ".watchfire", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	good := `version: 1
task_id: goodtask
task_number: 1
title: good
prompt: good
status: ready
position: 1
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
updated_at: 2026-05-19T18:00:00Z
`
	if err := os.WriteFile(filepath.Join(tasksDir, "0001.yaml"), []byte(good), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}

	// Genuinely corrupt YAML — not just an empty timestamp, since the v7.2.0
	// Task.UnmarshalYAML now handles those cleanly. Unbalanced bracket forces
	// the parser to fail.
	corrupt := `version: 1
task_id: badtask1
task_number: 2
title: [unterminated
status: ready
`
	if err := os.WriteFile(filepath.Join(tasksDir, "0002.yaml"), []byte(corrupt), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	another := `version: 1
task_id: thirdtsk
task_number: 3
title: third
prompt: third
status: ready
position: 3
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
updated_at: 2026-05-19T18:00:00Z
`
	if err := os.WriteFile(filepath.Join(tasksDir, "0003.yaml"), []byte(another), 0o644); err != nil {
		t.Fatalf("write another: %v", err)
	}

	tasks, err := LoadAllTasks(projectDir)
	if err != nil {
		t.Fatalf("LoadAllTasks must not propagate per-file errors: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks (corrupt skipped), got %d", len(tasks))
	}
	gotNums := map[int]bool{}
	for _, ta := range tasks {
		gotNums[ta.TaskNumber] = true
	}
	if !gotNums[1] || !gotNums[3] {
		t.Errorf("expected tasks 1 and 3 to load, got %+v", gotNums)
	}
}

// TestLoadAllTasksHandlesEmptyStartedAt is the end-to-end version of the
// model-level UnmarshalYAML test — it confirms the full LoadAllTasks ->
// LoadTask -> Task.UnmarshalYAML chain accepts the exact bytes Claude
// emitted in the live wildfire-website failure.
func TestLoadAllTasksHandlesEmptyStartedAt(t *testing.T) {
	projectDir := t.TempDir()
	tasksDir := filepath.Join(projectDir, ".watchfire", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	bad := `version: 1
task_id: cmpslg43
task_number: 143
title: live-failure-repro
prompt: irrelevant
status: ready
position: 0
agent_sessions: 0
created_at: 2026-05-19T18:00:00Z
started_at: ""
updated_at: 2026-05-19T18:00:00Z
`
	if err := os.WriteFile(filepath.Join(tasksDir, "0143.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tasks, err := LoadAllTasks(projectDir)
	if err != nil {
		t.Fatalf("must decode despite empty started_at: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].StartedAt != nil {
		t.Errorf("expected nil StartedAt, got %v", tasks[0].StartedAt)
	}
	if tasks[0].TaskNumber != 143 {
		t.Errorf("wrong task: %+v", tasks[0])
	}
}
