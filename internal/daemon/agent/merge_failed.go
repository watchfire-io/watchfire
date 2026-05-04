package agent

import (
	"fmt"
	"log"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// emitTaskDoneFailure fires a TASK_FAILED-shaped notification when the
// post-task auto-merge fails (a silent-halt of the run-all queue would
// otherwise leave the user wondering why their chain stopped). Modeled on
// the server-side `emitTaskFailed` but driven from the agent manager's
// monitorProcess where the merge result is available.
//
// Self-gates on the same notification preferences as agent-reported
// failures (master toggle, per-event `task_failed`, per-project mute, and
// quiet hours) via models.ShouldNotify — a user who has muted task
// failures shouldn't suddenly start receiving a different shape of the
// same event. The headless JSONL record is appended unconditionally so
// the tray's Notifications submenu and any post-hoc viewer always see
// the merge failure even when the bus has no subscribers.
//
// The v4.0 Beacon dashboard "needs attention" chip already reacts to
// TASK_FAILED in the Pulse bus, but the chip's primary signal is task
// state (`hasFailedTask` in `gui/src/renderer/src/lib/dashboard-filters.ts`).
// Since merge failures keep `success: true` and only set
// `merge_failure_reason`, the GUI / TUI also include that field in their
// "is this task in a needs-attention state" predicate so the indicator
// lights up regardless of whether a notification was delivered.
func emitTaskDoneFailure(bus *notify.Bus, projectID, projectPath, projectName string, taskNumber int, reason string) {
	if projectID == "" || taskNumber <= 0 {
		return
	}

	settings, _ := config.LoadSettings()
	cfg := models.DefaultNotifications()
	if settings != nil {
		cfg = settings.Defaults.Notifications
	}
	muted := false
	if proj, _ := config.LoadProject(projectPath); proj != nil {
		muted = proj.Notifications.Muted
	}
	if !models.ShouldNotify(models.NotificationTaskFailed, cfg, muted, time.Now().Local()) {
		return
	}

	emittedAt := time.Now().UTC()
	title := fmt.Sprintf("%s — Auto-merge failed for task #%04d", projectName, taskNumber)
	if projectName == "" {
		title = fmt.Sprintf("Auto-merge failed for task #%04d", taskNumber)
	}
	body := reason
	if body == "" {
		body = "merge failed"
	}

	n := notify.Notification{
		ID:         notify.MakeID(notify.KindTaskFailed, projectID, int32(taskNumber), emittedAt),
		Kind:       notify.KindTaskFailed,
		ProjectID:  projectID,
		TaskNumber: int32(taskNumber),
		Title:      title,
		Body:       body,
		EmittedAt:  emittedAt,
	}

	if bus != nil {
		bus.Emit(n)
	}
	if err := notify.AppendLogLine(n); err != nil {
		log.Printf("[merge-failed] failed to append notifications.log for %s task #%04d: %v", projectName, taskNumber, err)
	} else {
		log.Printf("[merge-failed] emitted for project %s task #%04d", projectName, taskNumber)
	}
}
