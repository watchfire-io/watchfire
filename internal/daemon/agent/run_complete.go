package agent

import (
	"fmt"
	"log"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

// runCompleteApplicable reports whether a falling-edge run in the given mode
// should be considered for a RUN_COMPLETE notification. Chat / generate /
// generate-tasks sessions never autonomously chain through tasks, so they are
// excluded outright; the spec deliberately wants RUN_COMPLETE on single-task
// (ModeTask) runs as well as on chained start-all / wildfire runs.
func runCompleteApplicable(mode Mode) bool {
	switch mode {
	case ModeTask, ModeStartAll, ModeWildfire:
		return true
	default:
		return false
	}
}

// runCompleteBody computes the "N tasks done · M failed" body line for a run
// that ended at runEnd, by counting tasks whose UpdatedAt falls within the
// run window [runStartedAt, runEnd]. Returns the empty string when the
// window had zero done and zero failed tasks (the user-aborted-empty case).
func runCompleteBody(tasks []*models.Task, runStartedAt time.Time) string {
	done, failed := countTasksInWindow(tasks, runStartedAt)
	if done+failed == 0 {
		return ""
	}
	return fmt.Sprintf("%d tasks done · %d failed", done, failed)
}

// countTasksInWindow buckets tasks updated within the run window into
// successful (`done && success==true`) and failed (`done && success==false`)
// counts. Tasks whose UpdatedAt is strictly before runStartedAt are skipped
// — those state transitions happened before this run began. Tasks not in
// status `done` are skipped regardless of their UpdatedAt: the only signal
// we count is "the agent finished a task during this run."
func countTasksInWindow(tasks []*models.Task, runStartedAt time.Time) (done, failed int) {
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if t.Status != models.TaskStatusDone {
			continue
		}
		if t.UpdatedAt.Before(runStartedAt) {
			continue
		}
		if t.Success != nil && *t.Success {
			done++
		} else {
			failed++
		}
	}
	return done, failed
}

// emitRunComplete fires a RUN_COMPLETE notification for an autonomous run
// that just ended. It self-gates on:
//  1. mode (chat / generate modes never emit),
//  2. window emptiness (a run with zero done and zero failed tasks does not
//     emit — covers user-aborted runs that never actually did anything), and
//  3. the user's notification preferences (master toggle, per-event toggle
//     `defaults.notifications.events.run_complete`, per-project mute, and
//     quiet hours) via models.ShouldNotify.
//
// Even when the bus is nil (no subscribers wired), the durable JSONL record
// is appended to `~/.watchfire/logs/<project_id>/notifications.log` so the
// tray menu and any post-hoc viewer still see the event.
func emitRunComplete(bus *notify.Bus, projectID, projectName, projectPath string, mode Mode, runStartedAt time.Time) {
	if !runCompleteApplicable(mode) {
		return
	}
	if runStartedAt.IsZero() {
		return
	}
	if projectID == "" || projectPath == "" {
		return
	}

	taskMgr := task.NewManager()
	tasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{IncludeDeleted: false})
	if err != nil {
		log.Printf("[run-complete] failed to load tasks for project %s: %v", projectName, err)
		return
	}
	body := runCompleteBody(tasks, runStartedAt)
	if body == "" {
		// Empty window — skip silently. Spec: "Skip if the run had zero done
		// and zero failed tasks in the window".
		return
	}

	settings, _ := config.LoadSettings()
	project, _ := config.LoadProject(projectPath)

	cfg := models.DefaultNotifications()
	if settings != nil {
		cfg = settings.Defaults.Notifications
	}
	muted := false
	if project != nil {
		muted = project.Notifications.Muted
	}
	if !models.ShouldNotify(models.NotificationRunComplete, cfg, muted, time.Now().Local()) {
		return
	}

	emittedAt := time.Now().UTC()
	n := notify.Notification{
		ID:         notify.MakeID(notify.KindRunComplete, projectID, 0, emittedAt),
		Kind:       notify.KindRunComplete,
		ProjectID:  projectID,
		TaskNumber: 0,
		Title:      fmt.Sprintf("%s — run complete", projectName),
		Body:       body,
		EmittedAt:  emittedAt,
	}

	bus.Emit(n)
	if err := notify.AppendLogLine(n); err != nil {
		log.Printf("[run-complete] failed to append notifications.log for %s: %v", projectName, err)
	} else {
		log.Printf("[run-complete] emitted for project %s: %s", projectName, body)
	}
}
