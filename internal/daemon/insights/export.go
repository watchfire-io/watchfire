package insights

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// Result is the rendered output of an export — bytes plus the canonical
// download metadata the GUI / TUI need to write the file to disk.
type Result struct {
	Filename string
	Mime     string
	Content  []byte
}

// ExportSingleTaskFromData renders a single-task report from a precomputed
// shape. Used by the gRPC service layer (which loads from disk via
// LoadSingleTaskData) and by tests (which construct fixtures inline).
func ExportSingleTaskFromData(d SingleTaskData, format Format, at time.Time) (Result, error) {
	var (
		content []byte
		err     error
	)
	switch format {
	case FormatCSV:
		content, err = renderSingleTaskCSV(d)
	case FormatMarkdown:
		content, err = renderSingleTaskMarkdown(d)
	default:
		return Result{}, fmt.Errorf("insights: unknown format %d", format)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{
		Filename: SingleTaskFilename(d.TaskNumber, format, at),
		Mime:     MimeType(format),
		Content:  content,
	}, nil
}

// ExportProjectFromData renders a per-project report from a precomputed
// shape.
func ExportProjectFromData(d ProjectData, format Format, at time.Time) (Result, error) {
	var (
		content []byte
		err     error
	)
	switch format {
	case FormatCSV:
		content, err = renderProjectCSV(d)
	case FormatMarkdown:
		content, err = renderProjectMarkdown(d)
	default:
		return Result{}, fmt.Errorf("insights: unknown format %d", format)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{
		Filename: ProjectFilename(d.ProjectName, format, at),
		Mime:     MimeType(format),
		Content:  content,
	}, nil
}

// ExportGlobalFromData renders a fleet-rollup report from a precomputed
// shape.
func ExportGlobalFromData(d GlobalData, format Format, at time.Time) (Result, error) {
	var (
		content []byte
		err     error
	)
	switch format {
	case FormatCSV:
		content, err = renderGlobalCSV(d)
	case FormatMarkdown:
		content, err = renderGlobalMarkdown(d)
	default:
		return Result{}, fmt.Errorf("insights: unknown format %d", format)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{
		Filename: GlobalFilename(format, at),
		Mime:     MimeType(format),
		Content:  content,
	}, nil
}

// --- live loaders (production path) ----------------------------------------

// LoadSingleTaskData resolves a "<project_id>:<task_number>" identifier
// into a SingleTaskData by reading the project's task YAML. Returns an
// error if the identifier is malformed or the task doesn't exist.
func LoadSingleTaskData(singleTaskID string) (SingleTaskData, error) {
	projectID, taskNumber, err := parseSingleTaskID(singleTaskID)
	if err != nil {
		return SingleTaskData{}, err
	}
	projectPath, projectName, ok := resolveProjectPath(projectID)
	if !ok {
		return SingleTaskData{}, fmt.Errorf("insights: project %q not found", projectID)
	}
	t, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		return SingleTaskData{}, fmt.Errorf("insights: load task #%d: %w", taskNumber, err)
	}
	if t == nil {
		return SingleTaskData{}, fmt.Errorf("insights: task #%d not found in project %q", taskNumber, projectID)
	}
	m := readMetricsBestEffort(projectPath, t)
	return singleTaskFromTask(projectID, projectName, t, m), nil
}

func singleTaskFromTask(projectID, projectName string, t *models.Task, m *models.TaskMetrics) SingleTaskData {
	d := SingleTaskData{
		ProjectID:      projectID,
		ProjectName:    projectName,
		TaskNumber:     t.TaskNumber,
		Title:          t.Title,
		Status:         string(t.Status),
		Success:        t.Success,
		FailureReason:  t.FailureReason,
		Agent:          t.Agent,
		AgentSessions:  t.AgentSessions,
		StartedAt:      t.StartedAt,
		CompletedAt:    t.CompletedAt,
		WorktreeBranch: fmt.Sprintf("watchfire/%04d", t.TaskNumber),
		Prompt:         truncatePrompt(t.Prompt, 240),
	}
	if t.StartedAt != nil && t.CompletedAt != nil {
		d.DurationSec = int64(t.CompletedAt.Sub(*t.StartedAt).Seconds())
	}
	// v8.0 — shipped-code stats, tolerant of a missing metrics file.
	cf := codeFieldsFrom(m)
	d.Commits = cf.commits
	d.FilesChanged = cf.filesChanged
	d.LinesAdded = cf.linesAdded
	d.LinesRemoved = cf.linesRemoved
	d.NetLines = cf.linesAdded - cf.linesRemoved
	d.Merged = cf.merged
	d.HasCode = cf.hasCode
	if m != nil {
		d.MergeKind = string(m.MergeKind)
	}
	return d
}

func truncatePrompt(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// LoadProjectData builds a ProjectData by walking every task in the project
// and bucketing it into KPIs, daily counts, and per-agent rows. The
// [windowStart, windowEnd] range filters the daily and agent breakdowns —
// in-flight counts (status != done) are always taken at "now" since
// in-flight is an instantaneous notion, not a historical one.
func LoadProjectData(projectID string, windowStart, windowEnd time.Time) (ProjectData, error) {
	projectPath, projectName, ok := resolveProjectPath(projectID)
	if !ok {
		return ProjectData{}, fmt.Errorf("insights: project %q not found", projectID)
	}
	tasks, err := config.LoadAllTasks(projectPath)
	if err != nil {
		return ProjectData{}, fmt.Errorf("insights: load tasks for %q: %w", projectID, err)
	}
	metricsFor := func(t *models.Task) *models.TaskMetrics {
		return readMetricsBestEffort(projectPath, t)
	}
	pd := buildProjectData(projectID, projectName, tasks, windowStart, windowEnd, metricsFor)
	return pd, nil
}

// buildProjectData aggregates a task list into a ProjectData. metricsFor
// resolves a task's `<n>.metrics.yaml` for the v8.0 code-output rollup; it
// may be nil (no code rollup) or return nil for a task without metrics.
func buildProjectData(projectID, projectName string, tasks []*models.Task, windowStart, windowEnd time.Time, metricsFor func(t *models.Task) *models.TaskMetrics) ProjectData {
	pd := ProjectData{
		ProjectID:   projectID,
		ProjectName: projectName,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}
	stats := newWindowStats(windowStart, windowEnd)
	for _, t := range tasks {
		if t == nil || t.IsDeleted() {
			continue
		}
		var m *models.TaskMetrics
		if metricsFor != nil {
			m = metricsFor(t)
		}
		stats.add(t, m)
	}
	pd.KPIs = stats.kpis()
	pd.Code = stats.codeOutput()
	pd.Daily = stats.daily()
	pd.Agents = stats.agents()
	return pd
}

// readMetricsBestEffort loads a task's metrics record, tolerating a nil task,
// a missing file, or a malformed file as "no metrics" (nil) so the code-output
// rollup degrades to zeros rather than failing the whole export.
func readMetricsBestEffort(projectPath string, t *models.Task) *models.TaskMetrics {
	if t == nil {
		return nil
	}
	m, err := config.ReadMetrics(projectPath, t.TaskNumber)
	if err != nil {
		return nil
	}
	return m
}

// LoadGlobalData walks every registered project and rolls up a fleet
// report. Top-projects table is sorted by completed task count (descending);
// ties broken by project name to keep the output deterministic.
func LoadGlobalData(windowStart, windowEnd time.Time) (GlobalData, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return GlobalData{}, fmt.Errorf("insights: load projects index: %w", err)
	}
	gd := GlobalData{WindowStart: windowStart, WindowEnd: windowEnd}
	if index == nil {
		return gd, nil
	}
	stats := newWindowStats(windowStart, windowEnd)
	var per []projectCount
	for _, entry := range index.Projects {
		tasks, err := config.LoadAllTasks(entry.Path)
		if err != nil {
			continue
		}
		ps := projectCount{id: entry.ProjectID, name: entry.Name}
		for _, t := range tasks {
			if t == nil || t.IsDeleted() {
				continue
			}
			m := readMetricsBestEffort(entry.Path, t)
			stats.add(t, m)
			if t.Status == models.TaskStatusDone && t.CompletedAt != nil &&
				inWindow(*t.CompletedAt, windowStart, windowEnd) {
				if t.Success != nil && *t.Success {
					ps.done++
				} else {
					ps.failed++
				}
				// Per-project shipped-code tally for the top-projects churn
				// columns; tolerant of missing metrics (all-zero fields).
				cf := codeFieldsFrom(m)
				ps.commits += cf.commits
				ps.linesAdded += cf.linesAdded
				ps.linesRemoved += cf.linesRemoved
				if cf.merged {
					ps.merges++
				}
			}
		}
		per = append(per, ps)
	}
	gd.ProjectCount = len(index.Projects)
	gd.KPIs = stats.kpis()
	gd.Code = stats.codeOutput()
	gd.Daily = stats.daily()
	gd.Agents = stats.agents()
	gd.TopProjects = topProjectsFrom(per)
	return gd, nil
}

// projectCount is the per-project tally used while building a global
// rollup; promoted out of LoadGlobalData so topProjectsFrom can take a
// concrete type instead of an anonymous struct (which Go's type system
// won't unify across call sites).
type projectCount struct {
	id, name                          string
	done, failed                      int
	commits, linesAdded, linesRemoved int
	merges                            int
}

func topProjectsFrom(rows []projectCount) []ProjectSummary {
	sortPCounts(rows)
	out := make([]ProjectSummary, 0, len(rows))
	for _, r := range rows {
		if r.done == 0 && r.failed == 0 {
			continue
		}
		out = append(out, ProjectSummary{
			ProjectID:    r.id,
			ProjectName:  r.name,
			Done:         r.done,
			Failed:       r.failed,
			Commits:      r.commits,
			LinesAdded:   r.linesAdded,
			LinesRemoved: r.linesRemoved,
			NetLines:     r.linesAdded - r.linesRemoved,
			Merges:       r.merges,
		})
	}
	return out
}

// parseSingleTaskID accepts "<project_id>:<task_number>" — the ID format
// the proto contract requires for ExportReportRequest.scope.single_task.
func parseSingleTaskID(s string) (string, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, fmt.Errorf("insights: single_task must be %q got %q", "<project_id>:<task_number>", s)
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 1 {
		return "", 0, fmt.Errorf("insights: single_task task_number %q invalid: %w", parts[1], err)
	}
	return parts[0], n, nil
}

func resolveProjectPath(projectID string) (path, name string, ok bool) {
	index, err := config.LoadProjectsIndex()
	if err != nil || index == nil {
		return "", "", false
	}
	for _, e := range index.Projects {
		if e.ProjectID == projectID {
			return e.Path, e.Name, true
		}
	}
	return "", "", false
}
