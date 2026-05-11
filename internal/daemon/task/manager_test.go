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

// TestListTasksAscendingByPositionThenTaskNumber locks in the v7 canonical
// work order: agents pick the oldest ready task first (`start-all` and
// `wildfire` both consume `tasks[0]`). The sort is compound — position
// ASC primary, task_number ASC tiebreaker — so both legs need a fixture
// that exercises them. Before v7 the manager sorted strictly descending
// by task_number and `Position` was dead data, which made the wildfire
// next-task picker walk the queue backwards (4 → 3 → 2 → 1).
func TestListTasksAscendingByPositionThenTaskNumber(t *testing.T) {
	projectPath := setupTempProject(t)
	now := time.Now().UTC()

	// Three tasks chosen so both legs of the compound sort matter:
	//   (position=2, task_number=1)  → would be first under pure task_number ASC
	//   (position=1, task_number=2)
	//   (position=1, task_number=3)
	// Expected order: (1,2), (1,3), (2,1).
	fixtures := []struct {
		num, pos int
	}{
		{1, 2},
		{2, 1},
		{3, 1},
	}
	for _, f := range fixtures {
		task := &models.Task{
			Version:    1,
			TaskID:     "id",
			TaskNumber: f.num,
			Title:      "t",
			Prompt:     "p",
			Status:     models.TaskStatusReady,
			Position:   f.pos,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := config.SaveTask(projectPath, task); err != nil {
			t.Fatalf("SaveTask %d: %v", f.num, err)
		}
	}

	listed, err := NewManager().ListTasks(projectPath, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(listed))
	}

	want := []struct {
		num, pos int
	}{
		{2, 1},
		{3, 1},
		{1, 2},
	}
	for i, w := range want {
		got := listed[i]
		if got.TaskNumber != w.num || got.Position != w.pos {
			t.Errorf("listed[%d] = (num=%d, pos=%d), want (num=%d, pos=%d)",
				i, got.TaskNumber, got.Position, w.num, w.pos)
		}
	}
}

// TestCreateTaskDefaultsToBottomOfQueue covers the v7 CreateTask change:
// without an explicit Position, new tasks land at `max(active.position)+1`
// so a brand-new task never jumps ahead of tasks the user just reordered.
// Three legs:
//   - empty project → first task at Position=1
//   - dense 1,2,3   → next task at Position=4
//   - sparse max 10 → next task at Position=11
func TestCreateTaskDefaultsToBottomOfQueue(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	first, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "first",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask first: %v", err)
	}
	if first.Position != 1 {
		t.Errorf("first task Position = %d, want 1 (empty project)", first.Position)
	}

	// Dense queue: positions 1, 2, 3 already in place — next at 4.
	for i := 0; i < 2; i++ {
		if _, err := m.CreateTask(projectPath, CreateOptions{
			Title:  "filler",
			Prompt: "p",
			Status: string(models.TaskStatusReady),
		}); err != nil {
			t.Fatalf("CreateTask filler: %v", err)
		}
	}
	dense, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "dense",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask dense: %v", err)
	}
	if dense.Position != 4 {
		t.Errorf("dense Position = %d, want 4 (positions 1,2,3 occupied)", dense.Position)
	}

	// Sparse queue: bump one task's position to 10 — next new task at 11.
	pos10 := 10
	if _, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: dense.TaskNumber,
		Position:   &pos10,
	}); err != nil {
		t.Fatalf("UpdateTask Position=10: %v", err)
	}
	sparse, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "sparse",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask sparse: %v", err)
	}
	if sparse.Position != 11 {
		t.Errorf("sparse Position = %d, want 11 (max active position = 10)", sparse.Position)
	}

	// Explicit opts.Position still wins.
	explicit := 99
	override, err := m.CreateTask(projectPath, CreateOptions{
		Title:    "override",
		Prompt:   "p",
		Status:   string(models.TaskStatusReady),
		Position: &explicit,
	})
	if err != nil {
		t.Fatalf("CreateTask override: %v", err)
	}
	if override.Position != 99 {
		t.Errorf("override Position = %d, want 99 (explicit caller value)", override.Position)
	}
}

// TestStartAllPicksOldestReadyFirst is the v7 smoke test against the
// `start-all` / `wildfire` next-task picker contract: every consumer in
// `internal/daemon/server/server.go` (lines 154 / 178 / 202) takes
// `tasks[0]` from a ready-filtered ListTasks. Before v7 that was the
// newest task; after v7 it must be the oldest. Create four tasks in
// order, mark them ready, list with status filter, assert task 1 is
// first and task 4 is last.
func TestStartAllPicksOldestReadyFirst(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	for i := 1; i <= 4; i++ {
		if _, err := m.CreateTask(projectPath, CreateOptions{
			Title:  "t",
			Prompt: "p",
			Status: string(models.TaskStatusReady),
		}); err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}
	}

	ready := string(models.TaskStatusReady)
	listed, err := m.ListTasks(projectPath, ListOptions{Status: &ready})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(listed) != 4 {
		t.Fatalf("expected 4 ready tasks, got %d", len(listed))
	}
	for i, got := range listed {
		want := i + 1
		if got.TaskNumber != want {
			t.Errorf("listed[%d].TaskNumber = %d, want %d (start-all picks oldest first)", i, got.TaskNumber, want)
		}
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
