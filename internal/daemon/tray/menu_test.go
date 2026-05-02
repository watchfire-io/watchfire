package tray

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

// TestBuildMenuFixtures asserts the pure menu builder produces the expected
// tree across the four canonical fixtures the spec calls out: all-idle,
// mixed (attention + working + idle), all-working, and all-failing.
func TestBuildMenuFixtures(t *testing.T) {
	cases := []struct {
		name     string
		in       MenuInputs
		expected string
	}{
		{
			name: "all_idle",
			in: MenuInputs{
				DaemonRunning: true,
				Projects: []ProjectMenuInfo{
					{ProjectID: "p1", ProjectName: "alpha", Status: ProjectIdle},
					{ProjectID: "p2", ProjectName: "beta", Status: ProjectIdle},
					{ProjectID: "p3", ProjectName: "gamma", Status: ProjectIdle},
				},
				NotificationsTodayCount: 0,
			},
			expected: `[
  {"Title":"Watchfire (running)","Disabled":true},
  {"Title":"---","Disabled":true},
  {"Title":"○  Idle (3)","Disabled":true},
  {"Title":"• alpha","OnClick":{"Kind":"focus_main","ProjectID":"p1"}},
  {"Title":"• beta","OnClick":{"Kind":"focus_main","ProjectID":"p2"}},
  {"Title":"• gamma","OnClick":{"Kind":"focus_main","ProjectID":"p3"}},
  {"Title":"---","Disabled":true},
  {"Title":"Open Watchfire","OnClick":{"Kind":"open_watchfire"}},
  {"Title":"Open Dashboard…","OnClick":{"Kind":"open_dashboard"}},
  {"Title":"---","Disabled":true},
  {"Title":"Notifications (0 today) ▸","OnClick":{"Kind":"reload_notifs"},"Children":[{"Title":"No notifications today","Disabled":true}]},
  {"Title":"---","Disabled":true},
  {"Title":"Quit Watchfire","OnClick":{"Kind":"quit_watchfire"}}
]`,
		},
		{
			name: "mixed",
			in: MenuInputs{
				DaemonRunning: true,
				Projects: []ProjectMenuInfo{
					{ProjectID: "p1", ProjectName: "my-app", Status: ProjectFailed, FailedCount: 3},
					{ProjectID: "p2", ProjectName: "api-service", Status: ProjectFailed, FailedCount: 1},
					{ProjectID: "p3", ProjectName: "payments", Status: ProjectWorking,
						CurrentTaskTitle: "Refactor billing", CurrentTaskNumber: 7},
					{ProjectID: "p4", ProjectName: "landing-page", Status: ProjectIdle},
					{ProjectID: "p5", ProjectName: "docs-site", Status: ProjectIdle},
				},
				NotificationsTodayCount: 3,
				Notifications: []NotificationLogEntry{
					{ID: "n1", ProjectID: "p1", ProjectName: "my-app", TaskNumber: 12, Kind: "TASK_FAILED", Title: "task failed", AgeText: "2m ago"},
					{ID: "n2", ProjectID: "p3", ProjectName: "payments", Kind: "RUN_COMPLETE", Title: "run complete", AgeText: "5m ago"},
					{ID: "n3", ProjectID: "p2", ProjectName: "api-service", TaskNumber: 4, Kind: "TASK_FAILED", Title: "task failed", AgeText: "1h ago"},
				},
			},
			expected: `[
  {"Title":"Watchfire (running)","Disabled":true},
  {"Title":"---","Disabled":true},
  {"Title":"⚠  Needs attention (2)","Disabled":true},
  {"Title":"• my-app","Subtitle":"3 failed tasks","OnClick":{"Kind":"focus_tasks","ProjectID":"p1"}},
  {"Title":"• api-service","Subtitle":"1 failed task","OnClick":{"Kind":"focus_tasks","ProjectID":"p2"}},
  {"Title":"●  Working (1)","Disabled":true},
  {"Title":"• payments","Subtitle":"Refactor billing","OnClick":{"Kind":"focus_main","ProjectID":"p3"}},
  {"Title":"○  Idle (2)","Disabled":true},
  {"Title":"• landing-page","OnClick":{"Kind":"focus_main","ProjectID":"p4"}},
  {"Title":"• docs-site","OnClick":{"Kind":"focus_main","ProjectID":"p5"}},
  {"Title":"---","Disabled":true},
  {"Title":"Open Watchfire","OnClick":{"Kind":"open_watchfire"}},
  {"Title":"Open Dashboard…","OnClick":{"Kind":"open_dashboard"}},
  {"Title":"---","Disabled":true},
  {"Title":"Notifications (3 today) ▸","OnClick":{"Kind":"reload_notifs"},"Children":[
    {"Title":"my-app: task failed","Subtitle":"2m ago","OnClick":{"Kind":"focus_task","ProjectID":"p1","TaskNumber":12}},
    {"Title":"payments: run complete","Subtitle":"5m ago","OnClick":{"Kind":"focus_main","ProjectID":"p3"}},
    {"Title":"api-service: task failed","Subtitle":"1h ago","OnClick":{"Kind":"focus_task","ProjectID":"p2","TaskNumber":4}}
  ]},
  {"Title":"---","Disabled":true},
  {"Title":"Quit Watchfire","OnClick":{"Kind":"quit_watchfire"}}
]`,
		},
		{
			name: "all_working",
			in: MenuInputs{
				DaemonRunning: true,
				Projects: []ProjectMenuInfo{
					{ProjectID: "p1", ProjectName: "svc-a", Status: ProjectWorking, CurrentTaskTitle: "Build pipeline"},
					{ProjectID: "p2", ProjectName: "svc-b", Status: ProjectWorking, CurrentTaskTitle: ""},
				},
				NotificationsTodayCount: 0,
			},
			expected: `[
  {"Title":"Watchfire (running)","Disabled":true},
  {"Title":"---","Disabled":true},
  {"Title":"●  Working (2)","Disabled":true},
  {"Title":"• svc-a","Subtitle":"Build pipeline","OnClick":{"Kind":"focus_main","ProjectID":"p1"}},
  {"Title":"• svc-b","OnClick":{"Kind":"focus_main","ProjectID":"p2"}},
  {"Title":"---","Disabled":true},
  {"Title":"Open Watchfire","OnClick":{"Kind":"open_watchfire"}},
  {"Title":"Open Dashboard…","OnClick":{"Kind":"open_dashboard"}},
  {"Title":"---","Disabled":true},
  {"Title":"Notifications (0 today) ▸","OnClick":{"Kind":"reload_notifs"},"Children":[{"Title":"No notifications today","Disabled":true}]},
  {"Title":"---","Disabled":true},
  {"Title":"Quit Watchfire","OnClick":{"Kind":"quit_watchfire"}}
]`,
		},
		{
			name: "all_failing",
			in: MenuInputs{
				DaemonRunning: true,
				Projects: []ProjectMenuInfo{
					{ProjectID: "p1", ProjectName: "x", Status: ProjectFailed, FailedCount: 1},
					{ProjectID: "p2", ProjectName: "y", Status: ProjectFailed, FailedCount: 5},
				},
				NotificationsTodayCount: 1,
				Notifications: []NotificationLogEntry{
					{ID: "n1", ProjectID: "p1", ProjectName: "x", TaskNumber: 99, Kind: "TASK_FAILED", AgeText: "10s"},
				},
				UpdateAvailable: true,
				UpdateVersion:   "5.0.0",
			},
			expected: `[
  {"Title":"Watchfire (running)","Disabled":true},
  {"Title":"---","Disabled":true},
  {"Title":"⚠  Needs attention (2)","Disabled":true},
  {"Title":"• x","Subtitle":"1 failed task","OnClick":{"Kind":"focus_tasks","ProjectID":"p1"}},
  {"Title":"• y","Subtitle":"5 failed tasks","OnClick":{"Kind":"focus_tasks","ProjectID":"p2"}},
  {"Title":"---","Disabled":true},
  {"Title":"Open Watchfire","OnClick":{"Kind":"open_watchfire"}},
  {"Title":"Open Dashboard…","OnClick":{"Kind":"open_dashboard"}},
  {"Title":"---","Disabled":true},
  {"Title":"Notifications (1 today) ▸","OnClick":{"Kind":"reload_notifs"},"Children":[
    {"Title":"x: task failed","Subtitle":"10s","OnClick":{"Kind":"focus_task","ProjectID":"p1","TaskNumber":99}}
  ]},
  {"Title":"---","Disabled":true},
  {"Title":"Update Available — v5.0.0","OnClick":{"Kind":"update_available"}},
  {"Title":"Quit Watchfire","OnClick":{"Kind":"quit_watchfire"}}
]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeJSON(t, BuildMenu(tc.in))
			want := normalizeRawJSON(t, tc.expected)
			if got != want {
				t.Errorf("BuildMenu mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}

// TestBuildMenuIdleOverflow asserts the >50 idle case collapses into the
// "… and N more idle projects" overflow row.
func TestBuildMenuIdleOverflow(t *testing.T) {
	in := MenuInputs{DaemonRunning: true}
	for i := 0; i < 53; i++ {
		in.Projects = append(in.Projects, ProjectMenuInfo{
			ProjectID:   "p",
			ProjectName: "x",
			Status:      ProjectIdle,
		})
	}
	tree := BuildMenu(in)

	// Find the overflow row.
	var overflow string
	for _, n := range tree {
		if n.Disabled && len(n.Title) > 0 && n.Title[0] == 0xE2 /* "…" lead byte */ {
			overflow = n.Title
		}
	}
	if overflow != "… and 3 more idle projects" {
		t.Fatalf("overflow row = %q, want '… and 3 more idle projects'", overflow)
	}

	// Count visible idle rows.
	idleRows := 0
	inIdle := false
	for _, n := range tree {
		if n.Disabled && len(n.Title) > 0 && n.Title[0] == 0xE2 && len(n.Title) > 3 && n.Title[3] == 0x20 {
			// section header "○  Idle (..)" — the first " " after the marker
			inIdle = true
			continue
		}
		if n.Title == "---" {
			inIdle = false
			continue
		}
		if inIdle && !n.Disabled {
			idleRows++
		}
	}
	if idleRows != MaxIdleProjects {
		t.Fatalf("idle rows shown = %d, want %d", idleRows, MaxIdleProjects)
	}
}

// TestBuildMenuRebuildGoroutineBaseline runs the pure builder 100 times and
// asserts no goroutine leak — BuildMenu allocates nothing of substance and
// must NOT spawn helpers. The whole point of extracting the menu builder is
// that it's free to call.
func TestBuildMenuRebuildGoroutineBaseline(t *testing.T) {
	// Quiesce: let any previously-started goroutines from sibling tests
	// settle before sampling the baseline.
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	in := MenuInputs{DaemonRunning: true}
	for i := 0; i < 5; i++ {
		in.Projects = append(in.Projects, ProjectMenuInfo{
			ProjectID:   "p", ProjectName: "x", Status: ProjectIdle,
		})
	}
	for i := 0; i < 100; i++ {
		_ = BuildMenu(in)
	}

	// Allow any opportunistic teardown to land.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > baseline+1 {
		t.Fatalf("goroutine leak: baseline=%d after=%d", baseline, after)
	}
}

// normalizeJSON marshals a tree to compact JSON, dropping zero-valued fields
// so the test fixtures can stay readable.
func normalizeJSON(t *testing.T, tree []MenuNode) string {
	t.Helper()
	out := make([]map[string]any, 0, len(tree))
	for _, n := range tree {
		out = append(out, nodeToMap(n))
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func nodeToMap(n MenuNode) map[string]any {
	m := map[string]any{"Title": n.Title}
	if n.Subtitle != "" {
		m["Subtitle"] = n.Subtitle
	}
	if n.Disabled {
		m["Disabled"] = true
	}
	if n.OnClick.Kind != "" {
		click := map[string]any{"Kind": string(n.OnClick.Kind)}
		if n.OnClick.ProjectID != "" {
			click["ProjectID"] = n.OnClick.ProjectID
		}
		if n.OnClick.TaskNumber != 0 {
			click["TaskNumber"] = int(n.OnClick.TaskNumber)
		}
		m["OnClick"] = click
	}
	if len(n.Children) > 0 {
		kids := make([]map[string]any, 0, len(n.Children))
		for _, c := range n.Children {
			kids = append(kids, nodeToMap(c))
		}
		m["Children"] = kids
	}
	return m
}

func normalizeRawJSON(t *testing.T, raw string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("normalizeRawJSON unmarshal: %v\nraw:\n%s", err, raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("normalizeRawJSON marshal: %v", err)
	}
	return string(b)
}
