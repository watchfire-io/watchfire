package server

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// failedTask returns a fresh done+failed task fixture for the gate tests.
func failedTask() *models.Task {
	f := false
	return &models.Task{
		Version:       1,
		TaskNumber:    7,
		Title:         "broken thing",
		Status:        models.TaskStatusDone,
		Success:       &f,
		FailureReason: "ran out of context",
	}
}

// withTempHome stages an isolated $HOME so SaveSettings / SaveProject /
// AppendLogLine all touch a temp dir, never the real ~/.watchfire/.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// withProjectAndSettings writes a settings.yaml + project.yaml so emitTaskFailed
// can read both. project.yaml lives at <tmp>/proj/.watchfire/project.yaml.
func withProjectAndSettings(t *testing.T, settings *models.Settings, projectMuted bool) (projectPath, projectID string) {
	t.Helper()
	if err := config.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	tmp := t.TempDir()
	projectID = "proj-test-1"
	if err := config.EnsureProjectDir(tmp); err != nil {
		t.Fatalf("ensure project dir: %v", err)
	}
	proj := models.NewProject(projectID, "Test Project", tmp)
	proj.Notifications.Muted = projectMuted
	if err := config.SaveProject(tmp, proj); err != nil {
		t.Fatalf("save project: %v", err)
	}
	return tmp, projectID
}

// drainOne reads one notification with a short timeout. Returns nil on timeout.
func drainOne(t *testing.T, ch <-chan notify.Notification) *notify.Notification {
	t.Helper()
	select {
	case n := <-ch:
		return &n
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

func TestEmitTaskFailedHappyPath(t *testing.T) {
	withTempHome(t)
	projectPath, projectID := withProjectAndSettings(t, models.NewSettings(), false)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	emitTaskFailed(bus, projectID, projectPath, "Test Project", failedTask())

	n := drainOne(t, ch)
	if n == nil {
		t.Fatal("expected a TASK_FAILED notification")
	}
	if n.Kind != notify.KindTaskFailed {
		t.Fatalf("kind: got %v, want TASK_FAILED", n.Kind)
	}
	if n.ProjectID != projectID {
		t.Fatalf("project id mismatch: %q", n.ProjectID)
	}
	if n.TaskNumber != 7 {
		t.Fatalf("task number: got %d, want 7", n.TaskNumber)
	}
}

func TestEmitTaskFailedSkipsNonDoneStatus(t *testing.T) {
	withTempHome(t)
	projectPath, projectID := withProjectAndSettings(t, models.NewSettings(), false)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	tk := failedTask()
	tk.Status = models.TaskStatusReady
	emitTaskFailed(bus, projectID, projectPath, "Test Project", tk)

	if n := drainOne(t, ch); n != nil {
		t.Fatalf("expected no notification for non-done status, got %+v", n)
	}
}

func TestEmitTaskFailedSkipsSuccessfulTask(t *testing.T) {
	withTempHome(t)
	projectPath, projectID := withProjectAndSettings(t, models.NewSettings(), false)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	tk := failedTask()
	tr := true
	tk.Success = &tr
	emitTaskFailed(bus, projectID, projectPath, "Test Project", tk)

	if n := drainOne(t, ch); n != nil {
		t.Fatalf("expected no notification for success=true task, got %+v", n)
	}
}

func TestEmitTaskFailedSkipsWhenMasterToggleOff(t *testing.T) {
	withTempHome(t)
	settings := models.NewSettings()
	settings.Defaults.Notifications.Enabled = false
	projectPath, projectID := withProjectAndSettings(t, settings, false)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	emitTaskFailed(bus, projectID, projectPath, "Test Project", failedTask())

	if n := drainOne(t, ch); n != nil {
		t.Fatalf("expected no notification when master toggle off, got %+v", n)
	}
}

func TestEmitTaskFailedSkipsWhenPerEventToggleOff(t *testing.T) {
	withTempHome(t)
	settings := models.NewSettings()
	settings.Defaults.Notifications.Events.TaskFailed = false
	projectPath, projectID := withProjectAndSettings(t, settings, false)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	emitTaskFailed(bus, projectID, projectPath, "Test Project", failedTask())

	if n := drainOne(t, ch); n != nil {
		t.Fatalf("expected no notification when task_failed event toggle off, got %+v", n)
	}
}

func TestEmitTaskFailedSkipsWhenProjectMuted(t *testing.T) {
	withTempHome(t)
	projectPath, projectID := withProjectAndSettings(t, models.NewSettings(), true)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	emitTaskFailed(bus, projectID, projectPath, "Test Project", failedTask())

	if n := drainOne(t, ch); n != nil {
		t.Fatalf("expected no notification when project muted, got %+v", n)
	}
}

func TestEmitTaskFailedSkipsDuringQuietHours(t *testing.T) {
	withTempHome(t)
	settings := models.NewSettings()
	// Configure quiet hours covering "all day" so any wall-clock test run
	// falls inside the window. Wrap around midnight: 00:00 → 23:59.
	settings.Defaults.Notifications.QuietHours.Enabled = true
	settings.Defaults.Notifications.QuietHours.Start = "00:00"
	settings.Defaults.Notifications.QuietHours.End = "23:59"
	projectPath, projectID := withProjectAndSettings(t, settings, false)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	emitTaskFailed(bus, projectID, projectPath, "Test Project", failedTask())

	// We can't pin "now" in this gate path, but the only excluded minute is
	// 23:59 — overwhelmingly likely we're not in that single minute. If the
	// test happens to run at 23:59 local, a retry inside that minute won't
	// help; we accept the 1/1440 flakiness as the cost of not threading a
	// clock through the production path.
	now := time.Now().Local()
	if now.Hour() == 23 && now.Minute() == 59 {
		t.Skip("skipping at 23:59 local — quiet-hours window excludes this minute")
	}
	if n := drainOne(t, ch); n != nil {
		t.Fatalf("expected no notification during quiet hours, got %+v", n)
	}
}
