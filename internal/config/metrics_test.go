package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestWriteAndReadMetricsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	in := int64(1234)
	out := int64(5678)
	cost := 0.0421
	captured := time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)

	want := &models.TaskMetrics{
		TaskNumber: 7,
		ProjectID:  "abc-123",
		Agent:      "claude-code",
		DurationMs: 42_000,
		TokensIn:   &in,
		TokensOut:  &out,
		CostUSD:    &cost,
		ExitReason: models.MetricsExitCompleted,
		CapturedAt: captured,
	}

	if err := WriteMetrics(dir, want); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}

	expectedPath := filepath.Join(ProjectTasksDir(dir), "0007.metrics.yaml")
	if !FileExists(expectedPath) {
		t.Fatalf("metrics file missing at %s", expectedPath)
	}

	got, err := ReadMetrics(dir, 7)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got == nil {
		t.Fatal("ReadMetrics returned nil")
	}
	if got.TaskNumber != want.TaskNumber {
		t.Errorf("TaskNumber=%d want %d", got.TaskNumber, want.TaskNumber)
	}
	if got.ProjectID != want.ProjectID {
		t.Errorf("ProjectID=%q want %q", got.ProjectID, want.ProjectID)
	}
	if got.Agent != want.Agent {
		t.Errorf("Agent=%q want %q", got.Agent, want.Agent)
	}
	if got.DurationMs != want.DurationMs {
		t.Errorf("DurationMs=%d want %d", got.DurationMs, want.DurationMs)
	}
	if got.TokensIn == nil || *got.TokensIn != in {
		t.Errorf("TokensIn=%v want %d", got.TokensIn, in)
	}
	if got.TokensOut == nil || *got.TokensOut != out {
		t.Errorf("TokensOut=%v want %d", got.TokensOut, out)
	}
	if got.CostUSD == nil || *got.CostUSD != cost {
		t.Errorf("CostUSD=%v want %f", got.CostUSD, cost)
	}
	if got.ExitReason != models.MetricsExitCompleted {
		t.Errorf("ExitReason=%q want %q", got.ExitReason, models.MetricsExitCompleted)
	}
	if !got.CapturedAt.Equal(captured) {
		t.Errorf("CapturedAt=%v want %v", got.CapturedAt, captured)
	}
}

func TestReadMetricsMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadMetrics(dir, 99)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestWriteMetricsNilFails(t *testing.T) {
	if err := WriteMetrics(t.TempDir(), nil); err == nil {
		t.Error("expected error for nil metrics")
	}
}

func TestWriteMetricsZeroTaskNumberFails(t *testing.T) {
	if err := WriteMetrics(t.TempDir(), &models.TaskMetrics{}); err == nil {
		t.Error("expected error for zero task_number")
	}
}

func TestWriteMetricsOmitsNilOptionalFields(t *testing.T) {
	dir := t.TempDir()
	m := &models.TaskMetrics{
		TaskNumber: 3,
		ProjectID:  "p",
		Agent:      "codex",
		DurationMs: 1000,
		ExitReason: models.MetricsExitFailed,
		CapturedAt: time.Now().UTC(),
	}
	if err := WriteMetrics(dir, m); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}
	got, err := ReadMetrics(dir, 3)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got.TokensIn != nil {
		t.Errorf("TokensIn should be nil, got %v", got.TokensIn)
	}
	if got.TokensOut != nil {
		t.Errorf("TokensOut should be nil, got %v", got.TokensOut)
	}
	if got.CostUSD != nil {
		t.Errorf("CostUSD should be nil, got %v", got.CostUSD)
	}
}

func TestMetricsExists(t *testing.T) {
	dir := t.TempDir()
	if MetricsExists(dir, 1) {
		t.Error("MetricsExists should be false before write")
	}
	m := &models.TaskMetrics{TaskNumber: 1, ProjectID: "p", Agent: "x", CapturedAt: time.Now().UTC()}
	if err := WriteMetrics(dir, m); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}
	if !MetricsExists(dir, 1) {
		t.Error("MetricsExists should be true after write")
	}
}
