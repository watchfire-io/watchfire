package models

import "time"

// MetricsExitReason classifies how a task ended for metric reporting.
type MetricsExitReason string

// Exit reasons recorded in TaskMetrics.ExitReason.
const (
	MetricsExitCompleted MetricsExitReason = "completed"
	MetricsExitFailed    MetricsExitReason = "failed"
	MetricsExitStopped   MetricsExitReason = "stopped"
	MetricsExitTimeout   MetricsExitReason = "timeout"
)

// MergeKind records which task-completion merge path landed the work.
type MergeKind string

// Merge kinds recorded in TaskMetrics.MergeKind.
const (
	// MergeKindSilent is the default `git merge --no-ff` of the task branch
	// into the project's default branch.
	MergeKindSilent MergeKind = "silent"
	// MergeKindAutoPR is the v7.0 Relay GitHub auto-PR path: the branch was
	// pushed and a PR opened instead of merged locally.
	MergeKindAutoPR MergeKind = "auto_pr"
)

// TaskMetrics is the v6.0 Ember per-task metrics record persisted next to
// the canonical task YAML as `<n>.metrics.yaml`. Token + cost fields are
// pointers so a backend that doesn't expose them can leave the field nil
// (vs. zero, which would skew rollups).
type TaskMetrics struct {
	TaskNumber int               `yaml:"task_number"`
	ProjectID  string            `yaml:"project_id"`
	Agent      string            `yaml:"agent"`
	DurationMs int64             `yaml:"duration_ms"`
	TokensIn   *int64            `yaml:"tokens_in,omitempty"`
	TokensOut  *int64            `yaml:"tokens_out,omitempty"`
	CostUSD    *float64          `yaml:"cost_usd,omitempty"`
	ExitReason MetricsExitReason `yaml:"exit_reason"`
	CapturedAt time.Time         `yaml:"captured_at"`

	// v8.0 Inferno — code-output stats, computed by the task-done merge path
	// from git + the diff package before worktree cleanup. They measure what
	// the agent SHIPPED, not just task throughput. Absent on metrics files
	// written before v8.0; those read back as zeros (backward compatible).
	Commits      int       `yaml:"commits"`
	FilesChanged int       `yaml:"files_changed"`
	LinesAdded   int       `yaml:"lines_added"`
	LinesRemoved int       `yaml:"lines_removed"`
	NetLines     int       `yaml:"net_lines"`
	Merged       bool      `yaml:"merged"`
	MergeKind    MergeKind `yaml:"merge_kind,omitempty"`
}
