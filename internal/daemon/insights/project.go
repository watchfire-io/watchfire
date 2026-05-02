// Package insights — v6.0 Ember per-project rollup aggregator.
//
// Sibling of global.go: shares the same shape vocabulary (DayBucket /
// AgentBreakdown via the proto) but rolls only one project's tasks. The
// dashboard's per-project Insights tab and the TUI per-project overlay
// (key `i`) both read the cached output.
//
// Cost / token data still ride task 0056 (per-task metrics capture). Until
// that lands, every completed task is counted as "missing cost" so the GUI
// partial-data caveat fires.
package insights

import (
	"sort"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// ProjectInsights mirrors `watchfire.ProjectInsights` proto. The Go-side
// JSON tags double as the on-disk per-project cache schema.
type ProjectInsights struct {
	ProjectID   string    `json:"project_id"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	TasksTotal     int `json:"tasks_total"`
	TasksSucceeded int `json:"tasks_succeeded"`
	TasksFailed    int `json:"tasks_failed"`

	TasksByDay     []ProjectDayBucket `json:"tasks_by_day"`
	AgentBreakdown []ProjectAgentRow  `json:"agent_breakdown"`

	TotalDurationMs int64 `json:"total_duration_ms"`
	AvgDurationMs   int64 `json:"avg_duration_ms"`
	P50DurationMs   int64 `json:"p50_duration_ms"`
	P95DurationMs   int64 `json:"p95_duration_ms"`

	TotalCostUSD     float64 `json:"total_cost_usd"`
	TasksMissingCost int     `json:"tasks_missing_cost"`
}

// ProjectDayBucket — one calendar day in the per-project breakdown. Shape
// matches GlobalDayBucket so renderers can be shared.
type ProjectDayBucket struct {
	Date      string `json:"date"`
	Count     int    `json:"count"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
}

// ProjectAgentRow rolls one backend agent for a single project across the
// window.
type ProjectAgentRow struct {
	Agent          string  `json:"agent"`
	Count          int     `json:"count"`
	SuccessRate    float64 `json:"success_rate"`
	AvgDurationMs  int64   `json:"avg_duration_ms"`
	TotalTokensIn  int64   `json:"total_tokens_in"`
	TotalTokensOut int64   `json:"total_tokens_out"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
}

// LoadProjectInsights computes the per-project rollup. Reads the cache
// first and falls back to a fresh task scan on miss.
func LoadProjectInsights(projectID string, windowStart, windowEnd time.Time) (*ProjectInsights, error) {
	if cached, ok := readProjectCache(projectID, windowStart, windowEnd); ok {
		return cached, nil
	}
	out, err := computeProjectInsights(projectID, windowStart, windowEnd)
	if err != nil {
		return nil, err
	}
	writeProjectCache(out)
	return out, nil
}

// ComputeProjectInsightsForTasks is the testable seam — same idea as
// `ComputeGlobalInsightsForTasks`. Production wraps a disk read; tests
// pass a slice directly.
func ComputeProjectInsightsForTasks(
	projectID string,
	windowStart, windowEnd time.Time,
	tasks []*models.Task,
) *ProjectInsights {
	p := &ProjectInsights{
		ProjectID:   projectID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}

	dayDone := map[string]int{}
	dayFailed := map[string]int{}
	durationsByAgent := map[string][]int64{}
	doneByAgent := map[string]int{}
	failedByAgent := map[string]int{}
	var allDurationsMs []int64

	for _, t := range tasks {
		if t == nil || t.IsDeleted() {
			continue
		}
		if t.Status != models.TaskStatusDone || t.CompletedAt == nil {
			continue
		}
		if !inWindow(*t.CompletedAt, windowStart, windowEnd) {
			continue
		}
		key := bucketKey(*t.CompletedAt)
		agent := agentKey(t.Agent)
		p.TasksTotal++
		p.TasksMissingCost++ // task 0056 not yet wired — every row is missing cost
		if t.Success != nil && *t.Success {
			p.TasksSucceeded++
			dayDone[key]++
			doneByAgent[agent]++
		} else {
			p.TasksFailed++
			dayFailed[key]++
			failedByAgent[agent]++
		}
		if t.StartedAt != nil {
			ms := t.CompletedAt.Sub(*t.StartedAt).Milliseconds()
			if ms > 0 {
				p.TotalDurationMs += ms
				allDurationsMs = append(allDurationsMs, ms)
				durationsByAgent[agent] = append(durationsByAgent[agent], ms)
			}
		}
	}

	p.TasksByDay = mergeProjectDayBuckets(dayDone, dayFailed)
	p.AgentBreakdown = buildProjectAgentRows(doneByAgent, failedByAgent, durationsByAgent)
	if n := len(allDurationsMs); n > 0 {
		p.AvgDurationMs = p.TotalDurationMs / int64(n)
		p.P50DurationMs = percentileInt64(allDurationsMs, 50)
		p.P95DurationMs = percentileInt64(allDurationsMs, 95)
	}

	return p
}

func computeProjectInsights(projectID string, windowStart, windowEnd time.Time) (*ProjectInsights, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	if index == nil {
		return &ProjectInsights{ProjectID: projectID, WindowStart: windowStart, WindowEnd: windowEnd}, nil
	}
	entry := index.FindProject(projectID)
	if entry == nil {
		return &ProjectInsights{ProjectID: projectID, WindowStart: windowStart, WindowEnd: windowEnd}, nil
	}
	tasks, err := config.LoadAllTasks(entry.Path)
	if err != nil {
		return nil, err
	}
	return ComputeProjectInsightsForTasks(projectID, windowStart, windowEnd, tasks), nil
}

func mergeProjectDayBuckets(dayDone, dayFailed map[string]int) []ProjectDayBucket {
	keys := unionKeys(dayDone, dayFailed)
	sort.Strings(keys)
	out := make([]ProjectDayBucket, 0, len(keys))
	for _, k := range keys {
		s, f := dayDone[k], dayFailed[k]
		out = append(out, ProjectDayBucket{
			Date:      k,
			Count:     s + f,
			Succeeded: s,
			Failed:    f,
		})
	}
	return out
}

func buildProjectAgentRows(done, failed map[string]int, durations map[string][]int64) []ProjectAgentRow {
	all := unionKeys(done, failed)
	out := make([]ProjectAgentRow, 0, len(all))
	for _, k := range all {
		count := done[k] + failed[k]
		row := ProjectAgentRow{Agent: k, Count: count}
		if count > 0 {
			row.SuccessRate = float64(done[k]) / float64(count)
		}
		row.AvgDurationMs = averageInt64(durations[k])
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// percentileInt64 returns the p-th percentile of xs using nearest-rank.
// Sorts a copy to avoid mutating the caller's slice.
func percentileInt64(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	dup := make([]int64, len(xs))
	copy(dup, xs)
	sort.Slice(dup, func(i, j int) bool { return dup[i] < dup[j] })
	rank := (p * len(dup)) / 100
	if rank >= len(dup) {
		rank = len(dup) - 1
	}
	if rank < 0 {
		rank = 0
	}
	return dup[rank]
}
