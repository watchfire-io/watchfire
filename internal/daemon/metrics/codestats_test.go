package metrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// TestRecordCodeStatsSerializes asserts the v8.0 code-output fields round-trip
// through `<n>.metrics.yaml` and that net_lines is derived from added−removed.
func TestRecordCodeStatsSerializes(t *testing.T) {
	dir := t.TempDir()
	tk := newDoneTask(11, "claude-code", true, 5_000)

	RecordCodeStats(dir, "proj-x", tk, CodeStats{
		Commits:      3,
		FilesChanged: 4,
		LinesAdded:   120,
		LinesRemoved: 45,
		Merged:       true,
		MergeKind:    models.MergeKindSilent,
	})

	got, err := config.ReadMetrics(dir, 11)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got == nil {
		t.Fatal("ReadMetrics returned nil after RecordCodeStats")
	}
	if got.Commits != 3 {
		t.Errorf("Commits=%d want 3", got.Commits)
	}
	if got.FilesChanged != 4 {
		t.Errorf("FilesChanged=%d want 4", got.FilesChanged)
	}
	if got.LinesAdded != 120 {
		t.Errorf("LinesAdded=%d want 120", got.LinesAdded)
	}
	if got.LinesRemoved != 45 {
		t.Errorf("LinesRemoved=%d want 45", got.LinesRemoved)
	}
	if got.NetLines != 75 {
		t.Errorf("NetLines=%d want 75 (120-45)", got.NetLines)
	}
	if !got.Merged {
		t.Error("Merged=false want true")
	}
	if got.MergeKind != models.MergeKindSilent {
		t.Errorf("MergeKind=%q want %q", got.MergeKind, models.MergeKindSilent)
	}
	// Base fields the merge path inferred from the task should still be set.
	if got.TaskNumber != 11 || got.ProjectID != "proj-x" || got.Agent != "claude-code" {
		t.Errorf("base fields not populated: %+v", got)
	}
}

// TestRecordCodeStatsPreservesTokens asserts the merge-path writer does not
// clobber token/cost fields a prior token Capture already persisted, and that
// a later Capture preserves the code fields in turn (the concurrent both-ways
// merge the shared mutex protects).
func TestRecordCodeStatsPreservesTokens(t *testing.T) {
	dir := t.TempDir()
	tk := newDoneTask(12, "codex", true, 9_000)

	// 1. Token capture lands first (no session log → duration-only + we add
	//    tokens by hand to mimic a parser hit).
	in, out, cost := int64(1000), int64(2000), 0.05
	base := BuildBaseMetrics("proj-y", tk)
	base.TokensIn, base.TokensOut, base.CostUSD = &in, &out, &cost
	if err := config.WriteMetrics(dir, base); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}

	// 2. Merge path records code stats — must keep the tokens.
	RecordCodeStats(dir, "proj-y", tk, CodeStats{
		Commits:      2,
		FilesChanged: 1,
		LinesAdded:   10,
		LinesRemoved: 2,
		Merged:       true,
		MergeKind:    models.MergeKindAutoPR,
	})

	got, err := config.ReadMetrics(dir, 12)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got.TokensIn == nil || *got.TokensIn != in || got.TokensOut == nil || *got.TokensOut != out {
		t.Errorf("tokens clobbered: in=%v out=%v", got.TokensIn, got.TokensOut)
	}
	if got.Commits != 2 || got.NetLines != 8 || got.MergeKind != models.MergeKindAutoPR {
		t.Errorf("code stats not recorded: %+v", got)
	}

	// 3. A later token Capture (e.g. session log finally landed) must keep the
	//    code fields written in step 2.
	Capture(dir, "proj-y", "", tk)
	got2, err := config.ReadMetrics(dir, 12)
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got2.Commits != 2 || got2.FilesChanged != 1 || got2.NetLines != 8 || got2.MergeKind != models.MergeKindAutoPR {
		t.Errorf("Capture clobbered code stats: %+v", got2)
	}
}

// TestReadMetricsBackwardCompatNoCodeFields asserts an old metrics YAML written
// before v8.0 (no commits/files/lines/merge keys) loads with those fields as
// their zero values rather than erroring.
func TestReadMetricsBackwardCompatNoCodeFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(config.ProjectTasksDir(dir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-v8.0 metrics file: only the v6.0 Ember fields.
	legacy := "" +
		"task_number: 9\n" +
		"project_id: legacy\n" +
		"agent: gemini\n" +
		"duration_ms: 3000\n" +
		"exit_reason: completed\n" +
		"captured_at: 2026-05-01T10:00:00Z\n"
	path := filepath.Join(config.ProjectTasksDir(dir), "0009.metrics.yaml")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	got, err := config.ReadMetrics(dir, 9)
	if err != nil {
		t.Fatalf("ReadMetrics on legacy file: %v", err)
	}
	if got == nil {
		t.Fatal("ReadMetrics returned nil for legacy file")
	}
	if got.TaskNumber != 9 || got.Agent != "gemini" || got.DurationMs != 3000 {
		t.Errorf("legacy base fields wrong: %+v", got)
	}
	if got.Commits != 0 || got.FilesChanged != 0 || got.LinesAdded != 0 ||
		got.LinesRemoved != 0 || got.NetLines != 0 || got.Merged || got.MergeKind != "" {
		t.Errorf("legacy code fields should be zero, got %+v", got)
	}
}
