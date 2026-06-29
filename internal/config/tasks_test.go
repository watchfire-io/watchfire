package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/watchfire-io/watchfire/internal/models"
)

// TestSaveTaskRoundTripsSpecialCharacters is the core v8.0 Inferno guard: a
// task whose scalars contain YAML-significant characters (`:`, `#`, quotes, a
// leading `-`, em-dashes) must survive save -> load intact and stay a single
// scalar. This is exactly the failure class that made tasks 0101-0121
// silently invisible — an unquoted `title:` with a literal `: ` parsed as a
// nested mapping. SaveTask marshals via yaml.Marshal (which quotes correctly),
// so the round trip must hold.
func TestSaveTaskRoundTripsSpecialCharacters(t *testing.T) {
	cases := []struct {
		name  string
		title string
	}{
		{"colon-space", "v8 Inferno — Main: window registry + chat-primary layout"},
		{"hash", "Fix #22: markdown editor round-trips through YAML"},
		{"double-quote", `Write a blog post — "Headline: Subhead"`},
		{"apostrophe", "Operator's guide: reading the daemon logs"},
		{"leading-dash", "- dash-led title that YAML reads as a sequence"},
		{"leading-bang", "!important: do the thing"},
		{"flow-chars", "title with {braces} and [brackets] and , commas"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			writeMinimalProject(t, projectDir)

			task := models.NewTask("rndtrip1", 7, tc.title, "prompt body: with a colon\nand a second line")
			task.AcceptanceCriteria = "criteria: must hold\n- bullet one\n- bullet two"
			task.Status = models.TaskStatusReady
			task.FailureReason = "n/a: nothing failed # yet"

			if err := SaveTask(projectDir, task); err != nil {
				t.Fatalf("SaveTask: %v", err)
			}

			// The file must load without error (no silent drop) and preserve
			// every scalar exactly.
			loaded, err := LoadTask(projectDir, 7)
			if err != nil {
				t.Fatalf("LoadTask after save: %v", err)
			}
			if loaded == nil {
				t.Fatal("LoadTask returned nil after a successful save")
			}
			if loaded.Title != tc.title {
				t.Errorf("Title mismatch:\n  got  %q\n  want %q", loaded.Title, tc.title)
			}
			if loaded.Prompt != task.Prompt {
				t.Errorf("Prompt mismatch:\n  got  %q\n  want %q", loaded.Prompt, task.Prompt)
			}
			if loaded.AcceptanceCriteria != task.AcceptanceCriteria {
				t.Errorf("AcceptanceCriteria mismatch:\n  got  %q\n  want %q", loaded.AcceptanceCriteria, task.AcceptanceCriteria)
			}
			if loaded.FailureReason != task.FailureReason {
				t.Errorf("FailureReason mismatch:\n  got  %q\n  want %q", loaded.FailureReason, task.FailureReason)
			}

			// And LoadAllTasksWithErrors must report zero malformed files.
			tasks, malformed, err := LoadAllTasksWithErrors(projectDir)
			if err != nil {
				t.Fatalf("LoadAllTasksWithErrors: %v", err)
			}
			if len(malformed) != 0 {
				t.Errorf("expected 0 malformed files, got %d: %+v", len(malformed), malformed)
			}
			if len(tasks) != 1 {
				t.Errorf("expected 1 task, got %d", len(tasks))
			}
		})
	}
}

// TestValidateTaskAcceptsSpecialScalars confirms ValidateTask itself passes for
// scalars that would break an unquoted hand-authored file — the function
// round-trips through the same marshal/unmarshal path the loader uses.
func TestValidateTaskAcceptsSpecialScalars(t *testing.T) {
	task := models.NewTask("validat1", 3, "Main: window registry — see #22", "body")
	if err := ValidateTask(task); err != nil {
		t.Fatalf("ValidateTask rejected a marshalable task: %v", err)
	}
}

// writeMinimalProject drops a project.yaml that satisfies SaveProject's
// non-zero guards so SaveTask's downstream next_task_number sync is happy.
func writeMinimalProject(t *testing.T, projectDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(projectDir, ".watchfire"), 0o755); err != nil {
		t.Fatalf("mkdir .watchfire: %v", err)
	}
	proj := `version: 1
project_id: test-project
name: test
default_branch: main
next_task_number: 1
`
	if err := os.WriteFile(filepath.Join(projectDir, ".watchfire", "project.yaml"), []byte(proj), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
}

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

// TestLoadAllTasksWithErrorsSurfacesUnquotedTitle reproduces the exact v8 bug:
// an unquoted `title:` containing a second `: ` is parsed by yaml.v3 as a
// nested mapping and rejected. The file must no longer silently vanish —
// LoadAllTasksWithErrors must report it as malformed (with its number and the
// parse error) while still loading the sibling good file.
func TestLoadAllTasksWithErrorsSurfacesUnquotedTitle(t *testing.T) {
	projectDir := t.TempDir()
	tasksDir := filepath.Join(projectDir, ".watchfire", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// The literal failure shape from tasks 0101-0121.
	bad := `version: 1
task_id: badcolon1
task_number: 107
title: v8 Inferno — Main: window registry + chat-primary layout
prompt: irrelevant
status: ready
position: 0
agent_sessions: 0
`
	if err := os.WriteFile(filepath.Join(tasksDir, "0107.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
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

	tasks, malformed, err := LoadAllTasksWithErrors(projectDir)
	if err != nil {
		t.Fatalf("LoadAllTasksWithErrors must not propagate per-file errors: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskNumber != 1 {
		t.Fatalf("expected only good task #1 to load, got %+v", tasks)
	}
	if len(malformed) != 1 {
		t.Fatalf("expected 1 malformed file surfaced, got %d: %+v", len(malformed), malformed)
	}
	mf := malformed[0]
	if mf.TaskNumber != 107 {
		t.Errorf("malformed TaskNumber = %d, want 107", mf.TaskNumber)
	}
	if mf.FileName != "0107.yaml" {
		t.Errorf("malformed FileName = %q, want 0107.yaml", mf.FileName)
	}
	if mf.Error == "" {
		t.Error("malformed Error should carry the parse error, got empty")
	}

	// LoadAllTasks (the back-compat wrapper) must still drop it silently and
	// not error — the chain keeps moving.
	plain, err := LoadAllTasks(projectDir)
	if err != nil {
		t.Fatalf("LoadAllTasks: %v", err)
	}
	if len(plain) != 1 {
		t.Fatalf("LoadAllTasks expected 1 task, got %d", len(plain))
	}
}

// TestLoadAllTasksWithErrorsIgnoresMetricsSiblings makes sure a sibling
// `<n>.metrics.yaml` is never mistaken for a malformed task file.
func TestLoadAllTasksWithErrorsIgnoresMetricsSiblings(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(tasksDir, "0001.metrics.yaml"), []byte("commits: 3\nfiles_changed: 2\n"), 0o644); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	tasks, malformed, err := LoadAllTasksWithErrors(projectDir)
	if err != nil {
		t.Fatalf("LoadAllTasksWithErrors: %v", err)
	}
	if len(malformed) != 0 {
		t.Fatalf("metrics sibling must not be flagged malformed, got %+v", malformed)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
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
