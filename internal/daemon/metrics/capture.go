package metrics

import (
	"log"
	"os"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// BuildBaseMetrics derives the duration-only fields from the canonical
// task YAML. Token + cost remain nil for the caller to fill in via a
// per-backend Parser. Returns nil if the task is missing required fields.
func BuildBaseMetrics(projectID string, t *models.Task) *models.TaskMetrics {
	if t == nil {
		return nil
	}
	exit := exitReason(t)
	return &models.TaskMetrics{
		TaskNumber: t.TaskNumber,
		ProjectID:  projectID,
		Agent:      t.Agent,
		DurationMs: durationMs(t),
		ExitReason: exit,
		CapturedAt: time.Now().UTC(),
	}
}

// CaptureWaitConfig tunes how long Capture waits for an in-flight session
// log to appear before giving up and writing duration-only metrics. Kept
// as variables (not constants) so tests can shrink the window.
var (
	CaptureWaitTotal = 6 * time.Second
	CaptureWaitStep  = 250 * time.Millisecond
)

// CaptureFromTask is the watcher-driven entry point. The session log is
// written asynchronously by `agent.Manager.writeSessionLog` after the
// agent process exits, so this helper polls briefly for the log to
// appear before parsing it. A missed log just means duration-only
// metrics — still better than nothing.
func CaptureFromTask(projectPath, projectID string, t *models.Task) {
	if t == nil || t.TaskNumber <= 0 {
		return
	}
	sessionLogPath := waitForSessionLog(projectID, t.TaskNumber)
	Capture(projectPath, projectID, sessionLogPath, t)
}

func waitForSessionLog(projectID string, taskNumber int) string {
	deadline := time.Now().Add(CaptureWaitTotal)
	for {
		path := LocateSessionLog(projectID, taskNumber)
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		if !time.Now().Before(deadline) {
			return ""
		}
		time.Sleep(CaptureWaitStep)
	}
}

// Capture builds the base metrics for `t`, runs the per-backend parser
// against `sessionLogPath`, and persists the resulting `<n>.metrics.yaml`
// next to the canonical task file. Failures degrade silently (we'd
// rather have duration-only metrics than no metrics) — parser errors are
// logged at WARN.
//
// Safe to invoke from a goroutine; all I/O is bounded to the project
// directory + session log.
func Capture(projectPath, projectID, sessionLogPath string, t *models.Task) {
	m := BuildBaseMetrics(projectID, t)
	if m == nil {
		return
	}

	if sessionLogPath != "" {
		parser := GetParser(t.Agent)
		in, out, cost, err := parser.Parse(sessionLogPath)
		if err != nil {
			log.Printf("[metrics] parser %q failed for project %s task #%04d: %v — writing duration-only metrics", t.Agent, projectID, t.TaskNumber, err)
		} else {
			m.TokensIn = in
			m.TokensOut = out
			m.CostUSD = cost
		}
	}

	if err := config.WriteMetrics(projectPath, m); err != nil {
		log.Printf("[metrics] failed to persist metrics for project %s task #%04d: %v", projectID, t.TaskNumber, err)
	}
}

func exitReason(t *models.Task) models.MetricsExitReason {
	if t.Status != models.TaskStatusDone {
		return models.MetricsExitStopped
	}
	if t.Success != nil && !*t.Success {
		return models.MetricsExitFailed
	}
	return models.MetricsExitCompleted
}

func durationMs(t *models.Task) int64 {
	if t == nil || t.StartedAt == nil {
		return 0
	}
	end := t.UpdatedAt
	if t.CompletedAt != nil {
		end = *t.CompletedAt
	}
	if end.Before(*t.StartedAt) {
		return 0
	}
	return end.Sub(*t.StartedAt).Milliseconds()
}
