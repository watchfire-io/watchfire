package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// setupTempProject writes a minimal project on disk + registers it in the
// global index, returning (manager, projectID, projectPath). HOME is
// redirected to a temp dir so the global index lives in a sandbox.
func setupTempProject(t *testing.T) (*Manager, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectPath := filepath.Join(home, "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := models.NewProject("test-project-id", "demo", projectPath)
	if err := config.SaveProject(projectPath, p); err != nil {
		t.Fatal(err)
	}
	if err := config.RegisterProject(p.ProjectID, p.Name, projectPath); err != nil {
		t.Fatal(err)
	}
	return NewManager(), p.ProjectID, projectPath
}

// TestUpdateProjectSandboxStatus verifies the v6 sandbox + status fields
// round-trip through UpdateProject.
func TestUpdateProjectSandboxStatus(t *testing.T) {
	mgr, id, path := setupTempProject(t)

	sb := "sandbox-exec"
	st := "archived"
	if _, err := mgr.UpdateProject(UpdateOptions{
		ProjectID: id,
		Sandbox:   &sb,
		Status:    &st,
	}); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	got, err := config.LoadProject(path)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if got.Sandbox != "sandbox-exec" {
		t.Errorf("Sandbox: got %q want sandbox-exec", got.Sandbox)
	}
	if got.Status != "archived" {
		t.Errorf("Status: got %q want archived", got.Status)
	}
}

// TestUpdateProjectNotificationsFullBlock confirms the v6 full-block
// override path: passing a Notifications struct replaces the on-disk
// block entirely, including event map + quiet-hours override pointer.
func TestUpdateProjectNotificationsFullBlock(t *testing.T) {
	mgr, id, path := setupTempProject(t)

	notif := models.ProjectNotifications{
		Muted:          false,
		OverrideEvents: true,
		Events: map[string]models.ProjectEventPref{
			"task_failed": {Enabled: false},
		},
		QuietHoursOverride: &models.QuietHoursConfig{
			Enabled: true,
			Start:   "23:00",
			End:     "07:00",
		},
	}
	if _, err := mgr.UpdateProject(UpdateOptions{
		ProjectID:     id,
		Notifications: &notif,
	}); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	got, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Notifications.OverrideEvents {
		t.Errorf("OverrideEvents not persisted")
	}
	if pref, ok := got.Notifications.Events["task_failed"]; !ok || pref.Enabled {
		t.Errorf("event override not persisted")
	}
	if got.Notifications.QuietHoursOverride == nil ||
		got.Notifications.QuietHoursOverride.Start != "23:00" {
		t.Errorf("QuietHoursOverride did not round-trip; got %v", got.Notifications.QuietHoursOverride)
	}
}

// TestRegenerateProjectID mints a new UUID, rewrites the file + index,
// and preserves position.
func TestRegenerateProjectID(t *testing.T) {
	mgr, id, path := setupTempProject(t)

	pwe, err := mgr.RegenerateProjectID(id)
	if err != nil {
		t.Fatalf("RegenerateProjectID: %v", err)
	}
	if pwe.Project.ProjectID == id {
		t.Errorf("expected new ID, got same %q", id)
	}

	// File on disk carries the new ID.
	got, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != pwe.Project.ProjectID {
		t.Errorf("disk ID mismatch: got %q want %q", got.ProjectID, pwe.Project.ProjectID)
	}

	// Index entry exists under new ID.
	idx, err := config.LoadProjectsIndex()
	if err != nil {
		t.Fatal(err)
	}
	if idx.FindProject(pwe.Project.ProjectID) == nil {
		t.Errorf("new ID not in global index")
	}
	if idx.FindProject(id) != nil {
		t.Errorf("old ID still in global index")
	}
}

// TestResetTaskNumberingNoTasks — no tasks on disk → next_task_number == 1.
func TestResetTaskNumberingNoTasks(t *testing.T) {
	mgr, id, _ := setupTempProject(t)

	pwe, err := mgr.ResetTaskNumbering(id)
	if err != nil {
		t.Fatalf("ResetTaskNumbering: %v", err)
	}
	if pwe.Project.NextTaskNumber != 1 {
		t.Errorf("zero-task reset should land on 1, got %d", pwe.Project.NextTaskNumber)
	}
}

// TestResetTaskNumberingPicksHighestPlus1 — the reset path must compute
// from the highest existing file, not from the current value.
func TestResetTaskNumberingPicksHighestPlus1(t *testing.T) {
	mgr, id, path := setupTempProject(t)

	// Drop two task files: 0003 + 0007. NextTaskNumber on the project
	// stays at 1, but ResetTaskNumbering should bump it to 8.
	tasksDir := filepath.Join(path, ".watchfire", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0003.yaml", "0007.yaml"} {
		if err := os.WriteFile(filepath.Join(tasksDir, n), []byte("task_number: 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pwe, err := mgr.ResetTaskNumbering(id)
	if err != nil {
		t.Fatalf("ResetTaskNumbering: %v", err)
	}
	if pwe.Project.NextTaskNumber != 8 {
		t.Errorf("expected next_task_number=8 (highest 7 + 1), got %d", pwe.Project.NextTaskNumber)
	}
}

// TestUnregisterProjectDropsFromIndex — local files survive, global
// index loses the entry.
func TestUnregisterProjectDropsFromIndex(t *testing.T) {
	mgr, id, path := setupTempProject(t)

	if err := mgr.UnregisterProject(id); err != nil {
		t.Fatalf("UnregisterProject: %v", err)
	}
	idx, err := config.LoadProjectsIndex()
	if err != nil {
		t.Fatal(err)
	}
	if idx.FindProject(id) != nil {
		t.Errorf("expected project removed from index")
	}
	// Local project.yaml stays.
	if _, err := os.Stat(config.ProjectFile(path)); err != nil {
		t.Errorf("local project.yaml should survive Unregister: %v", err)
	}
}
