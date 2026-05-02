package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/metrics"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	"github.com/watchfire-io/watchfire/internal/models"
)

// TestHandleTaskChangedWritesMetrics drives the same code path the live
// fsnotify watcher hits in production: a task YAML transitions to
// status=done, server.handleTaskChanged runs, the metrics capture hook
// writes `<n>.metrics.yaml` next to the task file. We pre-stage a
// session log so the per-backend parser produces token + cost numbers
// instead of falling through to duration-only.
func TestHandleTaskChangedWritesMetrics(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Shrink the metrics capture wait so the test doesn't sit around
	// for the production 6s timeout.
	prevTotal, prevStep := metrics.CaptureWaitTotal, metrics.CaptureWaitStep
	metrics.CaptureWaitTotal = 2 * time.Second
	metrics.CaptureWaitStep = 50 * time.Millisecond
	t.Cleanup(func() {
		metrics.CaptureWaitTotal, metrics.CaptureWaitStep = prevTotal, prevStep
	})

	projectID := "proj-metrics-1"
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject(projectID, "Metrics Integration", projectPath)
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(projectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := config.SaveSettings(models.NewSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	taskNumber := 11
	started := time.Now().UTC().Add(-3 * time.Second)
	completed := time.Now().UTC()
	success := true
	tk := &models.Task{
		Version:     1,
		TaskID:      "metrics01",
		TaskNumber:  taskNumber,
		Title:       "metrics capture",
		Agent:       "claude-code",
		Status:      models.TaskStatusDone,
		Success:     &success,
		StartedAt:   &started,
		CompletedAt: &completed,
		CreatedAt:   started,
		UpdatedAt:   completed,
	}
	if err := config.SaveTask(projectPath, tk); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	// Pre-stage a session log under ~/.watchfire/logs/<projectID>/.
	if err := config.EnsureGlobalLogsDir(); err != nil {
		t.Fatalf("EnsureGlobalLogsDir: %v", err)
	}
	logsDir, err := config.GlobalLogsDir()
	if err != nil {
		t.Fatalf("GlobalLogsDir: %v", err)
	}
	projectLogsDir := filepath.Join(logsDir, projectID)
	if err := os.MkdirAll(projectLogsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	logName := "0011-0-2026-05-01T12-00-00.log"
	logBody := "agent ran fine\nTotal tokens: in=1,500 out=750, cost=$0.0125\n"
	if err := os.WriteFile(filepath.Join(projectLogsDir, logName), []byte(logBody), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	bus := notify.NewBus()
	srv := &Server{
		agentManager: agent.NewManager(),
		notifyBus:    bus,
	}

	srv.handleTaskChanged(watcher.Event{
		Type:       watcher.EventTaskChanged,
		ProjectID:  projectID,
		TaskNumber: taskNumber,
	})

	// The metrics goroutine waits up to CaptureWaitTotal for the session
	// log; the log already exists so the first poll succeeds. Allow a
	// generous window before failing.
	deadline := time.Now().Add(3 * time.Second)
	var got *models.TaskMetrics
	for time.Now().Before(deadline) {
		got, err = config.ReadMetrics(projectPath, taskNumber)
		if err != nil {
			t.Fatalf("ReadMetrics: %v", err)
		}
		if got != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("expected `<n>.metrics.yaml` to be written, but file is missing")
	}

	if got.TaskNumber != taskNumber {
		t.Errorf("TaskNumber=%d want %d", got.TaskNumber, taskNumber)
	}
	if got.ProjectID != projectID {
		t.Errorf("ProjectID=%q want %q", got.ProjectID, projectID)
	}
	if got.Agent != "claude-code" {
		t.Errorf("Agent=%q want claude-code", got.Agent)
	}
	if got.ExitReason != models.MetricsExitCompleted {
		t.Errorf("ExitReason=%q want completed", got.ExitReason)
	}
	if got.DurationMs <= 0 {
		t.Errorf("DurationMs=%d want >0", got.DurationMs)
	}
	if got.TokensIn == nil || *got.TokensIn != 1500 {
		t.Errorf("TokensIn=%v want 1500", got.TokensIn)
	}
	if got.TokensOut == nil || *got.TokensOut != 750 {
		t.Errorf("TokensOut=%v want 750", got.TokensOut)
	}
	if got.CostUSD == nil || *got.CostUSD != 0.0125 {
		t.Errorf("CostUSD=%v want 0.0125", got.CostUSD)
	}
}

// TestHandleTaskChangedDurationOnlyWhenNoLog covers the manual-edit path
// where no session log exists — the hook should still write metrics
// with duration + exit_reason populated, leaving token/cost nil.
func TestHandleTaskChangedDurationOnlyWhenNoLog(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	prevTotal, prevStep := metrics.CaptureWaitTotal, metrics.CaptureWaitStep
	metrics.CaptureWaitTotal = 200 * time.Millisecond
	metrics.CaptureWaitStep = 50 * time.Millisecond
	t.Cleanup(func() {
		metrics.CaptureWaitTotal, metrics.CaptureWaitStep = prevTotal, prevStep
	})

	projectID := "proj-metrics-2"
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject(projectID, "Manual Edit", projectPath)
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(projectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := config.SaveSettings(models.NewSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	taskNumber := 12
	started := time.Now().UTC().Add(-1 * time.Second)
	completed := time.Now().UTC()
	failed := false
	tk := &models.Task{
		Version:       1,
		TaskID:        "manual001",
		TaskNumber:    taskNumber,
		Title:         "manual fail",
		Agent:         "codex",
		Status:        models.TaskStatusDone,
		Success:       &failed,
		FailureReason: "user gave up",
		StartedAt:     &started,
		CompletedAt:   &completed,
		CreatedAt:     started,
		UpdatedAt:     completed,
	}
	if err := config.SaveTask(projectPath, tk); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	srv := &Server{
		agentManager: agent.NewManager(),
		notifyBus:    notify.NewBus(),
	}

	srv.handleTaskChanged(watcher.Event{
		Type:       watcher.EventTaskChanged,
		ProjectID:  projectID,
		TaskNumber: taskNumber,
	})

	// Wait for goroutine to finish (the wait window plus a little slack).
	deadline := time.Now().Add(2 * time.Second)
	var got *models.TaskMetrics
	for time.Now().Before(deadline) {
		got, _ = config.ReadMetrics(projectPath, taskNumber)
		if got != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("expected duration-only metrics file even without session log")
	}
	if got.ExitReason != models.MetricsExitFailed {
		t.Errorf("ExitReason=%q want failed", got.ExitReason)
	}
	if got.DurationMs <= 0 {
		t.Errorf("DurationMs=%d want >0", got.DurationMs)
	}
	if got.TokensIn != nil || got.TokensOut != nil || got.CostUSD != nil {
		t.Errorf("expected token/cost to be nil; got %+v", got)
	}
}
