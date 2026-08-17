package server

import (
	"os"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/metrics"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	"github.com/watchfire-io/watchfire/internal/models"
)

// TestHandleTaskChangedStampsCompletedAt covers the v9.1 fix: agents write
// `status: done` without timestamps (per the completion protocol), so the
// daemon must stamp completed_at on the first observed done transition —
// it is what insights rollups and duration metrics key on. The re-save
// fires a second watcher event; that pass must neither move the stamp nor
// emit a second TASK_FAILED notification.
func TestHandleTaskChangedStampsCompletedAt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Shrink the metrics capture wait so background goroutines don't
	// outlive the test by the production 6s timeout.
	prevTotal, prevStep := metrics.CaptureWaitTotal, metrics.CaptureWaitStep
	metrics.CaptureWaitTotal = 100 * time.Millisecond
	metrics.CaptureWaitStep = 20 * time.Millisecond
	t.Cleanup(func() {
		metrics.CaptureWaitTotal, metrics.CaptureWaitStep = prevTotal, prevStep
	})

	projectID := "proj-stamp-1"
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject(projectID, "Stamp Test", projectPath)
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(projectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := config.SaveSettings(models.NewSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// The agent's write: done + failed, no timestamps beyond started_at.
	taskNumber := 7
	started := time.Now().UTC().Add(-2 * time.Minute)
	success := false
	tk := &models.Task{
		Version:       1,
		TaskID:        "stamp001",
		TaskNumber:    taskNumber,
		Title:         "stamp completed_at",
		Agent:         "claude-code",
		Status:        models.TaskStatusDone,
		Success:       &success,
		FailureReason: "blocked on fixture",
		StartedAt:     &started,
		CreatedAt:     started,
		UpdatedAt:     started,
	}
	if err := config.SaveTask(projectPath, tk); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	bus := notify.NewBus()
	notifications, cancel := bus.Subscribe()
	defer cancel()

	srv := &Server{
		agentManager: agent.NewManager(),
		notifyBus:    bus,
	}
	event := watcher.Event{
		Type:       watcher.EventTaskChanged,
		ProjectID:  projectID,
		TaskNumber: taskNumber,
	}

	// Each handleTaskChanged call detaches a metrics.CaptureFromTask
	// goroutine that reads the CaptureWait globals the Cleanup above
	// restores — racing them. Capture's single WriteMetrics is its final
	// act, so the metrics file (re)appearing is an exact join point.
	metricsPath := config.MetricsFile(projectPath, taskNumber)
	waitForCapture := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if config.FileExists(metricsPath) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("metrics capture goroutine did not finish")
	}

	srv.handleTaskChanged(event)
	waitForCapture()
	if err := os.Remove(metricsPath); err != nil {
		t.Fatalf("reset metrics file: %v", err)
	}

	got, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		t.Fatalf("LoadTask after first event: %v", err)
	}
	if got.CompletedAt == nil {
		t.Fatal("completed_at not stamped on done transition")
	}
	stamped := *got.CompletedAt
	if time.Since(stamped) > time.Minute {
		t.Errorf("completed_at = %v, want ~now", stamped)
	}
	select {
	case n := <-notifications:
		if n.Kind != notify.KindTaskFailed {
			t.Errorf("notification kind = %v, want %v", n.Kind, notify.KindTaskFailed)
		}
	default:
		t.Error("expected a TASK_FAILED notification on the first done event")
	}

	// Second event — what our own re-save triggers via the watcher.
	srv.handleTaskChanged(event)
	waitForCapture()

	got, err = config.LoadTask(projectPath, taskNumber)
	if err != nil {
		t.Fatalf("LoadTask after second event: %v", err)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(stamped) {
		t.Errorf("completed_at moved on second event: %v -> %v", stamped, got.CompletedAt)
	}
	select {
	case n := <-notifications:
		t.Errorf("unexpected second notification: %+v", n)
	default:
	}
}
