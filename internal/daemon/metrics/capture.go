package metrics

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// metricsFileMu serializes the read-modify-write access to a task's
// `<n>.metrics.yaml`. The token capture (Capture, watcher-driven) and the
// code-output capture (RecordCodeStats, merge-path-driven) run concurrently
// in the daemon and touch disjoint field sets of the same file; the lock +
// read-modify-write in both keeps whichever writes second from clobbering
// the other's fields. A single process-wide mutex is plenty — metrics
// writes are rare and fast.
var metricsFileMu sync.Mutex

// CodeStats carries the code-output numbers computed by the task-done merge
// path (`agent.HandleTaskDone`) from git + the diff package, ready to be
// merged into the task's metrics YAML by RecordCodeStats.
type CodeStats struct {
	Commits      int
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
	Merged       bool
	MergeKind    models.MergeKind
}

// RecordCodeStats merges code-output stats into a task's `<n>.metrics.yaml`,
// preserving the token/cost fields the watcher-driven Capture may have
// already written (and vice-versa). Best-effort: on any read/write error it
// logs and returns — a metrics hiccup must never strand or fail the merge.
//
// Safe to call concurrently with Capture; both take metricsFileMu.
func RecordCodeStats(projectPath, projectID string, t *models.Task, cs CodeStats) {
	if t == nil || t.TaskNumber <= 0 {
		return
	}

	metricsFileMu.Lock()
	defer metricsFileMu.Unlock()

	m, err := config.ReadMetrics(projectPath, t.TaskNumber)
	if err != nil || m == nil {
		// No metrics file yet (Capture hasn't landed) — build the base
		// record so the code stats aren't lost. Capture will later read
		// this back and graft its token fields on top.
		m = BuildBaseMetrics(projectID, t)
	}
	if m == nil {
		return
	}

	m.Commits = cs.Commits
	m.FilesChanged = cs.FilesChanged
	m.LinesAdded = cs.LinesAdded
	m.LinesRemoved = cs.LinesRemoved
	m.NetLines = cs.LinesAdded - cs.LinesRemoved
	m.Merged = cs.Merged
	m.MergeKind = cs.MergeKind

	if writeErr := config.WriteMetrics(projectPath, m); writeErr != nil {
		log.Printf("[metrics] failed to persist code stats for project %s task #%04d: %v", projectID, t.TaskNumber, writeErr)
	}
}

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

	metricsFileMu.Lock()
	defer metricsFileMu.Unlock()

	// Preserve any code-output stats RecordCodeStats may have already written
	// from the (concurrent) merge path — this fresh record only carries
	// duration + token/cost fields.
	if existing, _ := config.ReadMetrics(projectPath, t.TaskNumber); existing != nil {
		m.Commits = existing.Commits
		m.FilesChanged = existing.FilesChanged
		m.LinesAdded = existing.LinesAdded
		m.LinesRemoved = existing.LinesRemoved
		m.NetLines = existing.NetLines
		m.Merged = existing.Merged
		m.MergeKind = existing.MergeKind
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
