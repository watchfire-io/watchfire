package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	gitpkg "github.com/watchfire-io/watchfire/internal/daemon/git"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// taskDoneFns abstracts the side-effects of HandleTaskDone so the integration
// test can swap the merge / PR / notification calls out without standing up a
// real git remote, gh CLI, or notifications writer. Production wiring uses
// defaultTaskDoneFns (real config, real merge, real notify).
type taskDoneFns struct {
	LoadProject      func(projectPath string) (*models.Project, error)
	LoadTask         func(projectPath string, taskNumber int) (*models.Task, error)
	LoadIntegrations func() (*models.IntegrationsConfig, error)
	OpenPR           func(ctx context.Context, opts gitpkg.OpenPROptions) (*gitpkg.PRResult, error)
	MergeWorktree    func(projectPath string, taskNumber int) (bool, error)
	RemoveWorktree   func(projectPath string, taskNumber int, merged bool) error
	EmitNotification func(bus *notify.Bus, n notify.Notification) error
}

var defaultTaskDoneFns = taskDoneFns{
	LoadProject:      config.LoadProject,
	LoadTask:         config.LoadTask,
	LoadIntegrations: config.LoadIntegrations,
	OpenPR:           gitpkg.OpenPR,
	MergeWorktree:    MergeWorktree,
	RemoveWorktree:   RemoveWorktree,
	EmitNotification: func(bus *notify.Bus, n notify.Notification) error {
		if bus != nil {
			bus.Emit(n)
		}
		return notify.AppendLogLine(n)
	},
}

// ghFallbackWarned dedupes the ErrGHUnavailable / ErrNotGitHub fallback WARN
// to one line per (projectID, kind) per process lifetime. A misconfigured
// project shouldn't flood the daemon log with the same warning every task.
var ghFallbackWarned sync.Map

// resetGHFallbackWarnedForTest clears the dedupe table; tests use it to
// exercise the once-per-project semantics deterministically.
func resetGHFallbackWarnedForTest() { ghFallbackWarned = sync.Map{} }

// HandleTaskDone is the v7.0 Relay replacement for the silent-merge closure
// formerly inlined in `internal/daemon/server/server.go`. It runs after an
// agent exits for a task and decides between:
//
//  1. Auto-PR (`IntegrationsConfig.GitHub.AutoPRApplies(projectID) == true`)
//     — push the task branch and open a PR via `gh api`. On success, suppress
//     the local merge but still clean the worktree.
//  2. Silent merge — the existing v6.x flow: `git merge --no-ff` the task
//     branch into the project's default branch, then remove the worktree.
//
// Returns true to continue chaining (start-all / wildfire), false to stop.
//
// On any auto-PR failure (`gh` missing, non-github origin, push reject, gh
// api error) the function logs loudly then falls through to silent merge so
// the user's work never strands inside an unmerged worktree.
func HandleTaskDone(projectPath string, taskNumber int, worktreePath string, bus *notify.Bus) bool {
	return handleTaskDoneWith(defaultTaskDoneFns, projectPath, taskNumber, worktreePath, bus)
}

func handleTaskDoneWith(fns taskDoneFns, projectPath string, taskNumber int, worktreePath string, bus *notify.Bus) bool {
	if taskNumber == 0 || worktreePath == "" {
		log.Printf("[merge] Skipping merge: taskNumber=%d worktreePath=%q", taskNumber, worktreePath)
		return true
	}
	proj, err := fns.LoadProject(projectPath)
	if err != nil {
		log.Printf("[merge] Failed to load project for task #%04d: %v", taskNumber, err)
		return false
	}
	t, err := fns.LoadTask(projectPath, taskNumber)
	if err != nil || t == nil {
		log.Printf("[merge] Failed to load task #%04d: %v", taskNumber, err)
		return false
	}
	if t.Status != models.TaskStatusDone {
		log.Printf("[merge] Task #%04d not done (status: %s), skipping merge", taskNumber, t.Status)
		return true
	}
	log.Printf("[merge] Task #%04d done, deciding merge path (auto_merge=%v, auto_delete=%v)",
		taskNumber, proj.AutoMerge, proj.AutoDeleteBranch)

	if proj.AutoMerge && tryAutoPR(fns, proj, t, projectPath, taskNumber, bus) {
		return true
	}

	return runSilentMerge(fns, proj, projectPath, taskNumber)
}

// tryAutoPR returns true when the auto-PR flow took ownership of the merge
// (PR opened successfully, worktree cleaned). It returns false in two cases:
// auto-PR is not enabled for this project, or the PR attempt failed and the
// caller should fall through to silent merge.
func tryAutoPR(fns taskDoneFns, proj *models.Project, t *models.Task, projectPath string, taskNumber int, bus *notify.Bus) bool {
	integrations, _ := fns.LoadIntegrations()
	if integrations == nil || !integrations.GitHub.AutoPRApplies(proj.ProjectID) {
		return false
	}

	log.Printf("[auto-pr] Task #%04d: project %s opted into GitHub auto-PR — attempting PR", taskNumber, proj.Name)

	prRes, prErr := fns.OpenPR(context.Background(), gitpkg.OpenPROptions{
		ProjectPath:        projectPath,
		ProjectID:          proj.ProjectID,
		TaskNumber:         taskNumber,
		TaskTitle:          t.Title,
		TaskPrompt:         t.Prompt,
		AcceptanceCriteria: t.AcceptanceCriteria,
		Agent:              resolveAgentNameForPR(proj, t),
		DraftDefault:       integrations.GitHub.DraftDefault,
		CompletedAt:        completedAtOrNow(t),
	})
	if prErr == nil {
		log.Printf("[auto-pr] Task #%04d PR opened: %s", taskNumber, prRes.URL)
		emitPROpenedNotification(fns, bus, proj, taskNumber, prRes.URL)
		if proj.AutoDeleteBranch {
			if err := fns.RemoveWorktree(projectPath, taskNumber, true); err != nil {
				log.Printf("[auto-pr] Failed to remove worktree for task #%04d after PR open: %v", taskNumber, err)
			}
		}
		return true
	}

	logAutoPRFallback(proj.ProjectID, taskNumber, prErr)
	return false
}

// logAutoPRFallback emits a single log line per failure but dedupes the
// "gh missing / non-github origin" variant to one line per project lifetime
// — a misconfigured project would otherwise spam the daemon log with the
// same warning every task. Push / API failures log every time because they
// might be transient.
func logAutoPRFallback(projectID string, taskNumber int, prErr error) {
	switch {
	case errors.Is(prErr, gitpkg.ErrGHUnavailable):
		key := projectID + "|gh-unavailable"
		if _, loaded := ghFallbackWarned.LoadOrStore(key, struct{}{}); !loaded {
			log.Printf("WARN [auto-pr] project %s: github auto-PR enabled but gh CLI unavailable; falling back to silent merge (will not warn again this run)", projectID)
		}
	case errors.Is(prErr, gitpkg.ErrNotGitHub):
		key := projectID + "|not-github"
		if _, loaded := ghFallbackWarned.LoadOrStore(key, struct{}{}); !loaded {
			log.Printf("WARN [auto-pr] project %s: github auto-PR enabled but origin is not a github.com URL; falling back to silent merge (will not warn again this run)", projectID)
		}
	default:
		log.Printf("ERROR [auto-pr] task #%04d: failed to open PR (%v); falling back to silent merge", taskNumber, prErr)
	}
}

func emitPROpenedNotification(fns taskDoneFns, bus *notify.Bus, proj *models.Project, taskNumber int, prURL string) {
	emittedAt := time.Now().UTC()
	title := fmt.Sprintf("%s — PR opened for task #%04d", proj.Name, taskNumber)
	if proj.Name == "" {
		title = fmt.Sprintf("PR opened for task #%04d", taskNumber)
	}
	n := notify.Notification{
		ID:         notify.MakeID(notify.KindRunComplete, proj.ProjectID, int32(taskNumber), emittedAt),
		Kind:       notify.KindRunComplete,
		ProjectID:  proj.ProjectID,
		TaskNumber: int32(taskNumber),
		Title:      title,
		Body:       "PR opened: " + prURL,
		EmittedAt:  emittedAt,
	}
	if err := fns.EmitNotification(bus, n); err != nil {
		log.Printf("[auto-pr] failed to record PR-opened notification for task #%04d: %v", taskNumber, err)
	}
}

func runSilentMerge(fns taskDoneFns, proj *models.Project, projectPath string, taskNumber int) bool {
	var merged bool
	mergeFailed := false
	if proj.AutoMerge {
		var mergeErr error
		merged, mergeErr = fns.MergeWorktree(projectPath, taskNumber)
		switch {
		case mergeErr != nil:
			log.Printf("[merge] Auto-merge failed for task #%04d: %v", taskNumber, mergeErr)
			mergeFailed = true
		case merged:
			log.Printf("[merge] Auto-merged task #%04d into current branch", taskNumber)
		default:
			log.Printf("[merge] Task #%04d has no file differences — skipped merge", taskNumber)
		}
	}
	if proj.AutoDeleteBranch && !mergeFailed {
		if err := fns.RemoveWorktree(projectPath, taskNumber, merged); err != nil {
			log.Printf("[merge] Failed to remove worktree for task #%04d: %v", taskNumber, err)
		} else {
			log.Printf("[merge] Removed worktree for task #%04d", taskNumber)
		}
	}
	return !mergeFailed
}

func resolveAgentNameForPR(proj *models.Project, t *models.Task) string {
	if t != nil && t.Agent != "" {
		return t.Agent
	}
	if proj != nil && proj.DefaultAgent != "" {
		return proj.DefaultAgent
	}
	return "claude-code"
}

func completedAtOrNow(t *models.Task) time.Time {
	if t != nil && t.CompletedAt != nil {
		return *t.CompletedAt
	}
	return time.Now().UTC()
}
