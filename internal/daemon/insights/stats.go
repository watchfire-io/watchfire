package insights

import (
	"sort"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// windowStats accumulates per-task counters into the shapes a report
// renders. Two boundaries: window-scoped counters (done / failed / created
// / per-day / per-agent) and instantaneous counters (in-flight) — see
// kpis().
type windowStats struct {
	start, end time.Time

	totalDone     int
	totalFailed   int
	totalCreated  int
	totalInFlight int

	durationsSec []int64

	dayDone    map[string]int
	dayFailed  map[string]int
	dayCreated map[string]int

	agentTasks    map[string]int
	agentDone     map[string]int
	agentFailed   map[string]int
	agentDuration map[string][]int64
}

func newWindowStats(start, end time.Time) *windowStats {
	return &windowStats{
		start:         start,
		end:           end,
		dayDone:       map[string]int{},
		dayFailed:     map[string]int{},
		dayCreated:    map[string]int{},
		agentTasks:    map[string]int{},
		agentDone:     map[string]int{},
		agentFailed:   map[string]int{},
		agentDuration: map[string][]int64{},
	}
}

func (w *windowStats) add(t *models.Task) {
	// Created — counted only inside the window.
	if !t.CreatedAt.IsZero() && inWindow(t.CreatedAt, w.start, w.end) {
		w.totalCreated++
		w.dayCreated[bucketKey(t.CreatedAt)]++
	}

	// In-flight — instantaneous, no window filtering. A task that was
	// created last year but is still ready/draft today is "in flight" for
	// today's report.
	if t.Status == models.TaskStatusReady || t.Status == models.TaskStatusDraft {
		w.totalInFlight++
	}

	// Completion — counted only when CompletedAt is inside the window.
	if t.Status == models.TaskStatusDone && t.CompletedAt != nil &&
		inWindow(*t.CompletedAt, w.start, w.end) {
		key := bucketKey(*t.CompletedAt)
		agent := agentKey(t.Agent)
		w.agentTasks[agent]++
		if t.Success != nil && *t.Success {
			w.totalDone++
			w.dayDone[key]++
			w.agentDone[agent]++
		} else {
			w.totalFailed++
			w.dayFailed[key]++
			w.agentFailed[agent]++
		}
		if t.StartedAt != nil {
			d := int64(t.CompletedAt.Sub(*t.StartedAt).Seconds())
			if d > 0 {
				w.durationsSec = append(w.durationsSec, d)
				w.agentDuration[agent] = append(w.agentDuration[agent], d)
			}
		}
	}
}

func (w *windowStats) kpis() KPIs {
	return KPIs{
		TotalDone:      w.totalDone,
		TotalFailed:    w.totalFailed,
		TotalCreated:   w.totalCreated,
		TotalInFlight:  w.totalInFlight,
		AvgDurationSec: averageInt64(w.durationsSec),
	}
}

// daily returns one row per calendar day that had any activity, ordered
// chronologically. Days inside the window with zero activity are not
// emitted — keeping the table compact for typical reports.
func (w *windowStats) daily() []DayBucket {
	keys := unionKeys(w.dayDone, w.dayFailed, w.dayCreated)
	sort.Strings(keys)
	out := make([]DayBucket, 0, len(keys))
	for _, k := range keys {
		out = append(out, DayBucket{
			Date:    k,
			Done:    w.dayDone[k],
			Failed:  w.dayFailed[k],
			Created: w.dayCreated[k],
		})
	}
	return out
}

// agents returns one row per backend agent, sorted by tasks descending,
// ties broken by agent name to keep tests deterministic.
func (w *windowStats) agents() []AgentBreakdown {
	out := make([]AgentBreakdown, 0, len(w.agentTasks))
	for name, n := range w.agentTasks {
		out = append(out, AgentBreakdown{
			Agent:          name,
			Tasks:          n,
			Done:           w.agentDone[name],
			Failed:         w.agentFailed[name],
			AvgDurationSec: averageInt64(w.agentDuration[name]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tasks != out[j].Tasks {
			return out[i].Tasks > out[j].Tasks
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// inWindow reports whether t falls inside [start, end]. A zero start means
// "no lower bound" (open-ended on the left); same for a zero end. The
// interval is inclusive at both ends — typical reports tick across calendar
// days, so excluding the upper boundary would drop today's activity.
func inWindow(t, start, end time.Time) bool {
	if !start.IsZero() && t.Before(start) {
		return false
	}
	if !end.IsZero() && t.After(end) {
		return false
	}
	return true
}

func bucketKey(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

func agentKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(default)"
	}
	return s
}

func averageInt64(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	var sum int64
	for _, x := range xs {
		sum += x
	}
	return sum / int64(len(xs))
}

func unionKeys(maps ...map[string]int) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// sortPCounts sorts projectCount rows in-place: highest done first, ties by
// failed descending, then by name ascending.
func sortPCounts(rows []projectCount) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].done != rows[j].done {
			return rows[i].done > rows[j].done
		}
		if rows[i].failed != rows[j].failed {
			return rows[i].failed > rows[j].failed
		}
		return rows[i].name < rows[j].name
	})
}
