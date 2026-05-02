package insights

import (
	"encoding/csv"
	"fmt"
	"strings"
	"time"
)

// renderSingleTaskCSV emits one section with one data row. A single-task
// export is small; the section header keeps the file shape consistent with
// the per-project + global exports so downstream parsers don't branch on
// scope.
func renderSingleTaskCSV(d SingleTaskData) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("# section: task\n")
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{
		"project_id", "project_name", "task_number", "title", "status",
		"success", "failure_reason", "agent", "agent_sessions",
		"started_at", "completed_at", "duration_sec", "worktree_branch",
	}); err != nil {
		return nil, err
	}
	if err := w.Write([]string{
		d.ProjectID,
		d.ProjectName,
		fmt.Sprintf("%d", d.TaskNumber),
		d.Title,
		d.Status,
		boolPtrString(d.Success),
		d.FailureReason,
		d.Agent,
		fmt.Sprintf("%d", d.AgentSessions),
		timePtrString(d.StartedAt),
		timePtrString(d.CompletedAt),
		fmt.Sprintf("%d", d.DurationSec),
		d.WorktreeBranch,
	}); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// renderProjectCSV emits two sections in one file: daily breakdown and
// agent breakdown. The `# section:` headers let a parser split them without
// peeking at column counts.
func renderProjectCSV(d ProjectData) ([]byte, error) {
	var buf strings.Builder

	// KPI strip — single-row "header" section so any tooling can pull the
	// summary numbers without summing the per-day section.
	buf.WriteString("# section: kpis\n")
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{
		"project_id", "project_name", "window_start", "window_end",
		"total_done", "total_failed", "total_created", "total_in_flight", "avg_duration_sec",
	}); err != nil {
		return nil, err
	}
	if err := w.Write([]string{
		d.ProjectID,
		d.ProjectName,
		windowFmt(d.WindowStart),
		windowFmt(d.WindowEnd),
		fmt.Sprintf("%d", d.KPIs.TotalDone),
		fmt.Sprintf("%d", d.KPIs.TotalFailed),
		fmt.Sprintf("%d", d.KPIs.TotalCreated),
		fmt.Sprintf("%d", d.KPIs.TotalInFlight),
		fmt.Sprintf("%d", d.KPIs.AvgDurationSec),
	}); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}

	if err := writeDailySection(&buf, d.Daily); err != nil {
		return nil, err
	}
	if err := writeAgentsSection(&buf, d.Agents); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// renderGlobalCSV emits four sections: kpis, daily, top_projects, agents.
func renderGlobalCSV(d GlobalData) ([]byte, error) {
	var buf strings.Builder

	buf.WriteString("# section: kpis\n")
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{
		"window_start", "window_end", "project_count",
		"total_done", "total_failed", "total_created", "total_in_flight", "avg_duration_sec",
	}); err != nil {
		return nil, err
	}
	if err := w.Write([]string{
		windowFmt(d.WindowStart),
		windowFmt(d.WindowEnd),
		fmt.Sprintf("%d", d.ProjectCount),
		fmt.Sprintf("%d", d.KPIs.TotalDone),
		fmt.Sprintf("%d", d.KPIs.TotalFailed),
		fmt.Sprintf("%d", d.KPIs.TotalCreated),
		fmt.Sprintf("%d", d.KPIs.TotalInFlight),
		fmt.Sprintf("%d", d.KPIs.AvgDurationSec),
	}); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}

	if err := writeDailySection(&buf, d.Daily); err != nil {
		return nil, err
	}

	buf.WriteString("# section: top_projects\n")
	w = csv.NewWriter(&buf)
	if err := w.Write([]string{"project_id", "project_name", "done", "failed"}); err != nil {
		return nil, err
	}
	for _, p := range d.TopProjects {
		if err := w.Write([]string{
			p.ProjectID,
			p.ProjectName,
			fmt.Sprintf("%d", p.Done),
			fmt.Sprintf("%d", p.Failed),
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}

	if err := writeAgentsSection(&buf, d.Agents); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func writeDailySection(buf *strings.Builder, daily []DayBucket) error {
	buf.WriteString("# section: daily\n")
	w := csv.NewWriter(buf)
	if err := w.Write([]string{"date", "done", "failed", "created"}); err != nil {
		return err
	}
	for _, b := range daily {
		if err := w.Write([]string{
			b.Date,
			fmt.Sprintf("%d", b.Done),
			fmt.Sprintf("%d", b.Failed),
			fmt.Sprintf("%d", b.Created),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func writeAgentsSection(buf *strings.Builder, agents []AgentBreakdown) error {
	buf.WriteString("# section: agents\n")
	w := csv.NewWriter(buf)
	if err := w.Write([]string{"agent", "tasks", "done", "failed", "avg_duration_sec"}); err != nil {
		return err
	}
	for _, a := range agents {
		if err := w.Write([]string{
			a.Agent,
			fmt.Sprintf("%d", a.Tasks),
			fmt.Sprintf("%d", a.Done),
			fmt.Sprintf("%d", a.Failed),
			fmt.Sprintf("%d", a.AvgDurationSec),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func boolPtrString(b *bool) string {
	if b == nil {
		return ""
	}
	if *b {
		return "true"
	}
	return "false"
}

func timePtrString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func windowFmt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
