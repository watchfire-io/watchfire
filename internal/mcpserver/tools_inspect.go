package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	pb "github.com/watchfire-io/watchfire/proto"
)

// get_agent_screen tail-window bounds.
const (
	defaultScreenLines = 100
	maxScreenLines     = 1000
)

// maxLogBytes caps get_log content so one transcript cannot blow out the
// MCP client's context window. When a log is longer, only the tail is
// returned (the end of a session is where outcomes and errors live).
const maxLogBytes = 64 * 1024

// binaryHunkHeader is the synthetic hunk header the daemon's diff package
// emits for binary files (see internal/daemon/diff).
const binaryHunkHeader = "Binary file changed"

var inspectTools = []toolDef{
	newTool(toolSpec{
		Group: groupInspect, Name: "get_task_diff", Title: "Get task diff",
		ReadOnly: true, Idempotent: true,
		Description: "Get the code change a task produced, rendered as unified-diff text with per-file headers, +/- lines, and per-file plus total addition/deletion counts (also returned structured). Works whether the task's watchfire/<task_number> branch still exists or was already merged and deleted. This is the review step of the factory loop: call it after wait_for_task + get_task to see exactly what was shipped. Very large diffs are capped by the daemon and flagged truncated: true.",
	}, handleGetTaskDiff),
	newTool(toolSpec{
		Group: groupInspect, Name: "get_agent_screen", Title: "Get agent screen",
		ReadOnly:    true,
		Description: "Peek at the live agent terminal to diagnose a stuck or long-running task: returns the last N lines (default 100, max 1000) of the running agent's screen and scrollback as plain text, with ANSI escape sequences stripped. Requires an agent to be running for the project (check get_agent_status) — for finished sessions use list_logs / get_log instead.",
	}, handleGetAgentScreen,
		defaultProperty("lines", "100"),
		rangeProperty("lines", 1, maxScreenLines)),
	newTool(toolSpec{
		Group: groupInspect, Name: "get_insights", Title: "Get insights",
		ReadOnly: true, Idempotent: true,
		Description: "Get a compact productivity summary: task throughput (total / succeeded / failed, success rate), duration and cost totals, shipped-code output (commits, files changed, lines added/removed, merges), and a per-agent breakdown. scope \"project\" (the default) summarizes one project; scope \"global\" aggregates every registered project and lists the top projects, ignoring the \"project\" argument.",
	}, handleGetInsights,
		enumProperty("scope", "project", "global"),
		defaultProperty("scope", `"project"`)),
	newTool(toolSpec{
		Group: groupInspect, Name: "list_logs", Title: "List session logs",
		ReadOnly: true, Idempotent: true,
		Description: "List past agent session logs (transcripts) for a project: log_id, task_number, session number, agent backend, mode, start/end times, and exit status. Use get_log with a log_id to read one transcript.",
	}, handleListLogs),
	newTool(toolSpec{
		Group: groupInspect, Name: "get_log", Title: "Get session log",
		ReadOnly: true, Idempotent: true,
		Description: "Read one past agent session transcript by log_id (see list_logs). The content is plain text and capped at 64 KiB: when a transcript is longer, only its tail is returned and truncated: true is set.",
	}, handleGetLog),
}

// ---------------------------------------------------------------------------
// get_task_diff

// fileDiffStat is the structured per-file counterpart of the rendered diff.
type fileDiffStat struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	OldPath   string `json:"old_path,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
}

type taskDiffResult struct {
	TaskNumber     int32          `json:"task_number"`
	FilesChanged   int            `json:"files_changed"`
	TotalAdditions int32          `json:"total_additions"`
	TotalDeletions int32          `json:"total_deletions"`
	Truncated      bool           `json:"truncated,omitempty"`
	Note           string         `json:"note,omitempty"`
	Files          []fileDiffStat `json:"files"`
	Diff           string         `json:"diff"`
}

func handleGetTaskDiff(ctx context.Context, s *server, args taskRefArgs) (any, error) {
	if args.TaskNumber <= 0 {
		return nil, fmt.Errorf("\"task_number\" is required")
	}
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	set, err := s.insights.GetTaskDiff(ctx, &pb.GetTaskDiffRequest{
		ProjectId:  projectID,
		TaskNumber: args.TaskNumber,
	})
	if err != nil {
		return nil, rpcErr(fmt.Sprintf("get the diff for task %d", args.TaskNumber), err)
	}

	res := taskDiffResult{
		TaskNumber:     args.TaskNumber,
		FilesChanged:   len(set.Files),
		TotalAdditions: set.TotalAdditions,
		TotalDeletions: set.TotalDeletions,
		Truncated:      set.Truncated,
		Files:          make([]fileDiffStat, 0, len(set.Files)),
		Diff:           renderUnifiedDiff(set),
	}
	for _, f := range set.Files {
		add, del := fileDiffCounts(f)
		res.Files = append(res.Files, fileDiffStat{
			Path:      f.Path,
			Status:    fileStatusString(f.Status),
			OldPath:   f.OldPath,
			Additions: add,
			Deletions: del,
			Binary:    isBinaryDiff(f),
		})
	}
	if set.Truncated {
		res.Note = "diff truncated by daemon (MaxDiffLines)"
	}
	return res, nil
}

// renderUnifiedDiff renders a FileDiffSet as unified-diff-style text: a
// summary header with per-file addition/deletion counts, --- / +++ file
// headers, @@ hunk headers, and prefixed +/-/space lines, ending with the
// set-wide totals.
func renderUnifiedDiff(set *pb.FileDiffSet) string {
	var b strings.Builder
	for i, f := range set.Files {
		if i > 0 {
			b.WriteByte('\n')
		}
		add, del := fileDiffCounts(f)

		oldPath, newPath := "a/"+f.Path, "b/"+f.Path
		label := fileStatusString(f.Status)
		switch f.Status {
		case pb.FileDiff_ADDED:
			oldPath = "/dev/null"
		case pb.FileDiff_DELETED:
			newPath = "/dev/null"
		case pb.FileDiff_RENAMED:
			if f.OldPath != "" {
				oldPath = "a/" + f.OldPath
				label = fmt.Sprintf("renamed from %s", f.OldPath)
			}
		}

		fmt.Fprintf(&b, "=== %s (%s) +%d -%d\n", f.Path, label, add, del)
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldPath, newPath)

		if isBinaryDiff(f) {
			b.WriteString(binaryHunkHeader + "\n")
			continue
		}
		for _, h := range f.Hunks {
			fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
			if h.Header != "" {
				b.WriteString(" " + h.Header)
			}
			b.WriteByte('\n')
			for _, l := range h.Lines {
				switch l.Kind {
				case pb.DiffLine_ADD:
					b.WriteByte('+')
				case pb.DiffLine_DEL:
					b.WriteByte('-')
				default:
					b.WriteByte(' ')
				}
				b.WriteString(l.Text)
				b.WriteByte('\n')
			}
		}
	}

	fmt.Fprintf(&b, "\n%d file(s) changed, +%d -%d\n", len(set.Files), set.TotalAdditions, set.TotalDeletions)
	if set.Truncated {
		b.WriteString("[diff truncated by daemon (MaxDiffLines)]\n")
	}
	return b.String()
}

func fileDiffCounts(f *pb.FileDiff) (add, del int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case pb.DiffLine_ADD:
				add++
			case pb.DiffLine_DEL:
				del++
			}
		}
	}
	return add, del
}

// isBinaryDiff matches the daemon's binary-file marker: a single hunk with
// the synthetic "Binary file changed" header and no lines.
func isBinaryDiff(f *pb.FileDiff) bool {
	return len(f.Hunks) == 1 && f.Hunks[0].Header == binaryHunkHeader && len(f.Hunks[0].Lines) == 0
}

func fileStatusString(st pb.FileDiff_Status) string {
	return strings.ToLower(st.String())
}

// ---------------------------------------------------------------------------
// get_agent_screen

type agentScreenArgs struct {
	Project string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	Lines   int32  `json:"lines,omitempty" jsonschema:"Number of trailing terminal lines to return. Default 100, max 1000."`
}

type agentScreenResult struct {
	TotalLines    int32  `json:"total_lines"`
	ReturnedLines int    `json:"returned_lines"`
	Screen        string `json:"screen"`
}

func handleGetAgentScreen(ctx context.Context, s *server, args agentScreenArgs) (any, error) {
	if args.Lines < 0 {
		return nil, fmt.Errorf("\"lines\" must be positive")
	}
	n := args.Lines
	if n == 0 {
		n = defaultScreenLines
	} else if n > maxScreenLines {
		n = maxScreenLines
	}

	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	// The daemon's scrollback API is offset/limit from the top; probe the
	// total first (limit 0 returns no lines), then fetch the tail window.
	probe, err := s.agents.GetScrollback(ctx, &pb.ScrollbackRequest{ProjectId: projectID})
	if err != nil {
		return nil, rpcErr("read the agent screen (no agent may be running for this project — check get_agent_status)", err)
	}

	offset, limit := tailWindow(probe.TotalLines, n)
	res := agentScreenResult{TotalLines: probe.TotalLines}
	if limit == 0 {
		return res, nil
	}

	tail, err := s.agents.GetScrollback(ctx, &pb.ScrollbackRequest{
		ProjectId: projectID,
		Offset:    offset,
		Limit:     limit,
	})
	if err != nil {
		return nil, rpcErr("read the agent screen", err)
	}

	lines := make([]string, 0, len(tail.Lines))
	for _, l := range tail.Lines {
		lines = append(lines, plainLine(l))
	}
	res.TotalLines = tail.TotalLines
	res.ReturnedLines = len(lines)
	res.Screen = strings.Join(lines, "\n")
	return res, nil
}

// plainLine converts one raw scrollback line to plain text: ANSI escape
// sequences are stripped, carriage-return overwrites (spinner redraws)
// resolve to the last non-empty segment, and trailing padding is trimmed.
func plainLine(l string) string {
	l = ansi.Strip(l)
	if strings.ContainsRune(l, '\r') {
		segments := strings.Split(l, "\r")
		l = ""
		for i := len(segments) - 1; i >= 0; i-- {
			if segments[i] != "" {
				l = segments[i]
				break
			}
		}
	}
	return strings.TrimRight(l, " ")
}

// tailWindow maps "the last n of total lines" onto the daemon's
// offset/limit scrollback parameters.
func tailWindow(total, n int32) (offset, limit int32) {
	if n > total {
		n = total
	}
	return total - n, n
}

// ---------------------------------------------------------------------------
// get_insights

type getInsightsArgs struct {
	Project string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory. Ignored when scope is \"global\"."`
	Scope   string `json:"scope,omitempty" jsonschema:"\"project\" (the default) summarizes one project; \"global\" aggregates every registered project."`
}

type codeOutputSummary struct {
	Commits                 int32 `json:"commits"`
	FilesChanged            int32 `json:"files_changed"`
	LinesAdded              int32 `json:"lines_added"`
	LinesRemoved            int32 `json:"lines_removed"`
	NetLines                int32 `json:"net_lines"`
	TasksMerged             int32 `json:"tasks_merged"`
	TasksViaPR              int32 `json:"tasks_via_pr"`
	TasksMissingCodeMetrics int32 `json:"tasks_missing_code_metrics,omitempty"`
}

type agentThroughput struct {
	Agent       string  `json:"agent"`
	Tasks       int32   `json:"tasks"`
	SuccessRate float64 `json:"success_rate"`
}

type topProjectRow struct {
	Name     string `json:"name"`
	Tasks    int32  `json:"tasks"`
	NetLines int32  `json:"net_lines"`
}

type insightsSummary struct {
	Scope            string            `json:"scope"`
	ProjectID        string            `json:"project_id,omitempty"`
	TasksTotal       int32             `json:"tasks_total"`
	TasksSucceeded   int32             `json:"tasks_succeeded"`
	TasksFailed      int32             `json:"tasks_failed"`
	SuccessRate      float64           `json:"success_rate"`
	TotalDurationMs  int64             `json:"total_duration_ms"`
	AvgDurationMs    int64             `json:"avg_duration_ms,omitempty"`
	TotalCostUSD     float64           `json:"total_cost_usd"`
	TasksMissingCost int32             `json:"tasks_missing_cost,omitempty"`
	CodeOutput       codeOutputSummary `json:"code_output"`
	Agents           []agentThroughput `json:"agents,omitempty"`
	TopProjects      []topProjectRow   `json:"top_projects,omitempty"`
}

func handleGetInsights(ctx context.Context, s *server, args getInsightsArgs) (any, error) {
	switch args.Scope {
	case "", "project":
		return projectInsightsSummary(ctx, s, args.Project)
	case "global":
		return globalInsightsSummary(ctx, s)
	default:
		return nil, fmt.Errorf("invalid scope %q: must be \"project\" or \"global\"", args.Scope)
	}
}

func projectInsightsSummary(ctx context.Context, s *server, project string) (any, error) {
	projectID, err := s.resolveProject(ctx, project)
	if err != nil {
		return nil, err
	}
	in, err := s.insights.GetProjectInsights(ctx, &pb.GetProjectInsightsRequest{ProjectId: projectID})
	if err != nil {
		return nil, rpcErr("get project insights", err)
	}
	return insightsSummary{
		Scope:            "project",
		ProjectID:        in.ProjectId,
		TasksTotal:       in.TasksTotal,
		TasksSucceeded:   in.TasksSucceeded,
		TasksFailed:      in.TasksFailed,
		SuccessRate:      successRate(in.TasksSucceeded, in.TasksTotal),
		TotalDurationMs:  in.TotalDurationMs,
		AvgDurationMs:    in.AvgDurationMs,
		TotalCostUSD:     in.TotalCostUsd,
		TasksMissingCost: in.TasksMissingCost,
		CodeOutput: codeOutputSummary{
			Commits:                 in.TotalCommits,
			FilesChanged:            in.TotalFilesChanged,
			LinesAdded:              in.TotalLinesAdded,
			LinesRemoved:            in.TotalLinesRemoved,
			NetLines:                in.NetLines,
			TasksMerged:             in.TasksMerged,
			TasksViaPR:              in.TasksViaPr,
			TasksMissingCodeMetrics: in.MetricsMissingCode,
		},
		Agents: agentRows(in.AgentBreakdown),
	}, nil
}

func globalInsightsSummary(ctx context.Context, s *server) (any, error) {
	in, err := s.insights.GetGlobalInsights(ctx, &pb.GetGlobalInsightsRequest{})
	if err != nil {
		return nil, rpcErr("get global insights", err)
	}
	sum := insightsSummary{
		Scope:            "global",
		TasksTotal:       in.TasksTotal,
		TasksSucceeded:   in.TasksSucceeded,
		TasksFailed:      in.TasksFailed,
		SuccessRate:      successRate(in.TasksSucceeded, in.TasksTotal),
		TotalDurationMs:  in.TotalDurationMs,
		TotalCostUSD:     in.TotalCostUsd,
		TasksMissingCost: in.TasksMissingCost,
		CodeOutput: codeOutputSummary{
			Commits:                 in.TotalCommits,
			FilesChanged:            in.TotalFilesChanged,
			LinesAdded:              in.TotalLinesAdded,
			LinesRemoved:            in.TotalLinesRemoved,
			NetLines:                in.NetLines,
			TasksMerged:             in.TasksMerged,
			TasksViaPR:              in.TasksViaPr,
			TasksMissingCodeMetrics: in.MetricsMissingCode,
		},
		Agents: agentRows(in.AgentBreakdown),
	}
	for _, p := range in.TopProjects {
		sum.TopProjects = append(sum.TopProjects, topProjectRow{
			Name:     p.ProjectName,
			Tasks:    p.Count,
			NetLines: p.NetLines,
		})
	}
	return sum, nil
}

func agentRows(breakdown []*pb.AgentBreakdown) []agentThroughput {
	rows := make([]agentThroughput, 0, len(breakdown))
	for _, a := range breakdown {
		rows = append(rows, agentThroughput{
			Agent:       a.Agent,
			Tasks:       a.Count,
			SuccessRate: a.SuccessRate,
		})
	}
	return rows
}

func successRate(succeeded, total int32) float64 {
	if total == 0 {
		return 0
	}
	return float64(succeeded) / float64(total)
}

// ---------------------------------------------------------------------------
// list_logs / get_log

type listLogsArgs struct {
	Project string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
}

type getLogArgs struct {
	Project string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	LogID   string `json:"log_id" jsonschema:"Log id of the transcript to read (see list_logs)."`
}

type logRow struct {
	LogID         string `json:"log_id"`
	TaskNumber    int32  `json:"task_number,omitempty"`
	SessionNumber int32  `json:"session_number,omitempty"`
	Agent         string `json:"agent,omitempty"`
	Mode          string `json:"mode,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	EndedAt       string `json:"ended_at,omitempty"`
	Status        string `json:"status,omitempty"`
}

type logContentResult struct {
	Log       *logRow `json:"log,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Note      string  `json:"note,omitempty"`
	Content   string  `json:"content"`
}

func handleListLogs(ctx context.Context, s *server, args listLogsArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	list, err := s.logs.ListLogs(ctx, &pb.ListLogsRequest{ProjectId: projectID})
	if err != nil {
		return nil, rpcErr("list session logs", err)
	}

	rows := make([]logRow, 0, len(list.Logs))
	for _, l := range list.Logs {
		rows = append(rows, *protoLogRow(l))
	}
	return struct {
		Logs []logRow `json:"logs"`
	}{rows}, nil
}

func handleGetLog(ctx context.Context, s *server, args getLogArgs) (any, error) {
	if strings.TrimSpace(args.LogID) == "" {
		return nil, fmt.Errorf("\"log_id\" is required")
	}
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	log, err := s.logs.GetLog(ctx, &pb.GetLogRequest{ProjectId: projectID, LogId: args.LogID})
	if err != nil {
		return nil, rpcErr(fmt.Sprintf("get log %q (see list_logs for valid log ids)", args.LogID), err)
	}

	res := logContentResult{Log: protoLogRow(log.Entry)}
	// The daemon's LogContent entry omits the id; backfill it so the result
	// is self-describing.
	if res.Log != nil && res.Log.LogID == "" {
		res.Log.LogID = args.LogID
	}
	res.Content, res.Truncated = truncateTail(log.Content, maxLogBytes)
	if res.Truncated {
		res.Note = fmt.Sprintf("transcript truncated: showing the last %d of %d bytes", len(res.Content), len(log.Content))
	}
	return res, nil
}

func protoLogRow(l *pb.LogEntry) *logRow {
	if l == nil {
		return nil
	}
	return &logRow{
		LogID:         l.LogId,
		TaskNumber:    l.TaskNumber,
		SessionNumber: l.SessionNumber,
		Agent:         l.Agent,
		Mode:          l.Mode,
		StartedAt:     l.StartedAt,
		EndedAt:       l.EndedAt,
		Status:        l.Status,
	}
}

// truncateTail keeps the trailing max bytes of s, dropping the partial first
// line the cut leaves behind.
func truncateTail(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := s[len(s)-max:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	}
	return cut, true
}
