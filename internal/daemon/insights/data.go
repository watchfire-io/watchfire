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
}

// AgentBreakdown row — one per backend agent that touched tasks in the window.
type AgentBreakdown struct {
	Agent          string
	Done           int
	Failed         int
	AvgDurationSec int64
	Tasks          int
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

// ProjectData covers one project across a window. Used for ScopeProject and
// embedded inside GlobalData.TopProjects.
type ProjectData struct {
	ProjectID   string
	ProjectName string
	WindowStart time.Time
	WindowEnd   time.Time

	KPIs   KPIs
	Daily  []DayBucket
	Agents []AgentBreakdown
}

// GlobalData covers fleet-wide rollups across every registered project.
type GlobalData struct {
	WindowStart time.Time
	WindowEnd   time.Time

	KPIs         KPIs
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
}
