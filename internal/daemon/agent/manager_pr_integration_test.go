package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	gitpkg "github.com/watchfire-io/watchfire/internal/daemon/git"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// taskDoneFixture wires up a taskDoneFns where every side-effecting call is
// recorded for assertion. The default fixture has GitHub auto-PR enabled,
// AutoMerge on, AutoDeleteBranch on, and OpenPR returning success — each
// test mutates only the field(s) it cares about.
type taskDoneFixture struct {
	loadProjectCalled      int
	loadIntegrationsCalled int
	openPRCalled           int
	mergeCalled            int
	removeCalled           int
	notifyCalled           int

	openPRErr     error
	mergeErr      error
	mergeChanged  bool // first return of MergeWorktree
	removeMerged  bool // captured from RemoveWorktree(_, _, merged)
	notifications []notify.Notification
	openPROptions gitpkg.OpenPROptions
	openPRResult  *gitpkg.PRResult
	autoPREnabled bool
	githubScopes  []string
	taskCompleted *time.Time
}

func (f *taskDoneFixture) fns() taskDoneFns {
	now := time.Now().UTC()
	completedAt := &now
	if f.taskCompleted != nil {
		completedAt = f.taskCompleted
	}

	return taskDoneFns{
		LoadProject: func(string) (*models.Project, error) {
			f.loadProjectCalled++
			return &models.Project{
				ProjectID:        "proj-1",
				Name:             "demo",
				DefaultAgent:     "claude-code",
				AutoMerge:        true,
				AutoDeleteBranch: true,
			}, nil
		},
		LoadTask: func(string, int) (*models.Task, error) {
			success := true
			return &models.Task{
				TaskNumber:         42,
				Title:              "demo task",
				Prompt:             "do thing",
				AcceptanceCriteria: "thing done",
				Status:             models.TaskStatusDone,
				Success:            &success,
				CompletedAt:        completedAt,
			}, nil
		},
		LoadIntegrations: func() (*models.IntegrationsConfig, error) {
			f.loadIntegrationsCalled++
			cfg := &models.IntegrationsConfig{}
			cfg.GitHub.Enabled = f.autoPREnabled
			cfg.GitHub.DraftDefault = true
			cfg.GitHub.ProjectScopes = f.githubScopes
			return cfg, nil
		},
		OpenPR: func(_ context.Context, opts gitpkg.OpenPROptions) (*gitpkg.PRResult, error) {
			f.openPRCalled++
			f.openPROptions = opts
			if f.openPRErr != nil {
				return nil, f.openPRErr
			}
			res := &gitpkg.PRResult{
				URL:    "https://github.com/owner/repo/pull/42",
				Number: 42,
				Branch: "watchfire/0042",
			}
			f.openPRResult = res
			return res, nil
		},
		MergeWorktree: func(string, int) (bool, error) {
			f.mergeCalled++
			return f.mergeChanged, f.mergeErr
		},
		RemoveWorktree: func(_ string, _ int, merged bool) error {
			f.removeCalled++
			f.removeMerged = merged
			return nil
		},
		EmitNotification: func(_ *notify.Bus, n notify.Notification) error {
			f.notifyCalled++
			f.notifications = append(f.notifications, n)
			return nil
		},
	}
}

// captureLogs swaps log.Output for the duration of the test and returns the
// captured bytes. Used to assert the WARN-once-per-project-lifetime semantics.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

// TestHandleTaskDoneAutoPRHappyPath — auto-PR enabled + OpenPR success →
// no local merge, worktree cleaned, notification emitted.
func TestHandleTaskDoneAutoPRHappyPath(t *testing.T) {
	resetGHFallbackWarnedForTest()
	f := &taskDoneFixture{autoPREnabled: true}

	cont := handleTaskDoneWith(f.fns(), "/proj", 42, "/wt", nil)
	if !cont {
		t.Errorf("handleTaskDoneWith returned false, want true (continue chaining)")
	}
	if f.openPRCalled != 1 {
		t.Errorf("OpenPR called %d times, want 1", f.openPRCalled)
	}
	if f.mergeCalled != 0 {
		t.Errorf("MergeWorktree called %d times, want 0 (auto-PR should suppress merge)", f.mergeCalled)
	}
	if f.removeCalled != 1 {
		t.Errorf("RemoveWorktree called %d times, want 1", f.removeCalled)
	}
	if !f.removeMerged {
		t.Errorf("RemoveWorktree called with merged=false, want true (PR pushed branch upstream)")
	}
	if f.notifyCalled != 1 {
		t.Errorf("EmitNotification called %d times, want 1", f.notifyCalled)
	}
	if len(f.notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(f.notifications))
	}
	got := f.notifications[0]
	if got.Kind != notify.KindRunComplete {
		t.Errorf("notification kind = %s, want RUN_COMPLETE", got.Kind)
	}
	if !strings.Contains(got.Body, "https://github.com/owner/repo/pull/42") {
		t.Errorf("notification body missing PR URL: %q", got.Body)
	}
	if got.ProjectID != "proj-1" || got.TaskNumber != 42 {
		t.Errorf("notification fields wrong: project=%q task=%d", got.ProjectID, got.TaskNumber)
	}

	// Verify the task metadata flowed into OpenPR.
	if f.openPROptions.TaskTitle != "demo task" {
		t.Errorf("OpenPR opts.TaskTitle = %q, want %q", f.openPROptions.TaskTitle, "demo task")
	}
	if !f.openPROptions.DraftDefault {
		t.Errorf("OpenPR opts.DraftDefault = false, want true")
	}
	if f.openPROptions.Agent != "claude-code" {
		t.Errorf("OpenPR opts.Agent = %q, want claude-code", f.openPROptions.Agent)
	}
}

// TestHandleTaskDoneAutoPRGHUnavailableFallsBack — gh unavailable falls
// through to silent merge and emits a single WARN per project lifetime.
func TestHandleTaskDoneAutoPRGHUnavailableFallsBack(t *testing.T) {
	resetGHFallbackWarnedForTest()
	logBuf := captureLogs(t)

	f := &taskDoneFixture{
		autoPREnabled: true,
		openPRErr:     errors.Join(gitpkg.ErrGHUnavailable, errors.New("gh: command not found")),
		mergeChanged:  true,
	}

	cont := handleTaskDoneWith(f.fns(), "/proj", 42, "/wt", nil)
	if !cont {
		t.Errorf("returned false, want true (silent-merge fallback succeeded)")
	}
	if f.openPRCalled != 1 {
		t.Errorf("OpenPR called %d times, want 1", f.openPRCalled)
	}
	if f.mergeCalled != 1 {
		t.Errorf("MergeWorktree called %d times, want 1 (fallback path)", f.mergeCalled)
	}
	if f.removeCalled != 1 {
		t.Errorf("RemoveWorktree called %d times, want 1", f.removeCalled)
	}
	if f.notifyCalled != 0 {
		t.Errorf("EmitNotification called %d times, want 0 (no PR opened)", f.notifyCalled)
	}

	out := logBuf.String()
	want := "WARN [auto-pr] project proj-1: github auto-PR enabled but gh CLI unavailable"
	if !strings.Contains(out, want) {
		t.Errorf("log missing expected WARN: %q\n--- log ---\n%s", want, out)
	}

	// Second invocation: same project, same error — must NOT emit another WARN.
	logBuf.Reset()
	f2 := &taskDoneFixture{
		autoPREnabled: true,
		openPRErr:     errors.Join(gitpkg.ErrGHUnavailable, errors.New("gh: command not found")),
		mergeChanged:  true,
	}
	_ = handleTaskDoneWith(f2.fns(), "/proj", 43, "/wt2", nil)
	if strings.Contains(logBuf.String(), "github auto-PR enabled but gh CLI unavailable") {
		t.Errorf("second invocation re-emitted the WARN; should be deduped per project lifetime\n--- log ---\n%s", logBuf.String())
	}
}

// TestHandleTaskDoneAutoPRDisabledUnchanged — when auto-PR is not enabled,
// the existing silent-merge path runs unchanged and OpenPR is not called.
func TestHandleTaskDoneAutoPRDisabledUnchanged(t *testing.T) {
	resetGHFallbackWarnedForTest()
	f := &taskDoneFixture{autoPREnabled: false, mergeChanged: true}

	cont := handleTaskDoneWith(f.fns(), "/proj", 42, "/wt", nil)
	if !cont {
		t.Errorf("returned false, want true")
	}
	if f.openPRCalled != 0 {
		t.Errorf("OpenPR called %d times, want 0 (auto-PR disabled)", f.openPRCalled)
	}
	if f.mergeCalled != 1 {
		t.Errorf("MergeWorktree called %d times, want 1", f.mergeCalled)
	}
	if f.removeCalled != 1 {
		t.Errorf("RemoveWorktree called %d times, want 1", f.removeCalled)
	}
	if f.notifyCalled != 0 {
		t.Errorf("EmitNotification called %d times, want 0", f.notifyCalled)
	}
}

// TestHandleTaskDoneAutoPRPushFailureFallsBack — push / api errors (which
// are NOT ErrGHUnavailable / ErrNotGitHub) log per-failure (not deduped) and
// still fall through to silent merge.
func TestHandleTaskDoneAutoPRPushFailureFallsBack(t *testing.T) {
	resetGHFallbackWarnedForTest()
	logBuf := captureLogs(t)

	f := &taskDoneFixture{
		autoPREnabled: true,
		openPRErr:     errors.New("git push failed: remote rejected"),
		mergeChanged:  true,
	}
	cont := handleTaskDoneWith(f.fns(), "/proj", 42, "/wt", nil)
	if !cont {
		t.Errorf("returned false, want true (fallback merge succeeded)")
	}
	if f.openPRCalled != 1 || f.mergeCalled != 1 {
		t.Errorf("expected OpenPR=1 + Merge=1; got OpenPR=%d Merge=%d", f.openPRCalled, f.mergeCalled)
	}
	if !strings.Contains(logBuf.String(), "ERROR [auto-pr]") {
		t.Errorf("expected ERROR log line for push failure; got:\n%s", logBuf.String())
	}
}

// TestHandleTaskDoneAutoPRRespectsProjectScopes — auto-PR enabled globally
// but the project isn't in the scope list → silent merge, no OpenPR call.
func TestHandleTaskDoneAutoPRRespectsProjectScopes(t *testing.T) {
	resetGHFallbackWarnedForTest()
	f := &taskDoneFixture{
		autoPREnabled: true,
		githubScopes:  []string{"some-other-project"},
		mergeChanged:  true,
	}
	cont := handleTaskDoneWith(f.fns(), "/proj", 42, "/wt", nil)
	if !cont {
		t.Errorf("returned false, want true")
	}
	if f.openPRCalled != 0 {
		t.Errorf("OpenPR called %d times, want 0 (project not in scope list)", f.openPRCalled)
	}
	if f.mergeCalled != 1 {
		t.Errorf("MergeWorktree called %d times, want 1", f.mergeCalled)
	}
}
