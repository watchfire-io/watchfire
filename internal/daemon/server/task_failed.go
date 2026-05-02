package server

import (
	"fmt"
	"log"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// emitTaskFailed fires a TASK_FAILED notification when a task transitions to
// status=done with success=false. The function is task-state-driven (called
// from handleTaskChanged) rather than agent-state-driven so a manual edit of
// the YAML file produces the same notification — the dashboard's "needs
// attention" treatment from task 0041 tells the user *if* they look at the
// window, this tells them *when they don't*.
//
// Self-gates on:
//   - master toggle (defaults.notifications.enabled),
//   - per-event toggle (defaults.notifications.events.task_failed),
//   - per-project mute (project.notifications.muted), and
//   - quiet hours (defaults.notifications.quiet_hours)
//
// via models.ShouldNotify. The headless JSONL record is appended to
// ~/.watchfire/logs/<project_id>/notifications.log regardless of whether the
// bus has subscribers, so the tray's notifications menu (task 0052) and any
// post-hoc viewer always see the event.
func emitTaskFailed(bus *notify.Bus, projectID, projectPath, projectName string, t *models.Task) {
	if t == nil {
		return
	}
	if t.Status != models.TaskStatusDone {
		return
	}
	if t.Success == nil || *t.Success {
		return
	}
	if projectID == "" {
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
	title := fmt.Sprintf("%s — task #%04d failed", projectName, t.TaskNumber)
	if projectName == "" {
		title = fmt.Sprintf("Task #%04d failed", t.TaskNumber)
	}
	body := t.Title
	if t.FailureReason != "" {
		body = fmt.Sprintf("%s — %s", t.Title, t.FailureReason)
	}

	n := notify.Notification{
		ID:         notify.MakeID(notify.KindTaskFailed, projectID, int32(t.TaskNumber), emittedAt),
		Kind:       notify.KindTaskFailed,
		ProjectID:  projectID,
		TaskNumber: int32(t.TaskNumber),
		Title:      title,
		Body:       body,
		EmittedAt:  emittedAt,
	}

	bus.Emit(n)
	if err := notify.AppendLogLine(n); err != nil {
		log.Printf("[task-failed] failed to append notifications.log for %s task #%04d: %v", projectName, t.TaskNumber, err)
	}
}
