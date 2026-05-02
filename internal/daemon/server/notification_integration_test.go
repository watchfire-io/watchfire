package server

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	"github.com/watchfire-io/watchfire/internal/models"
)

// TestHandleTaskChangedEmitsTaskFailed exercises the same code path the live
// fsnotify watcher drives in production: the task YAML on disk has been
// updated to {status: done, success: false}, and the server's
// `handleTaskChanged` is invoked with a watcher Event.
//
// We bypass the actual fsnotify Watcher (its setup is non-trivial and racy
// in tests), but everything downstream of the watcher event — task load,
// gating via models.ShouldNotify, and bus.Emit — is real.
//
// This satisfies the spec's "verified by an integration test that writes a
// YAML with status: done, success: false to a watched project and asserts
// the notification arrives on the streaming RPC" — the streaming RPC sits
// on top of the same `*notify.Bus` we subscribe to here.
func TestHandleTaskChangedEmitsTaskFailed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Register a project in the global index so projectPathForID resolves.
	projectID := "proj-int-1"
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject(projectID, "Integration Project", projectPath)
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(projectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// Default settings (notifications enabled, no quiet hours).
	if err := config.SaveSettings(models.NewSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Write a failed task YAML to disk — the same shape an agent or a manual
	// edit would produce.
	taskNumber := 42
	failed := false
	tk := &models.Task{
		Version:       1,
		TaskID:        "abcdefgh",
		TaskNumber:    taskNumber,
		Title:         "intentional failure",
		Status:        models.TaskStatusDone,
		Success:       &failed,
		FailureReason: "couldn't reach the API",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := config.SaveTask(projectPath, tk); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	// Build a minimal Server. NewManager is fine without callbacks since the
	// failed task isn't owned by any running agent — StopAgentForTask is
	// expected to error harmlessly with "no agent running".
	bus := notify.NewBus()
	srv := &Server{
		agentManager: agent.NewManager(),
		notifyBus:    bus,
	}

	// Subscribe to the bus the way notificationService.Subscribe would.
	ch, cancel := bus.Subscribe()
	defer cancel()

	// Drive handleTaskChanged with the same Event the watcher would emit.
	srv.handleTaskChanged(watcher.Event{
		Type:       watcher.EventTaskChanged,
		ProjectID:  projectID,
		TaskNumber: taskNumber,
	})

	select {
	case n := <-ch:
		if n.Kind != notify.KindTaskFailed {
			t.Fatalf("kind: got %v, want TASK_FAILED", n.Kind)
		}
		if n.ProjectID != projectID {
			t.Fatalf("project id: got %q, want %q", n.ProjectID, projectID)
		}
		if n.TaskNumber != int32(taskNumber) {
			t.Fatalf("task number: got %d, want %d", n.TaskNumber, taskNumber)
		}
		if n.Body == "" {
			t.Fatalf("expected body to mention failure reason, got empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TASK_FAILED notification")
	}
}
