// Package insights — v6.0 Ember cross-project rollup aggregator.
//
// Splits cleanly from the v6.0 export package (csv.go / markdown.go /
// export.go) so the dashboard rollup card and the TUI fleet overlay can
// share one cached Go struct. The proto contract lives at
// `proto/watchfire.proto:GlobalInsights` and is constructed in
// `internal/daemon/server/insights_service.go:GetGlobalInsights`.
//
// Cache cascade: any per-project metrics change drops both the per-project
// cache (introduced in 0057) and the fleet `_global.json` cache. The
// invalidation entry points live in cache.go.
package insights

import (
	"sort"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// GlobalInsights mirrors `watchfire.GlobalInsights` proto. The Go-side
// JSON tags double as the on-disk cache schema.
type GlobalInsights struct {
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	TasksTotal     int `json:"tasks_total"`
	TasksSucceeded int `json:"tasks_succeeded"`
	TasksFailed    int `json:"tasks_failed"`

	TasksByDay     []GlobalDayBucket  `json:"tasks_by_day"`
	TopProjects    []GlobalTopProject `json:"top_projects"`
	AgentBreakdown []GlobalAgentRow   `json:"agent_breakdown"`

	TotalDurationMs  int64   `json:"total_duration_ms"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	TasksMissingCost int     `json:"tasks_missing_cost"`
}

// GlobalDayBucket — one calendar-day worth of completed-task counts.
type GlobalDayBucket struct {
	Date      string `json:"date"`
	Count     int    `json:"count"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
}

// GlobalTopProject is one row of the top-projects pill list. Sorted by
// Count desc, ties broken by name asc.
type GlobalTopProject struct {
	ProjectID    string  `json:"project_id"`
	ProjectName  string  `json:"project_name"`
	ProjectColor string  `json:"project_color"`
	Count        int     `json:"count"`
	SuccessRate  float64 `json:"success_rate"`
}

// GlobalAgentRow rolls one backend agent across every project in the window.
type GlobalAgentRow struct {
	Agent          string  `json:"agent"`
	Count          int     `json:"count"`
	SuccessRate    float64 `json:"success_rate"`
	AvgDurationMs  int64   `json:"avg_duration_ms"`
	TotalTokensIn  int64   `json:"total_tokens_in"`
	TotalTokensOut int64   `json:"total_tokens_out"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
}

// MaxTopProjects caps the rollup pill list. The dashboard renders a single
// row at ~64px tall — five pills already wraps awkwardly on narrow widths.
const MaxTopProjects = 5

// LoadGlobalInsights computes the fleet-wide rollup. Reads the cache first
// and falls back to a fresh fan-out across every registered project.
//
// Cost data is currently absent — task 0056 (per-task metrics capture) is
// the source of truth for token / cost numbers, and lands in a separate
// PR. Until then `TotalCostUSD` is 0 and every completed task counts as
// `tasks_missing_cost`, which is what the GUI partial-data caveat reads.
func LoadGlobalInsights(windowStart, windowEnd time.Time) (*GlobalInsights, error) {
	if cached, ok := readGlobalCache(windowStart, windowEnd); ok {
		return cached, nil
	}
	out, err := computeGlobalInsights(windowStart, windowEnd)
	if err != nil {
		return nil, err
	}
	writeGlobalCache(out)
	return out, nil
}

// ComputeGlobalInsightsForTasks is the testable seam — it takes a
// pre-fetched per-project task slice keyed by ProjectEntry, so unit tests
// don't have to write tasks to disk.
func ComputeGlobalInsightsForTasks(
	windowStart, windowEnd time.Time,
	projects []models.ProjectEntry,
	tasksFor func(p models.ProjectEntry) []*models.Task,
	colorFor func(p models.ProjectEntry) string,
) *GlobalInsights {
	g := &GlobalInsights{WindowStart: windowStart, WindowEnd: windowEnd}

	// Aggregate counters.
	dayDone := map[string]int{}
	dayFailed := map[string]int{}
	durationsByAgent := map[string][]int64{}
	doneByAgent := map[string]int{}
	failedByAgent := map[string]int{}

	var perProject []rollupProjTally

	for _, entry := range projects {
		tally := rollupProjTally{entry: entry, color: colorFor(entry)}
		tasks := tasksFor(entry)
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
			tally.count++
			g.TasksTotal++
			g.TasksMissingCost++ // task 0056 not yet wired — cost is always missing
			if t.Success != nil && *t.Success {
				g.TasksSucceeded++
				dayDone[key]++
				doneByAgent[agent]++
				tally.succeeded++
			} else {
				g.TasksFailed++
				dayFailed[key]++
				failedByAgent[agent]++
				tally.failed++
			}
			if t.StartedAt != nil {
				ms := t.CompletedAt.Sub(*t.StartedAt).Milliseconds()
				if ms > 0 {
					g.TotalDurationMs += ms
					durationsByAgent[agent] = append(durationsByAgent[agent], ms)
				}
			}
		}
		if tally.count > 0 {
			perProject = append(perProject, tally)
		}
	}

	g.TasksByDay = mergeDayBuckets(dayDone, dayFailed)
	g.AgentBreakdown = buildAgentRows(doneByAgent, failedByAgent, durationsByAgent)
	g.TopProjects = pickTopProjects(perProject)

	return g
}

func computeGlobalInsights(windowStart, windowEnd time.Time) (*GlobalInsights, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	if index == nil {
		return &GlobalInsights{WindowStart: windowStart, WindowEnd: windowEnd}, nil
	}

	tasksFor := func(p models.ProjectEntry) []*models.Task {
		tasks, terr := config.LoadAllTasks(p.Path)
		if terr != nil {
			return nil
		}
		return tasks
	}
	colorFor := func(p models.ProjectEntry) string {
		proj, lerr := config.LoadProject(p.Path)
		if lerr != nil || proj == nil {
			return ""
		}
		return proj.Color
	}

	return ComputeGlobalInsightsForTasks(windowStart, windowEnd, index.Projects, tasksFor, colorFor), nil
}

func mergeDayBuckets(dayDone, dayFailed map[string]int) []GlobalDayBucket {
	keys := unionKeys(dayDone, dayFailed)
	sort.Strings(keys)
	out := make([]GlobalDayBucket, 0, len(keys))
	for _, k := range keys {
		s, f := dayDone[k], dayFailed[k]
		out = append(out, GlobalDayBucket{
			Date:      k,
			Count:     s + f,
			Succeeded: s,
			Failed:    f,
		})
	}
	return out
}

func buildAgentRows(done, failed map[string]int, durations map[string][]int64) []GlobalAgentRow {
	all := unionKeys(done, failed)
	out := make([]GlobalAgentRow, 0, len(all))
	for _, k := range all {
		count := done[k] + failed[k]
		row := GlobalAgentRow{Agent: k, Count: count}
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

type rollupProjTally struct {
	entry     models.ProjectEntry
	color     string
	count     int
	succeeded int
	failed    int
}

func pickTopProjects(rows []rollupProjTally) []GlobalTopProject {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].entry.Name < rows[j].entry.Name
	})
	limit := MaxTopProjects
	if len(rows) < limit {
		limit = len(rows)
	}
	out := make([]GlobalTopProject, 0, limit)
	for i := 0; i < limit; i++ {
		r := rows[i]
		var rate float64
		if r.count > 0 {
			rate = float64(r.succeeded) / float64(r.count)
		}
		out = append(out, GlobalTopProject{
			ProjectID:    r.entry.ProjectID,
			ProjectName:  r.entry.Name,
			ProjectColor: r.color,
			Count:        r.count,
			SuccessRate:  rate,
		})
	}
	return out
}
