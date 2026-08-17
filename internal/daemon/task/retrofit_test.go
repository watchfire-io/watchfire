package task

import (
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// addTask creates a task through the manager and optionally marks it done.
func addTask(t *testing.T, m *Manager, projectPath, title string, done bool) *models.Task {
	t.Helper()
	created, err := m.CreateTask(projectPath, CreateOptions{Title: title, Prompt: "p", Status: string(models.TaskStatusReady)})
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", title, err)
	}
	if done {
		created.MarkDone(true, "")
		if err := config.SaveTask(projectPath, created); err != nil {
			t.Fatalf("SaveTask(%s): %v", title, err)
		}
	}
	return created
}

func taskNumbers(tasks []*models.Task) []int {
	nums := make([]int, 0, len(tasks))
	for _, t := range tasks {
		nums = append(nums, t.TaskNumber)
	}
	return nums
}

func TestRetrofitFoldWindowAndWatermarkAdvance(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	addTask(t, m, projectPath, "one", true)    // #1 done
	addTask(t, m, projectPath, "two", true)    // #2 done
	addTask(t, m, projectPath, "three", false) // #3 ready

	window, err := m.RetrofitFoldWindow(projectPath)
	if err != nil {
		t.Fatalf("RetrofitFoldWindow: %v", err)
	}
	if got := taskNumbers(window); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("first window = %v, want [1 2]", got)
	}

	// First retrofit completes → watermark moves to the highest done task.
	watermark, err := m.AdvanceRetrofitWatermark(projectPath)
	if err != nil {
		t.Fatalf("AdvanceRetrofitWatermark: %v", err)
	}
	if watermark != 2 {
		t.Fatalf("watermark = %d, want 2", watermark)
	}
	p, err := config.LoadProject(projectPath)
	if err != nil || p.LastRetrofitTaskNumber != 2 {
		t.Fatalf("persisted watermark = %d (err %v), want 2", p.LastRetrofitTaskNumber, err)
	}

	// Second run folds ONLY tasks done since the first retrofit.
	addTask(t, m, projectPath, "four", true) // #4 done
	window, err = m.RetrofitFoldWindow(projectPath)
	if err != nil {
		t.Fatalf("RetrofitFoldWindow (2nd): %v", err)
	}
	if got := taskNumbers(window); len(got) != 1 || got[0] != 4 {
		t.Fatalf("second window = %v, want [4]", got)
	}

	// Advancing again with no new done tasks is a no-op.
	if wm, err := m.AdvanceRetrofitWatermark(projectPath); err != nil || wm != 4 {
		t.Fatalf("second advance = %d (err %v), want 4", wm, err)
	}
	if wm, err := m.AdvanceRetrofitWatermark(projectPath); err != nil || wm != 4 {
		t.Fatalf("no-op advance = %d (err %v), want 4", wm, err)
	}
}

func TestArchiveRetrofitTasksSelectsExactlyTheFoldedWindow(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	addTask(t, m, projectPath, "one", true)    // #1 done — folded
	addTask(t, m, projectPath, "two", true)    // #2 done — folded
	addTask(t, m, projectPath, "three", false) // #3 ready — must never be touched
	if _, err := m.AdvanceRetrofitWatermark(projectPath); err != nil {
		t.Fatalf("AdvanceRetrofitWatermark: %v", err)
	}
	// A task completed AFTER the retrofit — above the watermark, not folded.
	addTask(t, m, projectPath, "four", true) // #4 done

	candidates, err := m.RetrofitArchiveCandidates(projectPath)
	if err != nil {
		t.Fatalf("RetrofitArchiveCandidates: %v", err)
	}
	if got := taskNumbers(candidates); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("candidates = %v, want [1 2]", got)
	}

	archived, err := m.ArchiveRetrofitTasks(projectPath)
	if err != nil {
		t.Fatalf("ArchiveRetrofitTasks: %v", err)
	}
	if got := taskNumbers(archived); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("archived = %v, want [1 2]", got)
	}

	// Archived tasks are soft-deleted with the retrofit mark; #3 and #4 untouched.
	for _, n := range []int{1, 2} {
		tk, err := config.LoadTask(projectPath, n)
		if err != nil {
			t.Fatalf("LoadTask(%d): %v", n, err)
		}
		if !tk.IsDeleted() || !tk.RetrofitArchived {
			t.Errorf("task %d: deleted=%v retrofitArchived=%v, want true/true", n, tk.IsDeleted(), tk.RetrofitArchived)
		}
		if tk.HiddenFromInsights() {
			t.Errorf("task %d should still count in insights", n)
		}
	}
	for _, n := range []int{3, 4} {
		tk, err := config.LoadTask(projectPath, n)
		if err != nil {
			t.Fatalf("LoadTask(%d): %v", n, err)
		}
		if tk.IsDeleted() {
			t.Errorf("task %d must not be archived", n)
		}
	}

	// Idempotent: re-archiving finds nothing left in the window.
	again, err := m.ArchiveRetrofitTasks(projectPath)
	if err != nil {
		t.Fatalf("ArchiveRetrofitTasks (again): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second archive = %v, want empty", taskNumbers(again))
	}

	// Reversible: restore clears the retrofit mark and the deletion.
	restored, err := m.RestoreTask(projectPath, 1)
	if err != nil {
		t.Fatalf("RestoreTask: %v", err)
	}
	if restored.IsDeleted() || restored.RetrofitArchived {
		t.Errorf("restored task: deleted=%v retrofitArchived=%v, want false/false", restored.IsDeleted(), restored.RetrofitArchived)
	}
}
