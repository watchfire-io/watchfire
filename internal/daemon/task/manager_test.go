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
		Version:        1,
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

func TestBulkUpdateStatusMovesAllAndSkipsNoops(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	// Create three draft tasks and one already-ready task.
	var drafts []int
	for i := 0; i < 3; i++ {
		created, err := m.CreateTask(projectPath, CreateOptions{
			Title:  "draft",
			Prompt: "p",
			Status: string(models.TaskStatusDraft),
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		drafts = append(drafts, created.TaskNumber)
	}
	already, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "already ready",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	nums := append([]int{}, drafts...)
	nums = append(nums, already.TaskNumber)

	updated, err := m.BulkUpdateStatus(projectPath, nums, string(models.TaskStatusReady))
	if err != nil {
		t.Fatalf("BulkUpdateStatus: %v", err)
	}
	// Only the 3 drafts should be touched; the already-ready task is a no-op.
	if len(updated) != 3 {
		t.Fatalf("expected 3 updated tasks, got %d", len(updated))
	}
	// Canonical order: newest first.
	for i := 0; i < len(updated)-1; i++ {
		if updated[i].TaskNumber <= updated[i+1].TaskNumber {
			t.Errorf("expected newest-first order, got %d before %d", updated[i].TaskNumber, updated[i+1].TaskNumber)
		}
	}
	// Verify persisted status.
	for _, n := range drafts {
		loaded, err := config.LoadTask(projectPath, n)
		if err != nil {
			t.Fatalf("LoadTask %d: %v", n, err)
		}
		if loaded.Status != models.TaskStatusReady {
			t.Errorf("task %d: got status %s, want ready", n, loaded.Status)
		}
	}
}

func TestBulkUpdateStatusRejectsInvalidStatus(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, _ := m.CreateTask(projectPath, CreateOptions{
		Title: "t", Prompt: "p", Status: string(models.TaskStatusDraft),
	})
	if _, err := m.BulkUpdateStatus(projectPath, []int{created.TaskNumber}, "bogus"); err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
}

func TestBulkUpdateStatusToDoneSetsSuccess(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, _ := m.CreateTask(projectPath, CreateOptions{
		Title: "t", Prompt: "p", Status: string(models.TaskStatusReady),
	})
	updated, err := m.BulkUpdateStatus(projectPath, []int{created.TaskNumber}, string(models.TaskStatusDone))
	if err != nil {
		t.Fatalf("BulkUpdateStatus: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("expected 1 updated task, got %d", len(updated))
	}
	if updated[0].Success == nil || !*updated[0].Success {
		t.Errorf("expected Success=true, got %v", updated[0].Success)
	}
	if updated[0].CompletedAt == nil {
		t.Errorf("expected CompletedAt to be set")
	}
}

// TestRestoreTaskRoundTrip verifies that DeleteTask + RestoreTask leave
// the task fully visible again with deleted_at cleared, so the v6 trash
// filter mode's `u` action returns the row to the active list on the
// very next ListTasks call.
func TestRestoreTaskRoundTrip(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "soon to be trash",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	deleted, err := m.DeleteTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if !deleted.IsDeleted() {
		t.Fatalf("DeleteTask did not mark task as deleted")
	}

	active, err := m.ListTasks(projectPath, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks active: %v", err)
	}
	for _, task := range active {
		if task.TaskNumber == created.TaskNumber {
			t.Fatalf("deleted task should not appear in active list")
		}
	}

	restored, err := m.RestoreTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("RestoreTask: %v", err)
	}
	if restored.IsDeleted() {
		t.Fatalf("RestoreTask did not clear deleted_at")
	}

	active, err = m.ListTasks(projectPath, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks after restore: %v", err)
	}
	found := false
	for _, task := range active {
		if task.TaskNumber == created.TaskNumber {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored task should appear in active list again")
	}
}

// TestPermanentDeleteRefusesUnmergedBranch is the safety guard for `x`
// in trash mode: when the branchMerged callback reports unmerged the
// task YAML must remain on disk untouched.
func TestPermanentDeleteRefusesUnmergedBranch(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "unmerged work",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := m.DeleteTask(projectPath, created.TaskNumber); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	unmerged := func(_ int) (bool, error) { return false, nil }
	err = m.PermanentDelete(projectPath, created.TaskNumber, unmerged)
	if err == nil {
		t.Fatalf("PermanentDelete should refuse when branch is unmerged")
	}

	loaded, err := config.LoadTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("LoadTask after refused permanent delete: %v", err)
	}
	if loaded == nil {
		t.Fatalf("task YAML should still exist after refused permanent delete")
	}
}

// TestPermanentDeleteRefusesActiveTask makes sure the manager won't hard
// delete a task that hasn't been soft-deleted first — protecting against
// a UI bug that calls the RPC on the wrong row.
func TestPermanentDeleteRefusesActiveTask(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "still alive",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := m.PermanentDelete(projectPath, created.TaskNumber, nil); err == nil {
		t.Fatalf("PermanentDelete should refuse non-soft-deleted task")
	}
	if loaded, _ := config.LoadTask(projectPath, created.TaskNumber); loaded == nil {
		t.Fatalf("active task YAML should still exist after refused permanent delete")
	}
}

// TestPermanentDeleteRemovesYAMLAndMetrics verifies the happy path:
// when the branch check passes (or is nil) the YAML is removed and the
// optional `<n>.metrics.yaml` sibling is cleaned up alongside it.
func TestPermanentDeleteRemovesYAMLAndMetrics(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "trash me",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Drop a metrics sibling so we can confirm it gets cleaned up too.
	if err := config.WriteMetrics(projectPath, &models.TaskMetrics{TaskNumber: created.TaskNumber}); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}
	if !config.MetricsExists(projectPath, created.TaskNumber) {
		t.Fatalf("precondition: metrics sibling should exist on disk")
	}

	if _, err := m.DeleteTask(projectPath, created.TaskNumber); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if err := m.PermanentDelete(projectPath, created.TaskNumber, nil); err != nil {
		t.Fatalf("PermanentDelete: %v", err)
	}

	loaded, _ := config.LoadTask(projectPath, created.TaskNumber)
	if loaded != nil {
		t.Fatalf("expected task YAML to be removed; still loaded as %+v", loaded)
	}
	if config.MetricsExists(projectPath, created.TaskNumber) {
		t.Fatalf("expected metrics sibling to be removed alongside the task YAML")
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
