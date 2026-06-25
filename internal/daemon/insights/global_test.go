package insights

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// boolPtr / timePtr — fixture helpers for *bool / *time.Time fields.
func boolPtr(b bool) *bool { return &b }
func timePtr(t time.Time) *time.Time { return &t }

// makeMetrics builds a v8.0 code-output metrics record for a task.
func makeMetrics(num int, agent string, commits, files, added, removed int, merged bool, kind models.MergeKind) *models.TaskMetrics {
	return &models.TaskMetrics{
		TaskNumber:   num,
		Agent:        agent,
		Commits:      commits,
		FilesChanged: files,
		LinesAdded:   added,
		LinesRemoved: removed,
		NetLines:     added - removed,
		Merged:       merged,
		MergeKind:    kind,
	}
}

// makeTask builds a "done" task ready to feed the aggregator.
func makeTask(num int, agent string, success bool, started, completed time.Time) *models.Task {
	return &models.Task{
		TaskNumber:  num,
		Title:       "fixture",
		Status:      models.TaskStatusDone,
		Success:     boolPtr(success),
		Agent:       agent,
		StartedAt:   timePtr(started),
		CompletedAt: timePtr(completed),
	}
}

func TestComputeGlobalInsights_FleetTotalsAndTopProjects(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }

	projects := []models.ProjectEntry{
		{ProjectID: "proj-a", Name: "alpha"},
		{ProjectID: "proj-b", Name: "bravo"},
		{ProjectID: "proj-c", Name: "charlie"},
	}
	tasksByProject := map[string][]*models.Task{
		"proj-a": {
			makeTask(1, "claude-code", true, day(2).Add(-10*time.Minute), day(2)),
			makeTask(2, "codex", true, day(3).Add(-5*time.Minute), day(3)),
			makeTask(3, "claude-code", false, day(3).Add(-1*time.Minute), day(3)),
		},
		"proj-b": {
			makeTask(1, "claude-code", true, day(2).Add(-3*time.Minute), day(2)),
		},
		"proj-c": {}, // empty — should not appear in top-projects
	}
	colors := map[string]string{"proj-a": "#ef4444", "proj-b": "#22c55e", "proj-c": "#3b82f6"}

	g := ComputeGlobalInsightsForTasks(
		day(1), day(30),
		projects,
		func(p models.ProjectEntry) []*models.Task { return tasksByProject[p.ProjectID] },
		func(p models.ProjectEntry) string { return colors[p.ProjectID] },
		nil,
	)

	if g.TasksTotal != 4 {
		t.Errorf("TasksTotal = %d, want 4", g.TasksTotal)
	}
	if g.TasksSucceeded != 3 {
		t.Errorf("TasksSucceeded = %d, want 3", g.TasksSucceeded)
	}
	if g.TasksFailed != 1 {
		t.Errorf("TasksFailed = %d, want 1", g.TasksFailed)
	}
	if g.TasksMissingCost != 4 { // task 0056 not yet wired
		t.Errorf("TasksMissingCost = %d, want 4", g.TasksMissingCost)
	}
	if g.TotalDurationMs <= 0 {
		t.Errorf("TotalDurationMs = %d, want > 0", g.TotalDurationMs)
	}

	// Top projects sorted by count desc; ties by name asc. Empty projects
	// excluded so proj-c should not appear.
	if got, want := len(g.TopProjects), 2; got != want {
		t.Fatalf("TopProjects len = %d, want %d", got, want)
	}
	if g.TopProjects[0].ProjectID != "proj-a" {
		t.Errorf("TopProjects[0] = %q, want proj-a", g.TopProjects[0].ProjectID)
	}
	if g.TopProjects[0].Count != 3 {
		t.Errorf("TopProjects[0].Count = %d, want 3", g.TopProjects[0].Count)
	}
	if g.TopProjects[0].ProjectColor != "#ef4444" {
		t.Errorf("TopProjects[0].ProjectColor = %q, want #ef4444", g.TopProjects[0].ProjectColor)
	}
	if g.TopProjects[1].ProjectID != "proj-b" {
		t.Errorf("TopProjects[1] = %q, want proj-b", g.TopProjects[1].ProjectID)
	}

	// Tasks-by-day sorted chronologically; days with 0 activity omitted.
	if len(g.TasksByDay) != 2 {
		t.Fatalf("TasksByDay len = %d, want 2", len(g.TasksByDay))
	}
	if g.TasksByDay[0].Date >= g.TasksByDay[1].Date {
		t.Errorf("TasksByDay not sorted: %v", g.TasksByDay)
	}

	// Agent breakdown — claude-code has 3 (2✓ 1✗), codex has 1 (1✓).
	if len(g.AgentBreakdown) != 2 {
		t.Fatalf("AgentBreakdown len = %d, want 2", len(g.AgentBreakdown))
	}
	if g.AgentBreakdown[0].Agent != "claude-code" {
		t.Errorf("AgentBreakdown[0] = %q, want claude-code", g.AgentBreakdown[0].Agent)
	}
	if g.AgentBreakdown[0].Count != 3 {
		t.Errorf("AgentBreakdown[0].Count = %d, want 3", g.AgentBreakdown[0].Count)
	}
	wantRate := 2.0 / 3.0
	if got := g.AgentBreakdown[0].SuccessRate; got < wantRate-1e-9 || got > wantRate+1e-9 {
		t.Errorf("AgentBreakdown[0].SuccessRate = %v, want %v", got, wantRate)
	}
}

func TestComputeGlobalInsights_CodeOutputRollup(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }

	projects := []models.ProjectEntry{
		{ProjectID: "proj-a", Name: "alpha"},
		{ProjectID: "proj-b", Name: "bravo"},
	}
	tasksByProject := map[string][]*models.Task{
		"proj-a": {
			makeTask(1, "claude-code", true, day(2).Add(-10*time.Minute), day(2)),
			makeTask(2, "claude-code", true, day(3).Add(-5*time.Minute), day(3)), // no metrics
		},
		"proj-b": {
			makeTask(1, "codex", true, day(2).Add(-3*time.Minute), day(2)),
		},
	}
	metricsByProject := map[string]map[int]*models.TaskMetrics{
		"proj-a": {1: makeMetrics(1, "claude-code", 2, 3, 80, 10, true, models.MergeKindSilent)},
		"proj-b": {1: makeMetrics(1, "codex", 1, 1, 40, 40, true, models.MergeKindAutoPR)},
	}

	g := ComputeGlobalInsightsForTasks(
		day(1), day(30),
		projects,
		func(p models.ProjectEntry) []*models.Task { return tasksByProject[p.ProjectID] },
		func(_ models.ProjectEntry) string { return "" },
		func(p models.ProjectEntry, t *models.Task) *models.TaskMetrics {
			return metricsByProject[p.ProjectID][t.TaskNumber]
		},
	)

	if g.TotalCommits != 3 {
		t.Errorf("TotalCommits = %d, want 3", g.TotalCommits)
	}
	if g.TotalFilesChanged != 4 {
		t.Errorf("TotalFilesChanged = %d, want 4", g.TotalFilesChanged)
	}
	if g.TotalLinesAdded != 120 || g.TotalLinesRemoved != 50 {
		t.Errorf("lines added/removed = %d/%d, want 120/50", g.TotalLinesAdded, g.TotalLinesRemoved)
	}
	if g.NetLines != 70 {
		t.Errorf("NetLines = %d, want 70", g.NetLines)
	}
	if g.TasksMerged != 2 {
		t.Errorf("TasksMerged = %d, want 2", g.TasksMerged)
	}
	if g.TasksViaPR != 1 {
		t.Errorf("TasksViaPR = %d, want 1", g.TasksViaPR)
	}
	if g.MetricsMissingCode != 1 {
		t.Errorf("MetricsMissingCode = %d, want 1 (proj-a task 2 has no metrics)", g.MetricsMissingCode)
	}

	// Agent rows carry commits + lines across the fleet.
	byAgent := map[string]GlobalAgentRow{}
	for _, r := range g.AgentBreakdown {
		byAgent[r.Agent] = r
	}
	if cc := byAgent["claude-code"]; cc.Commits != 2 || cc.LinesAdded != 80 || cc.LinesRemoved != 10 {
		t.Errorf("claude-code row = %d/%d/%d, want 2/80/10", cc.Commits, cc.LinesAdded, cc.LinesRemoved)
	}
	if cx := byAgent["codex"]; cx.Commits != 1 || cx.LinesAdded != 40 || cx.LinesRemoved != 40 {
		t.Errorf("codex row = %d/%d/%d, want 1/40/40", cx.Commits, cx.LinesAdded, cx.LinesRemoved)
	}
}

func TestComputeGlobalInsights_RespectsWindow(t *testing.T) {
	t.Parallel()
	in := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	out := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	projects := []models.ProjectEntry{{ProjectID: "p", Name: "p"}}
	tasksByProject := map[string][]*models.Task{
		"p": {
			makeTask(1, "claude-code", true, in.Add(-5*time.Minute), in),       // inside
			makeTask(2, "claude-code", true, out.Add(-5*time.Minute), out),     // outside
		},
	}
	g := ComputeGlobalInsightsForTasks(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		projects,
		func(p models.ProjectEntry) []*models.Task { return tasksByProject[p.ProjectID] },
		func(_ models.ProjectEntry) string { return "" },
		nil,
	)
	if g.TasksTotal != 1 {
		t.Errorf("TasksTotal = %d, want 1 (other task is outside window)", g.TasksTotal)
	}
}

func TestComputeGlobalInsights_TopProjectsCappedAt5(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

	var projects []models.ProjectEntry
	tasksByProject := map[string][]*models.Task{}
	for i := 0; i < 8; i++ {
		id := string(rune('a' + i))
		entry := models.ProjectEntry{ProjectID: id, Name: id}
		projects = append(projects, entry)
		// Higher i → more tasks, so the top 5 should be h, g, f, e, d.
		var tasks []*models.Task
		for j := 0; j < i+1; j++ {
			tasks = append(tasks, makeTask(j+1, "claude-code", true, day.Add(-time.Minute), day))
		}
		tasksByProject[id] = tasks
	}

	g := ComputeGlobalInsightsForTasks(
		time.Time{}, time.Time{},
		projects,
		func(p models.ProjectEntry) []*models.Task { return tasksByProject[p.ProjectID] },
		func(_ models.ProjectEntry) string { return "" },
		nil,
	)
	if got, want := len(g.TopProjects), MaxTopProjects; got != want {
		t.Errorf("TopProjects len = %d, want %d", got, want)
	}
	if g.TopProjects[0].ProjectID != "h" || g.TopProjects[4].ProjectID != "d" {
		t.Errorf("Top-5 ordering wrong: %+v", g.TopProjects)
	}
}

func TestCacheCascadeInvalidationDropsGlobal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	insightsCache := filepath.Join(tmp, ".watchfire", CacheDirName)
	if err := os.MkdirAll(insightsCache, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	g := &GlobalInsights{
		WindowStart: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		TasksTotal:  42,
	}
	writeGlobalCache(g)

	cachePath := filepath.Join(insightsCache, GlobalCacheFile)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected cache file at %s: %v", cachePath, err)
	}

	// Per-project invalidation cascades into the global cache file.
	InvalidateProjectCache("any-project-id")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("global cache should be removed after per-project invalidation; stat err = %v", err)
	}
}

func TestGlobalCacheRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	want := &GlobalInsights{
		WindowStart:    start,
		WindowEnd:      end,
		TasksTotal:     12,
		TasksSucceeded: 10,
		TasksFailed:    2,
	}
	writeGlobalCache(want)
	got, ok := readGlobalCache(start, end)
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if got.TasksTotal != want.TasksTotal || got.TasksSucceeded != want.TasksSucceeded {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}

	// Different window → cache miss, original entry still present.
	if _, ok := readGlobalCache(start.Add(time.Hour), end); ok {
		t.Errorf("expected cache miss for different window")
	}
}
