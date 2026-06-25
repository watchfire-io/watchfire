package insights

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestComputeProjectInsights_TotalsAndAgentBreakdown(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }

	tasks := []*models.Task{
		makeTask(1, "claude-code", true, day(2).Add(-10*time.Minute), day(2)),
		makeTask(2, "codex", true, day(3).Add(-5*time.Minute), day(3)),
		makeTask(3, "claude-code", false, day(3).Add(-1*time.Minute), day(3)),
		// outside window — should not count
		makeTask(4, "claude-code", true, day(40).Add(-5*time.Minute), day(40)),
		// missing CompletedAt — should not count
		{TaskNumber: 5, Status: models.TaskStatusDone, Agent: "codex", Success: boolPtr(true)},
	}

	p := ComputeProjectInsightsForTasks(
		"proj-a", day(1), day(30),
		tasks,
		nil, // no metrics — code rollup stays zero, MetricsMissingCode counts all
	)

	if p.TasksTotal != 3 {
		t.Errorf("TasksTotal = %d, want 3", p.TasksTotal)
	}
	if p.TasksSucceeded != 2 || p.TasksFailed != 1 {
		t.Errorf("succeeded/failed = %d/%d, want 2/1", p.TasksSucceeded, p.TasksFailed)
	}
	if p.TasksMissingCost != 3 {
		t.Errorf("TasksMissingCost = %d, want 3 (every task counted while 0056 not landed)", p.TasksMissingCost)
	}
	if p.TotalDurationMs <= 0 || p.AvgDurationMs <= 0 {
		t.Errorf("expected positive total + avg duration, got %d / %d", p.TotalDurationMs, p.AvgDurationMs)
	}
	if p.P50DurationMs <= 0 || p.P95DurationMs < p.P50DurationMs {
		t.Errorf("expected p95 >= p50 > 0, got p50=%d p95=%d", p.P50DurationMs, p.P95DurationMs)
	}

	// Two day buckets (day-2 with 1 succeeded, day-3 with 1 succeeded + 1 failed).
	if len(p.TasksByDay) != 2 {
		t.Fatalf("TasksByDay len = %d, want 2", len(p.TasksByDay))
	}
	if p.TasksByDay[0].Date >= p.TasksByDay[1].Date {
		t.Errorf("TasksByDay not sorted: %v", p.TasksByDay)
	}

	// Agent breakdown — claude-code 2 (1✓ 1✗), codex 1 (1✓).
	if len(p.AgentBreakdown) != 2 {
		t.Fatalf("AgentBreakdown len = %d, want 2", len(p.AgentBreakdown))
	}
	if p.AgentBreakdown[0].Agent != "claude-code" {
		t.Errorf("AgentBreakdown[0] = %q, want claude-code", p.AgentBreakdown[0].Agent)
	}
	wantRate := 0.5
	if got := p.AgentBreakdown[0].SuccessRate; got < wantRate-1e-9 || got > wantRate+1e-9 {
		t.Errorf("claude-code success rate = %v, want %v", got, wantRate)
	}
}

func TestComputeProjectInsights_CodeOutputRollup(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }

	tasks := []*models.Task{
		makeTask(1, "claude-code", true, day(2).Add(-10*time.Minute), day(2)),
		makeTask(2, "codex", true, day(3).Add(-5*time.Minute), day(3)),
		makeTask(3, "claude-code", false, day(3).Add(-1*time.Minute), day(3)),
	}
	metrics := map[int]*models.TaskMetrics{
		1: makeMetrics(1, "claude-code", 3, 4, 100, 20, true, models.MergeKindSilent),
		2: makeMetrics(2, "codex", 1, 2, 50, 5, true, models.MergeKindAutoPR),
		// task 3 has no metrics → counted as missing code, contributes zero
	}

	p := ComputeProjectInsightsForTasks(
		"proj-a", day(1), day(30),
		tasks,
		func(t *models.Task) *models.TaskMetrics { return metrics[t.TaskNumber] },
	)

	if p.TotalCommits != 4 {
		t.Errorf("TotalCommits = %d, want 4", p.TotalCommits)
	}
	if p.TotalFilesChanged != 6 {
		t.Errorf("TotalFilesChanged = %d, want 6", p.TotalFilesChanged)
	}
	if p.TotalLinesAdded != 150 || p.TotalLinesRemoved != 25 {
		t.Errorf("lines added/removed = %d/%d, want 150/25", p.TotalLinesAdded, p.TotalLinesRemoved)
	}
	if p.NetLines != 125 {
		t.Errorf("NetLines = %d, want 125", p.NetLines)
	}
	if p.TasksMerged != 2 {
		t.Errorf("TasksMerged = %d, want 2", p.TasksMerged)
	}
	if p.TasksViaPR != 1 {
		t.Errorf("TasksViaPR = %d, want 1", p.TasksViaPR)
	}
	if p.MetricsMissingCode != 1 {
		t.Errorf("MetricsMissingCode = %d, want 1 (task 3 has no metrics)", p.MetricsMissingCode)
	}

	// Day buckets carry churn: day-2 has task 1 (100/20), day-3 has task 2
	// (50/5) plus task 3 (no metrics, zero).
	byDate := map[string]ProjectDayBucket{}
	for _, b := range p.TasksByDay {
		byDate[b.Date] = b
	}
	if d2 := byDate["2026-05-02"]; d2.LinesAdded != 100 || d2.LinesRemoved != 20 {
		t.Errorf("day-2 churn = %d/%d, want 100/20", d2.LinesAdded, d2.LinesRemoved)
	}
	if d3 := byDate["2026-05-03"]; d3.LinesAdded != 50 || d3.LinesRemoved != 5 {
		t.Errorf("day-3 churn = %d/%d, want 50/5", d3.LinesAdded, d3.LinesRemoved)
	}

	// Agent rows carry commits + lines: claude-code = task 1 only (task 3
	// has no metrics), codex = task 2.
	byAgent := map[string]ProjectAgentRow{}
	for _, r := range p.AgentBreakdown {
		byAgent[r.Agent] = r
	}
	if cc := byAgent["claude-code"]; cc.Commits != 3 || cc.LinesAdded != 100 || cc.LinesRemoved != 20 {
		t.Errorf("claude-code row commits/added/removed = %d/%d/%d, want 3/100/20", cc.Commits, cc.LinesAdded, cc.LinesRemoved)
	}
	if cx := byAgent["codex"]; cx.Commits != 1 || cx.LinesAdded != 50 || cx.LinesRemoved != 5 {
		t.Errorf("codex row commits/added/removed = %d/%d/%d, want 1/50/5", cx.Commits, cx.LinesAdded, cx.LinesRemoved)
	}
}

func TestComputeProjectInsights_AllMetricsMissing(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	tasks := []*models.Task{
		makeTask(1, "claude-code", true, day(2).Add(-time.Minute), day(2)),
		makeTask(2, "codex", true, day(3).Add(-time.Minute), day(3)),
	}

	// nil metricsFor and a closure returning nil both mean "no code data".
	for _, mf := range []func(*models.Task) *models.TaskMetrics{
		nil,
		func(*models.Task) *models.TaskMetrics { return nil },
	} {
		p := ComputeProjectInsightsForTasks("proj-a", day(1), day(30), tasks, mf)
		if p.MetricsMissingCode != 2 {
			t.Errorf("MetricsMissingCode = %d, want 2", p.MetricsMissingCode)
		}
		if p.TotalCommits != 0 || p.TotalLinesAdded != 0 || p.NetLines != 0 || p.TasksMerged != 0 {
			t.Errorf("expected zero code totals with no metrics, got commits=%d added=%d net=%d merged=%d",
				p.TotalCommits, p.TotalLinesAdded, p.NetLines, p.TasksMerged)
		}
	}
}

func TestComputeProjectInsights_EmptyAndDeleted(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	deleted := day(2)

	tasks := []*models.Task{
		{TaskNumber: 1, Status: models.TaskStatusDone, Agent: "claude-code", DeletedAt: &deleted, CompletedAt: timePtr(day(2))},
	}

	p := ComputeProjectInsightsForTasks("proj-empty", day(1), day(30), tasks, nil)
	if p.TasksTotal != 0 {
		t.Errorf("deleted task should be excluded; TasksTotal = %d", p.TasksTotal)
	}
}

func TestPercentileInt64_NearestRank(t *testing.T) {
	t.Parallel()
	xs := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentileInt64(xs, 50); got != 6 {
		t.Errorf("p50 = %d, want 6 (nearest-rank index 5)", got)
	}
	if got := percentileInt64(xs, 95); got != 10 {
		t.Errorf("p95 = %d, want 10", got)
	}
	if got := percentileInt64(nil, 95); got != 0 {
		t.Errorf("empty p95 = %d, want 0", got)
	}
}

func TestProjectCacheRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	want := &ProjectInsights{
		ProjectID:      "abcd1234",
		WindowStart:    start,
		WindowEnd:      end,
		TasksTotal:     12,
		TasksSucceeded: 10,
		TasksFailed:    2,
	}
	writeProjectCache(want)
	got, ok := readProjectCache("abcd1234", start, end)
	if !ok {
		t.Fatalf("expected per-project cache hit")
	}
	if got.TasksTotal != want.TasksTotal {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}

	// Different project → cache miss.
	if _, ok := readProjectCache("zzzz9999", start, end); ok {
		t.Errorf("expected cache miss for different project")
	}
}

func TestProjectCacheCascadeInvalidatesGlobal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	insightsCache := filepath.Join(tmp, ".watchfire", CacheDirName)
	if err := os.MkdirAll(insightsCache, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Seed both caches.
	gp := &GlobalInsights{TasksTotal: 1}
	writeGlobalCache(gp)
	pp := &ProjectInsights{ProjectID: "abcd1234", TasksTotal: 1}
	writeProjectCache(pp)

	gpath := filepath.Join(insightsCache, GlobalCacheFile)
	ppath := filepath.Join(insightsCache, "abcd1234.json")
	if _, err := os.Stat(gpath); err != nil {
		t.Fatalf("global cache should exist: %v", err)
	}
	if _, err := os.Stat(ppath); err != nil {
		t.Fatalf("project cache should exist: %v", err)
	}

	InvalidateProjectCache("abcd1234")
	if _, err := os.Stat(gpath); !os.IsNotExist(err) {
		t.Errorf("global cache should be removed after per-project invalidation; stat err = %v", err)
	}
	if _, err := os.Stat(ppath); !os.IsNotExist(err) {
		t.Errorf("project cache should be removed after per-project invalidation; stat err = %v", err)
	}
}

// BenchmarkProjectInsights covers both the cold (compute from disk-shaped
// task slice) and hot (cache-hit) paths. Performance budget: cold <50ms /
// 1000 tasks; cache hit <5ms.
func BenchmarkProjectInsights(b *testing.B) {
	day := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	tasks := make([]*models.Task, 1000)
	for i := range tasks {
		agent := "claude-code"
		if i%4 == 0 {
			agent = "codex"
		}
		success := i%5 != 0
		t := makeTask(i+1, agent, success, day.Add(-time.Duration(i)*time.Minute), day.Add(-time.Duration(i-1)*time.Minute))
		tasks[i] = t
	}

	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = ComputeProjectInsightsForTasks("proj-x", time.Time{}, time.Time{}, tasks, nil)
		}
	})

	b.Run("cache-hit", func(b *testing.B) {
		tmp := b.TempDir()
		b.Setenv("HOME", tmp)
		seeded := ComputeProjectInsightsForTasks("proj-x", time.Time{}, time.Time{}, tasks, nil)
		writeProjectCache(seeded)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = readProjectCache("proj-x", time.Time{}, time.Time{})
		}
	})
}
