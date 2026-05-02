package insights

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// TestBuildProjectData walks the production aggregation path with a hand-rolled
// task list. The renderer + stats split means the (data → bytes) leg has its
// own golden tests; this test covers the (tasks → ProjectData) leg, which is
// the part that touches *models.Task semantics (in-window vs out-of-window
// completions, created vs in-flight bookkeeping, agent rollups).
func TestBuildProjectData(t *testing.T) {
	windowEnd := time.Date(2026, 5, 2, 23, 59, 59, 0, time.UTC)
	windowStart := windowEnd.AddDate(0, 0, -7)

	mkTime := func(day int, hour int) time.Time {
		return time.Date(2026, 4, day, hour, 0, 0, 0, time.UTC)
	}
	timePtr := func(t time.Time) *time.Time { return &t }
	bptr := func(b bool) *bool { return &b }

	tasks := []*models.Task{
		// In-window done success — counted in done + day + agent + duration.
		{TaskNumber: 1, Status: models.TaskStatusDone, Success: bptr(true), Agent: "claude-code",
			CreatedAt: mkTime(28, 8), StartedAt: timePtr(mkTime(28, 9)), CompletedAt: timePtr(mkTime(28, 10))},
		// In-window done failed — counted in failed + day + agent.
		{TaskNumber: 2, Status: models.TaskStatusDone, Success: bptr(false), Agent: "codex",
			CreatedAt: mkTime(28, 8), StartedAt: timePtr(mkTime(29, 9)), CompletedAt: timePtr(mkTime(29, 10))},
		// Out-of-window completion — KPIs ignore it but in-flight is N/A
		// because it's done.
		{TaskNumber: 3, Status: models.TaskStatusDone, Success: bptr(true), Agent: "claude-code",
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			StartedAt: timePtr(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)),
			CompletedAt: timePtr(time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC))},
		// In-flight ready — instantaneous, contributes to TotalInFlight only.
		{TaskNumber: 4, Status: models.TaskStatusReady, Agent: "claude-code", CreatedAt: mkTime(30, 12)},
		// Empty agent → bucketed as "(default)".
		{TaskNumber: 5, Status: models.TaskStatusDone, Success: bptr(true),
			CreatedAt: mkTime(30, 8), StartedAt: timePtr(mkTime(30, 9)), CompletedAt: timePtr(mkTime(30, 11))},
		// Soft-deleted — entirely skipped.
		{TaskNumber: 6, Status: models.TaskStatusDone, Success: bptr(true), Agent: "claude-code",
			CreatedAt:   mkTime(30, 8),
			StartedAt:   timePtr(mkTime(30, 9)),
			CompletedAt: timePtr(mkTime(30, 11)),
			DeletedAt:   timePtr(mkTime(30, 12))},
	}

	pd := buildProjectData("p", "Project", tasks, windowStart, windowEnd)

	// Tasks 1 + 5 succeeded in window → TotalDone == 2; task 2 failed in
	// window → TotalFailed == 1; task 3 is out of window; task 6 soft-
	// deleted; task 4 still ready.
	if pd.KPIs.TotalDone != 2 {
		t.Errorf("TotalDone = %d want 2", pd.KPIs.TotalDone)
	}
	if pd.KPIs.TotalFailed != 1 {
		t.Errorf("TotalFailed = %d want 1", pd.KPIs.TotalFailed)
	}
	if pd.KPIs.TotalInFlight != 1 {
		t.Errorf("TotalInFlight = %d want 1", pd.KPIs.TotalInFlight)
	}
	// Created counts in-window only — tasks 1, 2, 4, 5 (task 3 is out-of-window,
	// task 6 deleted).
	if pd.KPIs.TotalCreated != 4 {
		t.Errorf("TotalCreated = %d want 4", pd.KPIs.TotalCreated)
	}

	// Three agents in window — "(default)" (no agent on task 5),
	// "claude-code" (task 1), "codex" (task 2). All tied at 1 task, so the
	// alphabetical tiebreak puts "(default)" first.
	if len(pd.Agents) != 3 {
		t.Fatalf("Agents = %d rows, want 3", len(pd.Agents))
	}
	if pd.Agents[0].Agent != "(default)" {
		t.Errorf("first agent = %+v", pd.Agents[0])
	}

	// Daily buckets — one row per day with activity.
	if len(pd.Daily) == 0 {
		t.Fatal("Daily empty")
	}
}
