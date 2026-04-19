package task

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// setupTempProject creates a minimal project on disk so Create/UpdateTask can
// round-trip against real YAML files. Returns the project path.
func setupTempProject(t *testing.T) string {
	t.Helper()
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	now := time.Now().UTC()
	p := &models.Project{
		ProjectID:      "testproj",
		Name:           "test",
		Status:         "active",
		NextTaskNumber: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := config.SaveProject(projectPath, p); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	return projectPath
}

func TestCreateTaskPersistsAgent(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "Use Codex for this",
		Prompt: "do stuff",
		Status: string(models.TaskStatusReady),
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Agent != "codex" {
		t.Fatalf("Agent on created: got %q, want %q", created.Agent, "codex")
	}

	// Reload from disk to verify YAML round-trip.
	loaded, err := config.LoadTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Agent != "codex" {
		t.Errorf("Agent after reload: got %q, want %q", loaded.Agent, "codex")
	}
}

func TestCreateTaskWithoutAgentLeavesFieldEmpty(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "Defaults to project",
		Prompt: "do stuff",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Agent != "" {
		t.Errorf("Agent should be empty, got %q", created.Agent)
	}
}

func TestUpdateTaskSetsAgent(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "t",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newAgent := "codex"
	updated, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: created.TaskNumber,
		Agent:      &newAgent,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Agent != "codex" {
		t.Errorf("Agent after update: got %q, want %q", updated.Agent, "codex")
	}
}

func TestUpdateTaskClearsAgentWithExplicitEmptyString(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "t",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Agent != "codex" {
		t.Fatalf("precondition: Agent not persisted on create")
	}

	empty := ""
	updated, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: created.TaskNumber,
		Agent:      &empty,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Agent != "" {
		t.Errorf("Agent should be cleared, got %q", updated.Agent)
	}

	loaded, err := config.LoadTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Agent != "" {
		t.Errorf("Agent should be cleared on disk, got %q", loaded.Agent)
	}
}

// TestListTasksDescendingByTaskNumber is a regression test for #28. When a
// project has many tasks with mixed statuses, the task list must come back in
// a single canonical order — descending by task_number — regardless of the
// order files are read from disk or the legacy `position` field. Before the
// fix, sorting by `position` first caused status-grouped consumers (TUI,
// GUI) to render a rotated list like 0017→0047 followed by 0001→0016.
func TestListTasksDescendingByTaskNumber(t *testing.T) {
	projectPath := setupTempProject(t)
	now := time.Now().UTC()

	// Write 25 task YAMLs directly to disk in a non-sequential order so the
	// test exercises the manager's sort rather than filesystem iteration
	// order. Alternate statuses so a status-grouped consumer would scramble
	// the list if the manager ever stopped returning a canonical order.
	order := []int{13, 1, 25, 7, 20, 2, 19, 8, 14, 3, 24, 9, 15, 4, 23, 10, 16, 5, 22, 11, 17, 6, 21, 12, 18}
	for _, n := range order {
		status := models.TaskStatusReady
		switch n % 3 {
		case 0:
			status = models.TaskStatusDone
		case 1:
			status = models.TaskStatusDraft
		}
		task := &models.Task{
			Version:    1,
			TaskID:     "id",
			TaskNumber: n,
			Title:      "t",
			Prompt:     "p",
			Status:     status,
			Position:   n, // Legacy field; must not influence ordering.
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := config.SaveTask(projectPath, task); err != nil {
			t.Fatalf("SaveTask %d: %v", n, err)
		}
	}

	listed, err := NewManager().ListTasks(projectPath, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(listed) != 25 {
		t.Fatalf("expected 25 tasks, got %d", len(listed))
	}

	for i, got := range listed {
		want := 25 - i
		if got.TaskNumber != want {
			t.Errorf("listed[%d].TaskNumber = %d, want %d (expected strictly descending)", i, got.TaskNumber, want)
		}
	}

	// Confirm position values don't sneak back in as a tiebreaker: bump the
	// Position of the highest-numbered task to the lowest value and re-list.
	// With a canonical task_number-descending sort the order must be
	// unchanged.
	highest := listed[0]
	highest.Position = -1
	if err := config.SaveTask(projectPath, highest); err != nil {
		t.Fatalf("SaveTask highest: %v", err)
	}
	relisted, err := NewManager().ListTasks(projectPath, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks (relisted): %v", err)
	}
	if relisted[0].TaskNumber != 25 {
		t.Errorf("after reshuffling position, first task = %d, want 25 (position must not affect order)", relisted[0].TaskNumber)
	}
}

func TestUpdateTaskNilAgentLeavesUnchanged(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "t",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newTitle := "new title"
	updated, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: created.TaskNumber,
		Title:      &newTitle,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Agent != "codex" {
		t.Errorf("Agent should stay codex, got %q", updated.Agent)
	}
}
