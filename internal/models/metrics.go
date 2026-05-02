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
}
