// v10.0 Torch, task 0141 — the read-only session view the Telegram
// bridge's watch mode observes agents through. This adapter is the
// seam that keeps the telegram package away from the PTY: it exposes
// screen snapshots and task outcomes only — no SendInput, no Resize
// (the telegram package has a guard test asserting exactly that).
package server

import (
	"fmt"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/telegram"
	"github.com/watchfire-io/watchfire/internal/models"
)

// agentSessionSource adapts agent.Manager to telegram.SessionSource.
type agentSessionSource struct {
	mgr *agent.Manager
}

// ActiveSession maps the manager's running agent (if any) onto the
// watch-mode view. Snapshot closes over the Process but only its vt10x
// screen read — the closure is safe to call after the process exits.
func (s *agentSessionSource) ActiveSession(projectID string) (*telegram.WatchedSession, bool) {
	if s == nil || s.mgr == nil {
		return nil, false
	}
	ag, ok := s.mgr.GetAgent(projectID)
	if !ok || ag == nil || ag.Process == nil {
		return nil, false
	}
	proc := ag.Process
	workDir := ag.WorktreePath
	if workDir == "" {
		workDir = ag.ProjectPath
	}
	return &telegram.WatchedSession{
		ProjectID:   ag.ProjectID,
		ProjectPath: ag.ProjectPath,
		Mode:        string(ag.Mode),
		TaskNumber:  ag.TaskNumber,
		TaskTitle:   ag.TaskTitle,
		BackendName: ag.BackendName,
		WorkDir:     workDir,
		SessionName: ag.SessionName,
		StartedAt:   ag.StartedAt,
		Done:        proc.Done(),
		Snapshot: func() []string {
			su := proc.SnapshotScreen()
			if su == nil {
				return nil
			}
			return su.Lines
		},
	}, true
}

// TaskOutcome reads the task's final state from its YAML — the same
// artifact the completion watcher and merge hook maintain, so watch
// mode reuses existing signals without touching their behavior.
func (s *agentSessionSource) TaskOutcome(projectPath string, taskNumber int) (telegram.TaskOutcome, error) {
	t, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		return telegram.TaskOutcome{}, err
	}
	if t == nil {
		return telegram.TaskOutcome{}, fmt.Errorf("task #%04d not found", taskNumber)
	}
	oc := telegram.TaskOutcome{
		Done:               t.Status == models.TaskStatusDone,
		FailureReason:      t.FailureReason,
		MergeFailureReason: t.MergeFailureReason,
	}
	if t.Success != nil {
		oc.Success = *t.Success
	}
	// "Merged" is best-effort: a successful done task with no recorded
	// merge failure on an auto-merge project landed on the default
	// branch (auto-PR projects suppress the local merge but that path
	// also records no failure — the marker still reads correctly as
	// "the work left the worktree").
	if oc.Done && oc.Success && oc.MergeFailureReason == "" {
		if proj, projErr := config.LoadProject(projectPath); projErr == nil && proj != nil && proj.AutoMerge {
			oc.Merged = true
		}
	}
	return oc, nil
}
