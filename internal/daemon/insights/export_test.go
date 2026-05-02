package insights

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// updateGoldens regenerates testdata/* fixture files. Run with:
//
//	go test ./internal/daemon/insights/ -run TestGoldens -update
//
// The flag is local to the package so it doesn't pollute global flag state
// when running `go test ./...`.
var updateGoldens = flag.Bool("update", false, "update golden fixture files in testdata/")

// fixedReportTime is the deterministic "report generated at" stamp used by
// every golden-file test. Picking a wall-clock value rather than time.Now
// keeps the canonical filename stable across CI runs.
var fixedReportTime = time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

// fixtureSingleTask returns the canonical SingleTaskData used by golden
// tests — completed task, success=true, real duration, real branch.
func fixtureSingleTask() SingleTaskData {
	started := time.Date(2026, 4, 28, 9, 30, 0, 0, time.UTC)
	completed := time.Date(2026, 4, 28, 11, 12, 0, 0, time.UTC)
	success := true
	return SingleTaskData{
		ProjectID:      "watchfire-pid",
		ProjectName:    "watchfire",
		TaskNumber:     59,
		Title:          "v6.0 Ember — Export reports (CSV + Markdown)",
		Status:         "done",
		Success:        &success,
		Agent:          "claude-code",
		AgentSessions:  2,
		StartedAt:      &started,
		CompletedAt:    &completed,
		DurationSec:    int64(completed.Sub(started).Seconds()),
		WorktreeBranch: "watchfire/0059",
		Prompt:         "Implement ExportReport RPC, render CSV + Markdown reports for single-task / project / global scopes.",
	}
}

// fixtureProject returns canonical ProjectData spanning a 7-day window
// with three completed tasks across two agents and two failed tasks.
func fixtureProject() ProjectData {
	windowEnd := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	windowStart := windowEnd.AddDate(0, 0, -7)
	return ProjectData{
		ProjectID:   "watchfire-pid",
		ProjectName: "watchfire",
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		KPIs: KPIs{
			TotalDone:      4,
			TotalFailed:    2,
			TotalCreated:   8,
			TotalInFlight:  3,
			AvgDurationSec: 4500,
		},
		Daily: []DayBucket{
			{Date: "2026-04-26", Done: 0, Failed: 1, Created: 2},
			{Date: "2026-04-28", Done: 2, Failed: 0, Created: 3},
			{Date: "2026-04-30", Done: 1, Failed: 1, Created: 1},
			{Date: "2026-05-01", Done: 1, Failed: 0, Created: 2},
		},
		Agents: []AgentBreakdown{
			{Agent: "claude-code", Tasks: 4, Done: 3, Failed: 1, AvgDurationSec: 5400},
			{Agent: "codex", Tasks: 2, Done: 1, Failed: 1, AvgDurationSec: 3000},
		},
	}
}

// fixtureGlobal returns canonical GlobalData covering 3 projects.
func fixtureGlobal() GlobalData {
	windowEnd := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	windowStart := windowEnd.AddDate(0, 0, -7)
	return GlobalData{
		WindowStart:  windowStart,
		WindowEnd:    windowEnd,
		ProjectCount: 3,
		KPIs: KPIs{
			TotalDone:      9,
			TotalFailed:    3,
			TotalCreated:   18,
			TotalInFlight:  5,
			AvgDurationSec: 5100,
		},
		Daily: []DayBucket{
			{Date: "2026-04-26", Done: 1, Failed: 1, Created: 4},
			{Date: "2026-04-28", Done: 4, Failed: 0, Created: 5},
			{Date: "2026-04-30", Done: 2, Failed: 1, Created: 3},
			{Date: "2026-05-01", Done: 2, Failed: 1, Created: 6},
		},
		TopProjects: []ProjectSummary{
			{ProjectID: "watchfire-pid", ProjectName: "watchfire", Done: 4, Failed: 2},
			{ProjectID: "blog-pid", ProjectName: "blog", Done: 3, Failed: 0},
			{ProjectID: "infra-pid", ProjectName: "infra", Done: 2, Failed: 1},
		},
		Agents: []AgentBreakdown{
			{Agent: "claude-code", Tasks: 8, Done: 6, Failed: 2, AvgDurationSec: 5400},
			{Agent: "codex", Tasks: 3, Done: 2, Failed: 1, AvgDurationSec: 3600},
			{Agent: "opencode", Tasks: 1, Done: 1, Failed: 0, AvgDurationSec: 1800},
		},
	}
}

// TestGoldens covers every (scope × format) combination. The fixture
// produces deterministic output; the canonical bytes live in testdata/.
// Use `-update` to regenerate after intentional output changes.
func TestGoldens(t *testing.T) {
	cases := []struct {
		name string
		run  func() (Result, error)
		file string
	}{
		{
			name: "single_task_csv",
			run: func() (Result, error) {
				return ExportSingleTaskFromData(fixtureSingleTask(), FormatCSV, fixedReportTime)
			},
			file: "single_task.csv",
		},
		{
			name: "single_task_md",
			run: func() (Result, error) {
				return ExportSingleTaskFromData(fixtureSingleTask(), FormatMarkdown, fixedReportTime)
			},
			file: "single_task.md",
		},
		{
			name: "project_csv",
			run: func() (Result, error) {
				return ExportProjectFromData(fixtureProject(), FormatCSV, fixedReportTime)
			},
			file: "project.csv",
		},
		{
			name: "project_md",
			run: func() (Result, error) {
				return ExportProjectFromData(fixtureProject(), FormatMarkdown, fixedReportTime)
			},
			file: "project.md",
		},
		{
			name: "global_csv",
			run: func() (Result, error) {
				return ExportGlobalFromData(fixtureGlobal(), FormatCSV, fixedReportTime)
			},
			file: "global.csv",
		},
		{
			name: "global_md",
			run: func() (Result, error) {
				return ExportGlobalFromData(fixtureGlobal(), FormatMarkdown, fixedReportTime)
			},
			file: "global.md",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.run()
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			path := filepath.Join("testdata", tc.file)
			if *updateGoldens {
				if err := os.WriteFile(path, got.Content, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if string(got.Content) != string(want) {
				t.Errorf("content mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", tc.file, got.Content, want)
			}
		})
	}
}

// TestFilenames covers the canonical filename rules — the spec is explicit
// about the shape, and a regression here would silently break user file
// organisation in shared workspaces.
func TestFilenames(t *testing.T) {
	at := fixedReportTime
	cases := []struct {
		name, got, want string
	}{
		{"single task md", SingleTaskFilename(59, FormatMarkdown, at), "watchfire-task-59-2026-05-02.md"},
		{"single task csv", SingleTaskFilename(59, FormatCSV, at), "watchfire-task-59-2026-05-02.csv"},
		{"project md", ProjectFilename("Watchfire", FormatMarkdown, at), "watchfire-project-watchfire-2026-05-02.md"},
		{"project csv slug", ProjectFilename("My Cool Project!", FormatCSV, at), "watchfire-project-my-cool-project-2026-05-02.csv"},
		{"project empty name", ProjectFilename("", FormatCSV, at), "watchfire-project-project-2026-05-02.csv"},
		{"global md", GlobalFilename(FormatMarkdown, at), "watchfire-global-2026-05-02.md"},
		{"global csv", GlobalFilename(FormatCSV, at), "watchfire-global-2026-05-02.csv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q want %q", tc.got, tc.want)
			}
		})
	}
}

// TestParseSingleTaskID covers happy + sad paths for the
// "<project_id>:<task_number>" wire format.
func TestParseSingleTaskID(t *testing.T) {
	cases := []struct {
		in       string
		wantPID  string
		wantNum  int
		wantErr  bool
	}{
		{"watchfire-pid:59", "watchfire-pid", 59, false},
		{"watchfire-pid", "", 0, true},
		{":59", "", 0, true},
		{"watchfire-pid:", "", 0, true},
		{"watchfire-pid:notanumber", "", 0, true},
		{"watchfire-pid:0", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			pid, n, err := parseSingleTaskID(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && (pid != tc.wantPID || n != tc.wantNum) {
				t.Errorf("got (%q, %d) want (%q, %d)", pid, n, tc.wantPID, tc.wantNum)
			}
		})
	}
}
