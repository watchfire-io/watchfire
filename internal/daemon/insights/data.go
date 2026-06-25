// Package insights renders v6.0 Ember reports — single-task / per-project /
// fleet rollups in CSV + Markdown. The package is split into a data layer
// (this file: shapes that are easy to fixture for golden tests) and a
// rendering layer (csv.go + markdown.go) that consumes those shapes.
//
// Render-from-data is the testable seam: production loads tasks/projects
// from disk and builds these shapes; tests construct them inline so a single
// committed fixture covers a (scope × format) combo without depending on
// repo state.
package insights

import "time"

// Scope identifies which shape of report is being rendered.
type Scope int

// Scope values.
const (
	ScopeSingleTask Scope = iota
	ScopeProject
	ScopeGlobal
)

// Format selects CSV vs Markdown.
type Format int

// Format values match proto/watchfire.proto:ExportFormat (CSV=0, MARKDOWN=1).
const (
	FormatCSV Format = iota
	FormatMarkdown
)

// SingleTaskData covers one task. Used for ScopeSingleTask. Keeps everything
// needed for a paste-into-PR description: identity, outcome, timing, the
// worktree branch (so reviewers can `git log <branch>` the diff).
type SingleTaskData struct {
	ProjectID      string
	ProjectName    string
	TaskNumber     int
	Title          string
	Status         string // "draft" | "ready" | "done"
	Success        *bool  // nil when not done
	FailureReason  string
	Agent          string
	AgentSessions  int
	StartedAt      *time.Time
	CompletedAt    *time.Time
	DurationSec    int64 // CompletedAt - StartedAt; 0 when one of them is missing
	WorktreeBranch string
	Prompt         string // First 240 chars of the prompt, for context

	// v8.0 Inferno — code-output stats pulled from the task's
	// `<n>.metrics.yaml` (task 0114). HasCode reports whether the metrics
	// file carried any code-output signal; when false the numbers are all
	// zero and the export renders an em-dash rather than "0".
	Commits      int
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
	NetLines     int
	Merged       bool
	MergeKind    string // "silent" | "auto_pr" | ""
	HasCode      bool
}

// AgentBreakdown row — one per backend agent that touched tasks in the window.
type AgentBreakdown struct {
	Agent          string
	Done           int
	Failed         int
	AvgDurationSec int64
	Tasks          int

	// v8.0 Inferno — shipped-code totals for this agent across the window,
	// so reports compare output-per-agent, not only task count.
	Commits      int
	LinesAdded   int
	LinesRemoved int
}

// DayBucket — one calendar-day worth of activity counts.
type DayBucket struct {
	Date    string // YYYY-MM-DD (local zone)
	Done    int
	Failed  int
	Created int
}

// KPIs — the shared headline numbers for ScopeProject + ScopeGlobal reports.
type KPIs struct {
	TotalDone      int
	TotalFailed    int
	TotalCreated   int
	TotalInFlight  int
	AvgDurationSec int64
}

// CodeOutput — the shared "what shipped" totals for ScopeProject +
// ScopeGlobal reports (v8.0 Inferno, task 0118). Rolled up from the
// per-task `<n>.metrics.yaml` code fields over completed-in-window tasks.
// MetricsMissingCode tallies completed tasks whose metrics carried no
// code signal so the report can honestly caveat "based on N of M tasks"
// (mirror of KPIs/TasksMissingCost).
type CodeOutput struct {
	TotalCommits       int
	TotalFilesChanged  int
	TotalLinesAdded    int
	TotalLinesRemoved  int
	NetLines           int
	TasksMerged        int
	TasksViaPR         int
	MetricsMissingCode int
}

// ProjectData covers one project across a window. Used for ScopeProject and
// embedded inside GlobalData.TopProjects.
type ProjectData struct {
	ProjectID   string
	ProjectName string
	WindowStart time.Time
	WindowEnd   time.Time

	KPIs   KPIs
	Code   CodeOutput
	Daily  []DayBucket
	Agents []AgentBreakdown
}

// GlobalData covers fleet-wide rollups across every registered project.
type GlobalData struct {
	WindowStart time.Time
	WindowEnd   time.Time

	KPIs         KPIs
	Code         CodeOutput
	Daily        []DayBucket
	TopProjects  []ProjectSummary
	Agents       []AgentBreakdown
	ProjectCount int
}

// ProjectSummary is one row of the GlobalData "top projects" table.
type ProjectSummary struct {
	ProjectID   string
	ProjectName string
	Done        int
	Failed      int

	// v8.0 Inferno — per-project shipped-code totals over the window so the
	// fleet report can rank/compare projects by churn, not only task count.
	Commits      int
	LinesAdded   int
	LinesRemoved int
	NetLines     int
	Merges       int
}
