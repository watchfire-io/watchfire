package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

func newDoneTask(taskNumber int, agent string, success bool, durationMs int64) *models.Task {
	started := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	completed := started.Add(time.Duration(durationMs) * time.Millisecond)
	t := &models.Task{
		TaskNumber:  taskNumber,
		Agent:       agent,
		Status:      models.TaskStatusDone,
		Success:     &success,
		StartedAt:   &started,
		CompletedAt: &completed,
		UpdatedAt:   completed,
	}
	return t
}

func TestBuildBaseMetricsCompleted(t *testing.T) {
	tk := newDoneTask(5, "claude-code", true, 12_345)
	m := BuildBaseMetrics("proj-1", tk)
	if m == nil {
		t.Fatal("BuildBaseMetrics returned nil")
	}
	if m.TaskNumber != 5 {
		t.Errorf("TaskNumber=%d want 5", m.TaskNumber)
	}
	if m.ProjectID != "proj-1" {
		t.Errorf("ProjectID=%q", m.ProjectID)
	}
	if m.Agent != "claude-code" {
		t.Errorf("Agent=%q", m.Agent)
	}
	if m.DurationMs != 12_345 {
		t.Errorf("DurationMs=%d want 12345", m.DurationMs)
	}
	if m.ExitReason != models.MetricsExitCompleted {
		t.Errorf("ExitReason=%q want completed", m.ExitReason)
	}
	if m.TokensIn != nil || m.TokensOut != nil || m.CostUSD != nil {
		t.Errorf("BuildBaseMetrics must not pre-fill token/cost")
	}
}

func TestBuildBaseMetricsFailed(t *testing.T) {
	tk := newDoneTask(7, "codex", false, 1000)
	m := BuildBaseMetrics("proj", tk)
	if m.ExitReason != models.MetricsExitFailed {
		t.Errorf("ExitReason=%q want failed", m.ExitReason)
	}
}

func TestBuildBaseMetricsNoStartedAt(t *testing.T) {
	tk := &models.Task{TaskNumber: 1, Status: models.TaskStatusDone, UpdatedAt: time.Now().UTC()}
	success := true
	tk.Success = &success
	m := BuildBaseMetrics("p", tk)
	if m.DurationMs != 0 {
		t.Errorf("DurationMs=%d want 0 (no StartedAt)", m.DurationMs)
	}
}

func TestCaptureWritesMetricsWithParserOutput(t *testing.T) {
	dir := t.TempDir()
	// Need the project tasks dir to exist.
	if err := os.MkdirAll(config.ProjectTasksDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "session.log")
	if err := os.WriteFile(logPath, []byte("Total tokens: in=42 out=24, cost=$0.0099\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tk := newDoneTask(3, "claude-code", true, 5_000)
	Capture(dir, "proj-x", logPath, tk)

	got, err := config.ReadMetrics(dir, 3)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got == nil {
		t.Fatal("expected metrics file")
	}
	if got.DurationMs != 5_000 {
		t.Errorf("DurationMs=%d want 5000", got.DurationMs)
	}
	if got.TokensIn == nil || *got.TokensIn != 42 {
		t.Errorf("TokensIn=%v want 42", got.TokensIn)
	}
	if got.TokensOut == nil || *got.TokensOut != 24 {
		t.Errorf("TokensOut=%v want 24", got.TokensOut)
	}
	if got.CostUSD == nil || *got.CostUSD != 0.0099 {
		t.Errorf("CostUSD=%v want 0.0099", got.CostUSD)
	}
	if got.ExitReason != models.MetricsExitCompleted {
		t.Errorf("ExitReason=%q want completed", got.ExitReason)
	}
}

func TestCaptureDegradesToDurationOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(config.ProjectTasksDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty session log → parser falls back to all-nil but no error.
	logPath := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(logPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	tk := newDoneTask(9, "claude-code", true, 7_777)
	Capture(dir, "proj", logPath, tk)
	got, err := config.ReadMetrics(dir, 9)
	if err != nil || got == nil {
		t.Fatalf("expected metrics file, got %v err=%v", got, err)
	}
	if got.DurationMs != 7_777 {
		t.Errorf("DurationMs=%d want 7777", got.DurationMs)
	}
	if got.TokensIn != nil || got.TokensOut != nil || got.CostUSD != nil {
		t.Errorf("expected nil tokens/cost; got %+v", got)
	}
}

func TestCaptureNilTaskNoOp(t *testing.T) {
	dir := t.TempDir()
	Capture(dir, "p", "", nil)
	// Just ensure no panic and no file written.
	entries, _ := os.ReadDir(config.ProjectTasksDir(dir))
	if len(entries) != 0 {
		t.Errorf("unexpected files written for nil task: %v", entries)
	}
}
