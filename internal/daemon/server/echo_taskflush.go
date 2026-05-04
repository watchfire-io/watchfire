// Package server — task-flush dispatch for the v8.x Echo inbound
// webhooks (GitLab, Bitbucket, GitHub Enterprise). The v7.0 Relay
// auto-PR path opens a PR on the upstream host and pushes
// `watchfire/<n>` to a remote branch; the matching inbound handler
// receives the merge / close event, looks up the local task by branch
// name + repo URL, and flips it to `done` so the watcher kicks off the
// merge / cleanup pipeline (or just notifies completion when auto-merge
// is off).
//
// The implementation lives in `server` (not `echo`) because it has to
// reach into the projects index + per-project YAML — concerns the echo
// package deliberately stays clear of so the handler tests don't drag
// the full daemon graph in.
package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/models"
)

// gitOriginCommand is the exec entry point for `git remote get-url
// origin` calls. Tests can override the variable to inject a fake
// without spinning up real repos.
var gitOriginCommand = func(ctx context.Context, projectPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = projectPath
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git remote get-url origin in %s: %v (stderr: %s)", projectPath, err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// newTaskFlusher returns the daemon-side `echo.TaskFlusher` that maps a
// verified webhook delivery to the matching local task. The closure
// captures the loaders so tests can swap them with fakes.
func newTaskFlusher() echo.TaskFlusher {
	return makeTaskFlusher(taskFlusherDeps{
		LoadProjects: config.LoadProjectsIndex,
		ResolveOrigin: func(ctx context.Context, projectPath string) (string, error) {
			return gitOriginCommand(ctx, projectPath)
		},
		LoadTask: config.LoadTask,
		SaveTask: config.SaveTask,
	})
}

// taskFlusherDeps is the seam tests use to inject fake loaders. The
// real wiring in `newTaskFlusher` plugs the production `config.*` calls
// in.
type taskFlusherDeps struct {
	LoadProjects  func() (*models.ProjectsIndex, error)
	ResolveOrigin func(ctx context.Context, projectPath string) (string, error)
	LoadTask      func(projectPath string, taskNumber int) (*models.Task, error)
	SaveTask      func(projectPath string, task *models.Task) error
}

// makeTaskFlusher wires the deps into a closure.
func makeTaskFlusher(deps taskFlusherDeps) echo.TaskFlusher {
	return func(ctx context.Context, req echo.TaskFlushRequest) (echo.TaskFlushResult, error) {
		taskNumber, ok := echo.ParseTaskBranch(req.SourceBranch)
		if !ok {
			return echo.TaskFlushResult{Outcome: echo.TaskFlushNoMatch}, nil
		}
		if req.RepoURL == "" {
			return echo.TaskFlushResult{Outcome: echo.TaskFlushNoMatch}, nil
		}
		incomingNorm := echo.NormalizeRepoURL(req.RepoURL)
		if incomingNorm == "" {
			return echo.TaskFlushResult{Outcome: echo.TaskFlushNoMatch}, nil
		}

		index, err := deps.LoadProjects()
		if err != nil {
			return echo.TaskFlushResult{}, fmt.Errorf("load projects index: %w", err)
		}

		var matched *models.ProjectEntry
		for i := range index.Projects {
			entry := &index.Projects[i]
			origin, err := deps.ResolveOrigin(ctx, entry.Path)
			if err != nil {
				log.Printf("WARN: echo: project %s (%s): cannot resolve origin: %v", entry.Name, entry.ProjectID, err)
				continue
			}
			if echo.NormalizeRepoURL(origin) == incomingNorm {
				matched = entry
				break
			}
		}
		if matched == nil {
			log.Printf("INFO: echo: no project matched repo URL %s for branch %s", req.RepoURL, req.SourceBranch)
			return echo.TaskFlushResult{Outcome: echo.TaskFlushNoMatch}, nil
		}

		task, err := deps.LoadTask(matched.Path, taskNumber)
		if err != nil {
			return echo.TaskFlushResult{}, fmt.Errorf("load task #%d in %s: %w", taskNumber, matched.Name, err)
		}
		if task == nil {
			log.Printf("INFO: echo: task #%d not found in project %s — branch %s", taskNumber, matched.Name, req.SourceBranch)
			return echo.TaskFlushResult{
				Outcome:     echo.TaskFlushNoMatch,
				ProjectID:   matched.ProjectID,
				ProjectName: matched.Name,
				TaskNumber:  taskNumber,
			}, nil
		}

		if task.Status == models.TaskStatusDone {
			return echo.TaskFlushResult{
				Outcome:     echo.TaskFlushAlreadyDone,
				ProjectID:   matched.ProjectID,
				ProjectName: matched.Name,
				TaskNumber:  task.TaskNumber,
				TaskTitle:   task.Title,
			}, nil
		}

		task.MarkDone(req.Merged, req.FailureReason)
		if err := deps.SaveTask(matched.Path, task); err != nil {
			return echo.TaskFlushResult{}, fmt.Errorf("save task #%d in %s: %w", taskNumber, matched.Name, err)
		}

		outcome := echo.TaskFlushedSuccess
		if !req.Merged {
			outcome = echo.TaskFlushedFailure
		}
		return echo.TaskFlushResult{
			Outcome:     outcome,
			ProjectID:   matched.ProjectID,
			ProjectName: matched.Name,
			TaskNumber:  task.TaskNumber,
			TaskTitle:   task.Title,
		}, nil
	}
}
