package mcpserver

import (
	"context"
	"sort"
	"strings"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/watchfire-io/watchfire/proto"
)

func (f *fakeAgentClient) GetScrollback(_ context.Context, req *pb.ScrollbackRequest, _ ...grpc.CallOption) (*pb.ScrollbackLines, error) {
	return f.scrollbackFn(req)
}

// fakeInsightsClient serves the three InsightsService methods the inspect
// tools use; every other method panics via the embedded nil interface.
type fakeInsightsClient struct {
	pb.InsightsServiceClient
	diffFn    func(*pb.GetTaskDiffRequest) (*pb.FileDiffSet, error)
	projectFn func(*pb.GetProjectInsightsRequest) (*pb.ProjectInsights, error)
	globalFn  func(*pb.GetGlobalInsightsRequest) (*pb.GlobalInsights, error)
}

func (f *fakeInsightsClient) GetTaskDiff(_ context.Context, req *pb.GetTaskDiffRequest, _ ...grpc.CallOption) (*pb.FileDiffSet, error) {
	return f.diffFn(req)
}

func (f *fakeInsightsClient) GetProjectInsights(_ context.Context, req *pb.GetProjectInsightsRequest, _ ...grpc.CallOption) (*pb.ProjectInsights, error) {
	return f.projectFn(req)
}

func (f *fakeInsightsClient) GetGlobalInsights(_ context.Context, req *pb.GetGlobalInsightsRequest, _ ...grpc.CallOption) (*pb.GlobalInsights, error) {
	return f.globalFn(req)
}

type fakeLogClient struct {
	pb.LogServiceClient
	listFn func(*pb.ListLogsRequest) (*pb.LogList, error)
	getFn  func(*pb.GetLogRequest) (*pb.LogContent, error)
}

func (f *fakeLogClient) ListLogs(_ context.Context, req *pb.ListLogsRequest, _ ...grpc.CallOption) (*pb.LogList, error) {
	return f.listFn(req)
}

func (f *fakeLogClient) GetLog(_ context.Context, req *pb.GetLogRequest, _ ...grpc.CallOption) (*pb.LogContent, error) {
	return f.getFn(req)
}

// ---------------------------------------------------------------------------
// get_task_diff rendering

func sampleDiffSet() *pb.FileDiffSet {
	return &pb.FileDiffSet{
		Files: []*pb.FileDiff{
			{
				Path:   "main.go",
				Status: pb.FileDiff_MODIFIED,
				Hunks: []*pb.Hunk{{
					OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3,
					Header: "func main",
					Lines: []*pb.DiffLine{
						{Kind: pb.DiffLine_CONTEXT, Text: "import \"fmt\""},
						{Kind: pb.DiffLine_DEL, Text: "fmt.Println(\"old\")"},
						{Kind: pb.DiffLine_ADD, Text: "fmt.Println(\"new\")"},
					},
				}},
			},
			{
				Path:   "added.go",
				Status: pb.FileDiff_ADDED,
				Hunks: []*pb.Hunk{{
					OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2,
					Lines: []*pb.DiffLine{
						{Kind: pb.DiffLine_ADD, Text: "package extra"},
						{Kind: pb.DiffLine_ADD, Text: "var X = 1"},
					},
				}},
			},
			{
				Path:   "gone.go",
				Status: pb.FileDiff_DELETED,
				Hunks: []*pb.Hunk{{
					OldStart: 1, OldLines: 1, NewStart: 0, NewLines: 0,
					Lines: []*pb.DiffLine{
						{Kind: pb.DiffLine_DEL, Text: "package gone"},
					},
				}},
			},
			{
				Path:    "renamed.go",
				OldPath: "original.go",
				Status:  pb.FileDiff_RENAMED,
				Hunks: []*pb.Hunk{{
					OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
					Lines: []*pb.DiffLine{
						{Kind: pb.DiffLine_CONTEXT, Text: "package same"},
					},
				}},
			},
			{
				Path:   "logo.png",
				Status: pb.FileDiff_MODIFIED,
				Hunks:  []*pb.Hunk{{Header: "Binary file changed"}},
			},
		},
		TotalAdditions: 3,
		TotalDeletions: 2,
	}
}

func TestRenderUnifiedDiff(t *testing.T) {
	out := renderUnifiedDiff(sampleDiffSet())

	for _, want := range []string{
		"=== main.go (modified) +1 -1",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,3 +1,3 @@ func main",
		" import \"fmt\"",
		"-fmt.Println(\"old\")",
		"+fmt.Println(\"new\")",
		"=== added.go (added) +2 -0",
		"--- /dev/null",
		"+++ b/added.go",
		"=== gone.go (deleted) +0 -1",
		"+++ /dev/null",
		"=== renamed.go (renamed from original.go) +0 -0",
		"--- a/original.go",
		"+++ b/renamed.go",
		"=== logo.png (modified) +0 -0",
		"Binary file changed",
		"5 file(s) changed, +3 -2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered diff missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "truncated") {
		t.Errorf("untruncated diff must not mention truncation:\n%s", out)
	}
}

func TestRenderUnifiedDiffTruncated(t *testing.T) {
	set := sampleDiffSet()
	set.Truncated = true

	out := renderUnifiedDiff(set)
	if !strings.Contains(out, "diff truncated by daemon (MaxDiffLines)") {
		t.Errorf("truncated diff must carry the daemon-cap note:\n%s", out)
	}
}

func TestGetTaskDiff(t *testing.T) {
	var got *pb.GetTaskDiffRequest
	s := testServer(&fakeTaskClient{})
	s.insights = &fakeInsightsClient{
		diffFn: func(req *pb.GetTaskDiffRequest) (*pb.FileDiffSet, error) {
			got = req
			set := sampleDiffSet()
			set.Truncated = true
			return set, nil
		},
	}

	out, err := handleGetTaskDiff(context.Background(), s, taskRefArgs{TaskNumber: 42})
	if err != nil {
		t.Fatalf("handleGetTaskDiff: %v", err)
	}
	if got.ProjectId != "id-demo" || got.TaskNumber != 42 {
		t.Errorf("unexpected request: %+v", got)
	}

	res := out.(taskDiffResult)
	if res.FilesChanged != 5 || res.TotalAdditions != 3 || res.TotalDeletions != 2 {
		t.Errorf("unexpected totals: %+v", res)
	}
	if !res.Truncated || !strings.Contains(res.Note, "MaxDiffLines") {
		t.Errorf("truncation must surface in the structured result: %+v", res)
	}
	if len(res.Files) != 5 {
		t.Fatalf("want 5 file stats, got %d", len(res.Files))
	}
	if f := res.Files[0]; f.Path != "main.go" || f.Additions != 1 || f.Deletions != 1 {
		t.Errorf("unexpected per-file stat: %+v", f)
	}
	if f := res.Files[3]; f.OldPath != "original.go" || f.Status != "renamed" {
		t.Errorf("unexpected rename stat: %+v", f)
	}
	if f := res.Files[4]; !f.Binary {
		t.Errorf("binary file must be flagged: %+v", f)
	}
}

func TestGetTaskDiffRequiresTaskNumber(t *testing.T) {
	s := testServer(&fakeTaskClient{})

	_, err := handleGetTaskDiff(context.Background(), s, taskRefArgs{})
	if err == nil || !strings.Contains(err.Error(), `"task_number" is required`) {
		t.Errorf("want task_number-required error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// get_agent_screen

func TestTailWindow(t *testing.T) {
	tests := []struct {
		total, n, wantOffset, wantLimit int32
	}{
		{0, 100, 0, 0},   // empty scrollback
		{50, 100, 0, 50}, // fewer lines than requested
		{250, 100, 150, 100},
		{1000, 1000, 0, 1000},
		{100, 100, 0, 100}, // exact fit
	}
	for _, tc := range tests {
		offset, limit := tailWindow(tc.total, tc.n)
		if offset != tc.wantOffset || limit != tc.wantLimit {
			t.Errorf("tailWindow(%d, %d) = (%d, %d), want (%d, %d)",
				tc.total, tc.n, offset, limit, tc.wantOffset, tc.wantLimit)
		}
	}
}

func TestGetAgentScreenTailAndStrip(t *testing.T) {
	lines := make([]string, 250)
	for i := range lines {
		// ANSI colors, a carriage-return spinner redraw, and trailing pad.
		lines[i] = "\x1b[31m·spinner\r\x1b[0mline   "
	}

	var reqs []*pb.ScrollbackRequest
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		scrollbackFn: func(req *pb.ScrollbackRequest) (*pb.ScrollbackLines, error) {
			reqs = append(reqs, req)
			end := int(req.Offset + req.Limit)
			if end > len(lines) {
				end = len(lines)
			}
			var window []string
			if int(req.Offset) < end {
				window = lines[req.Offset:end]
			}
			return &pb.ScrollbackLines{Lines: window, TotalLines: int32(len(lines))}, nil
		},
	})

	out, err := handleGetAgentScreen(context.Background(), s, agentScreenArgs{})
	if err != nil {
		t.Fatalf("handleGetAgentScreen: %v", err)
	}

	if len(reqs) != 2 {
		t.Fatalf("want probe + tail requests, got %d", len(reqs))
	}
	if reqs[0].Limit != 0 {
		t.Errorf("probe request must use limit 0, got %d", reqs[0].Limit)
	}
	if reqs[1].Offset != 150 || reqs[1].Limit != 100 {
		t.Errorf("tail request = offset %d limit %d, want offset 150 limit 100", reqs[1].Offset, reqs[1].Limit)
	}

	res := out.(agentScreenResult)
	if res.TotalLines != 250 || res.ReturnedLines != 100 {
		t.Errorf("unexpected result counts: %+v", res)
	}
	if strings.ContainsAny(res.Screen, "\x1b\r") {
		t.Error("screen must not contain ANSI escapes or carriage returns")
	}
	for _, l := range strings.Split(res.Screen, "\n") {
		if l != "line" {
			t.Fatalf("unexpected screen line %q (ANSI, CR overwrite, or padding not resolved)", l)
		}
	}
}

func TestPlainLine(t *testing.T) {
	tests := []struct{ in, want string }{
		{"\x1b[31mred\x1b[0m   ", "red"},
		{"·spin\rdone", "done"}, // CR overwrite keeps the last segment
		{"\r✳", "✳"},            // leading CR
		{"text\r", "text"},      // trailing CR resolves to the text
		{"\r\r", ""},            // only CRs
		{"plain", "plain"},
	}
	for _, tc := range tests {
		if got := plainLine(tc.in); got != tc.want {
			t.Errorf("plainLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGetAgentScreenClampsAndEmpty(t *testing.T) {
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		scrollbackFn: func(req *pb.ScrollbackRequest) (*pb.ScrollbackLines, error) {
			if req.Limit > maxScreenLines {
				t.Errorf("limit %d exceeds the %d-line cap", req.Limit, maxScreenLines)
			}
			return &pb.ScrollbackLines{TotalLines: 0}, nil
		},
	})

	out, err := handleGetAgentScreen(context.Background(), s, agentScreenArgs{Lines: 5000})
	if err != nil {
		t.Fatalf("handleGetAgentScreen: %v", err)
	}
	res := out.(agentScreenResult)
	if res.TotalLines != 0 || res.ReturnedLines != 0 || res.Screen != "" {
		t.Errorf("empty scrollback must yield an empty screen: %+v", res)
	}
}

// ---------------------------------------------------------------------------
// get_insights

func TestGetInsightsProjectDefault(t *testing.T) {
	var got *pb.GetProjectInsightsRequest
	s := testServer(&fakeTaskClient{})
	s.insights = &fakeInsightsClient{
		projectFn: func(req *pb.GetProjectInsightsRequest) (*pb.ProjectInsights, error) {
			got = req
			return &pb.ProjectInsights{
				ProjectId: req.ProjectId, TasksTotal: 10, TasksSucceeded: 8, TasksFailed: 2,
				TotalDurationMs: 60000, AvgDurationMs: 6000, TotalCostUsd: 1.25,
				TotalCommits: 14, TotalLinesAdded: 500, TotalLinesRemoved: 120,
				NetLines: 380, TasksMerged: 8,
				AgentBreakdown: []*pb.AgentBreakdown{{Agent: "claude-code", Count: 10, SuccessRate: 0.8}},
			}, nil
		},
	}

	out, err := handleGetInsights(context.Background(), s, getInsightsArgs{})
	if err != nil {
		t.Fatalf("handleGetInsights: %v", err)
	}
	if got.ProjectId != "id-demo" {
		t.Errorf("project insights requested for %q, want id-demo", got.ProjectId)
	}

	res := out.(insightsSummary)
	if res.Scope != "project" || res.ProjectID != "id-demo" {
		t.Errorf("unexpected scope/project: %+v", res)
	}
	if res.TasksTotal != 10 || res.SuccessRate != 0.8 {
		t.Errorf("unexpected throughput: %+v", res)
	}
	if res.CodeOutput.Commits != 14 || res.CodeOutput.NetLines != 380 || res.CodeOutput.TasksMerged != 8 {
		t.Errorf("unexpected code output: %+v", res.CodeOutput)
	}
	if len(res.Agents) != 1 || res.Agents[0].Agent != "claude-code" {
		t.Errorf("unexpected agent breakdown: %+v", res.Agents)
	}
	if res.TopProjects != nil {
		t.Errorf("project scope must not carry top_projects: %+v", res.TopProjects)
	}
}

func TestGetInsightsGlobal(t *testing.T) {
	s := testServer(&fakeTaskClient{})
	s.insights = &fakeInsightsClient{
		globalFn: func(*pb.GetGlobalInsightsRequest) (*pb.GlobalInsights, error) {
			return &pb.GlobalInsights{
				TasksTotal: 40, TasksSucceeded: 30, TasksFailed: 10,
				TopProjects: []*pb.TopProject{{ProjectName: "demo", Count: 25, NetLines: 900}},
			}, nil
		},
	}

	out, err := handleGetInsights(context.Background(), s, getInsightsArgs{Scope: "global"})
	if err != nil {
		t.Fatalf("handleGetInsights: %v", err)
	}
	res := out.(insightsSummary)
	if res.Scope != "global" || res.ProjectID != "" {
		t.Errorf("unexpected scope/project: %+v", res)
	}
	if res.SuccessRate != 0.75 {
		t.Errorf("success_rate = %v, want 0.75", res.SuccessRate)
	}
	if len(res.TopProjects) != 1 || res.TopProjects[0].Name != "demo" || res.TopProjects[0].NetLines != 900 {
		t.Errorf("unexpected top projects: %+v", res.TopProjects)
	}
}

func TestGetInsightsInvalidScope(t *testing.T) {
	s := testServer(&fakeTaskClient{})

	_, err := handleGetInsights(context.Background(), s, getInsightsArgs{Scope: "galaxy"})
	if err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Errorf("want invalid-scope error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// list_logs / get_log

func TestGetLogTruncation(t *testing.T) {
	long := strings.Repeat("0123456789012345678901234567890123456789012345678\n", 2000) // ~100 KiB
	s := testServer(&fakeTaskClient{})
	s.logs = &fakeLogClient{
		getFn: func(req *pb.GetLogRequest) (*pb.LogContent, error) {
			return &pb.LogContent{
				Entry:   &pb.LogEntry{LogId: req.LogId, TaskNumber: 7},
				Content: long,
			}, nil
		},
	}

	out, err := handleGetLog(context.Background(), s, getLogArgs{LogID: "log-1"})
	if err != nil {
		t.Fatalf("handleGetLog: %v", err)
	}
	res := out.(logContentResult)
	if !res.Truncated || !strings.Contains(res.Note, "truncated") {
		t.Errorf("long transcript must be flagged truncated: %+v", res.Note)
	}
	if len(res.Content) > maxLogBytes {
		t.Errorf("content is %d bytes, cap is %d", len(res.Content), maxLogBytes)
	}
	if !strings.HasSuffix(long, res.Content) {
		t.Error("truncation must keep the tail of the transcript")
	}
	if strings.HasPrefix(res.Content, "\n") || !strings.HasPrefix(res.Content, "0") {
		t.Errorf("truncation must cut on a line boundary, got prefix %q", res.Content[:10])
	}
	if res.Log == nil || res.Log.LogID != "log-1" || res.Log.TaskNumber != 7 {
		t.Errorf("unexpected log metadata: %+v", res.Log)
	}
}

func TestGetLogShortPassthrough(t *testing.T) {
	s := testServer(&fakeTaskClient{})
	s.logs = &fakeLogClient{
		getFn: func(req *pb.GetLogRequest) (*pb.LogContent, error) {
			return &pb.LogContent{Entry: &pb.LogEntry{LogId: req.LogId}, Content: "short session"}, nil
		},
	}

	out, err := handleGetLog(context.Background(), s, getLogArgs{LogID: "log-2"})
	if err != nil {
		t.Fatalf("handleGetLog: %v", err)
	}
	res := out.(logContentResult)
	if res.Truncated || res.Note != "" || res.Content != "short session" {
		t.Errorf("short transcript must pass through untouched: %+v", res)
	}
}

func TestGetLogRequiresLogID(t *testing.T) {
	s := testServer(&fakeTaskClient{})

	_, err := handleGetLog(context.Background(), s, getLogArgs{})
	if err == nil || !strings.Contains(err.Error(), `"log_id" is required`) {
		t.Errorf("want log_id-required error, got: %v", err)
	}
}

func TestListLogs(t *testing.T) {
	s := testServer(&fakeTaskClient{})
	s.logs = &fakeLogClient{
		listFn: func(req *pb.ListLogsRequest) (*pb.LogList, error) {
			if req.ProjectId != "id-demo" {
				t.Errorf("logs listed for %q, want id-demo", req.ProjectId)
			}
			return &pb.LogList{Logs: []*pb.LogEntry{
				{LogId: "log-1", TaskNumber: 7, Agent: "claude-code", Mode: "task", Status: "completed"},
			}}, nil
		},
	}

	out, err := handleListLogs(context.Background(), s, listLogsArgs{})
	if err != nil {
		t.Fatalf("handleListLogs: %v", err)
	}
	rows := out.(struct {
		Logs []logRow `json:"logs"`
	}).Logs
	if len(rows) != 1 || rows[0].LogID != "log-1" || rows[0].TaskNumber != 7 {
		t.Errorf("unexpected rows: %+v", rows)
	}
}

// ---------------------------------------------------------------------------
// --read-only registry filtering

func toolNames(defs []toolDef) []string {
	names := make([]string, 0, len(defs))
	for _, td := range defs {
		names = append(names, td.name)
	}
	sort.Strings(names)
	return names
}

func TestRegisteredToolsReadOnly(t *testing.T) {
	want := []string{
		"get_agent_screen",
		"get_agent_status",
		"get_insights",
		"get_log",
		"get_project",
		"get_task_diff",
		"list_logs",
		"list_projects",
	}

	got := toolNames(registeredTools(true))
	if len(got) != len(want) {
		t.Fatalf("read-only registry = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("read-only registry = %v, want %v", got, want)
		}
	}
}

func TestRegisteredToolsFull(t *testing.T) {
	full := toolNames(registeredTools(false))
	if len(full) != len(allTools()) {
		t.Fatalf("full registry filtered: got %d tools, want %d", len(full), len(allTools()))
	}
	// Write/run tools must be present in the full registry and absent from
	// the read-only one.
	ro := make(map[string]bool)
	for _, n := range toolNames(registeredTools(true)) {
		ro[n] = true
	}
	for _, name := range []string{"create_task", "update_task", "delete_task", "run_task", "run_all", "start_wildfire", "stop_agent", "wait_for_task"} {
		found := false
		for _, n := range full {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("full registry missing %q", name)
		}
		if ro[name] {
			t.Errorf("read-only registry must not contain %q", name)
		}
	}
}
