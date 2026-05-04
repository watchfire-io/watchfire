package server

import (
	"context"
	"errors"
	"testing"

	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/models"
)

// fakeFlusher is the test fixture for makeTaskFlusher: it stubs the
// projects index, the per-project origin lookup, and the load/save of
// task YAMLs so the dispatch logic can be exercised without a real
// project tree on disk.
type fakeFlusher struct {
	projects []models.ProjectEntry
	origins  map[string]string         // path -> origin URL
	tasks    map[string]*models.Task   // path -> task
	saved    map[string]*models.Task   // captured saves
}

func newFakeFlusher() *fakeFlusher {
	return &fakeFlusher{
		origins: map[string]string{},
		tasks:   map[string]*models.Task{},
		saved:   map[string]*models.Task{},
	}
}

func (f *fakeFlusher) deps() taskFlusherDeps {
	return taskFlusherDeps{
		LoadProjects: func() (*models.ProjectsIndex, error) {
			return &models.ProjectsIndex{Version: 1, Projects: f.projects}, nil
		},
		ResolveOrigin: func(_ context.Context, path string) (string, error) {
			origin, ok := f.origins[path]
			if !ok {
				return "", errors.New("no origin")
			}
			return origin, nil
		},
		LoadTask: func(path string, taskNumber int) (*models.Task, error) {
			t, ok := f.tasks[path]
			if !ok || t.TaskNumber != taskNumber {
				return nil, nil
			}
			c := *t
			return &c, nil
		},
		SaveTask: func(path string, task *models.Task) error {
			c := *task
			f.saved[path] = &c
			return nil
		},
	}
}

func TestTaskFlusherFlushesMatchingProject(t *testing.T) {
	ff := newFakeFlusher()
	ff.projects = []models.ProjectEntry{
		{ProjectID: "p-1", Name: "alpha", Path: "/p/alpha"},
		{ProjectID: "p-2", Name: "beta", Path: "/p/beta"},
	}
	ff.origins["/p/alpha"] = "git@github.com:org/alpha.git"
	ff.origins["/p/beta"] = "https://github.com/org/beta"
	ff.tasks["/p/beta"] = &models.Task{
		TaskNumber: 42,
		Title:      "Add inbound parity",
		Status:     models.TaskStatusReady,
	}

	flush := makeTaskFlusher(ff.deps())
	res, err := flush(context.Background(), echo.TaskFlushRequest{
		RepoURL:      "https://github.com/org/beta.git",
		SourceBranch: "watchfire/0042",
		Merged:       true,
	})
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if res.Outcome != echo.TaskFlushedSuccess {
		t.Fatalf("outcome = %v, want flushed-success", res.Outcome)
	}
	if res.ProjectID != "p-2" {
		t.Errorf("ProjectID = %q, want p-2", res.ProjectID)
	}
	saved, ok := ff.saved["/p/beta"]
	if !ok {
		t.Fatalf("expected SaveTask to be called for /p/beta")
	}
	if saved.Status != models.TaskStatusDone {
		t.Errorf("saved status = %s, want done", saved.Status)
	}
	if saved.Success == nil || !*saved.Success {
		t.Errorf("expected success=true on flushed task")
	}
}

func TestTaskFlusherAlreadyDoneNoOp(t *testing.T) {
	ff := newFakeFlusher()
	ff.projects = []models.ProjectEntry{
		{ProjectID: "p-1", Name: "alpha", Path: "/p/alpha"},
	}
	ff.origins["/p/alpha"] = "https://github.com/org/alpha.git"
	doneTrue := true
	ff.tasks["/p/alpha"] = &models.Task{
		TaskNumber: 7,
		Status:     models.TaskStatusDone,
		Success:    &doneTrue,
	}

	flush := makeTaskFlusher(ff.deps())
	res, err := flush(context.Background(), echo.TaskFlushRequest{
		RepoURL:      "https://github.com/org/alpha",
		SourceBranch: "watchfire/0007",
		Merged:       true,
	})
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if res.Outcome != echo.TaskFlushAlreadyDone {
		t.Errorf("outcome = %v, want already-done", res.Outcome)
	}
	if _, called := ff.saved["/p/alpha"]; called {
		t.Errorf("expected SaveTask NOT called when task already done")
	}
}

func TestTaskFlusherNoProjectMatch(t *testing.T) {
	ff := newFakeFlusher()
	ff.projects = []models.ProjectEntry{
		{ProjectID: "p-1", Name: "alpha", Path: "/p/alpha"},
	}
	ff.origins["/p/alpha"] = "https://github.com/org/alpha.git"

	flush := makeTaskFlusher(ff.deps())
	res, err := flush(context.Background(), echo.TaskFlushRequest{
		RepoURL:      "https://github.com/other/elsewhere.git",
		SourceBranch: "watchfire/0042",
		Merged:       true,
	})
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if res.Outcome != echo.TaskFlushNoMatch {
		t.Errorf("outcome = %v, want no-match", res.Outcome)
	}
}

func TestTaskFlusherInvalidBranchNoMatch(t *testing.T) {
	ff := newFakeFlusher()
	ff.projects = []models.ProjectEntry{
		{ProjectID: "p-1", Name: "alpha", Path: "/p/alpha"},
	}
	ff.origins["/p/alpha"] = "https://github.com/org/alpha.git"

	flush := makeTaskFlusher(ff.deps())
	res, err := flush(context.Background(), echo.TaskFlushRequest{
		RepoURL:      "https://github.com/org/alpha",
		SourceBranch: "feature/random",
		Merged:       true,
	})
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if res.Outcome != echo.TaskFlushNoMatch {
		t.Errorf("outcome = %v, want no-match", res.Outcome)
	}
}

func TestTaskFlusherFailureSetsReason(t *testing.T) {
	ff := newFakeFlusher()
	ff.projects = []models.ProjectEntry{
		{ProjectID: "p-1", Name: "alpha", Path: "/p/alpha"},
	}
	ff.origins["/p/alpha"] = "https://github.com/org/alpha.git"
	ff.tasks["/p/alpha"] = &models.Task{
		TaskNumber: 9,
		Status:     models.TaskStatusReady,
	}

	flush := makeTaskFlusher(ff.deps())
	res, err := flush(context.Background(), echo.TaskFlushRequest{
		RepoURL:       "https://github.com/org/alpha",
		SourceBranch:  "watchfire/0009",
		Merged:        false,
		FailureReason: "Bitbucket PR rejected without merge",
	})
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if res.Outcome != echo.TaskFlushedFailure {
		t.Errorf("outcome = %v, want flushed-failure", res.Outcome)
	}
	saved := ff.saved["/p/alpha"]
	if saved == nil {
		t.Fatalf("expected SaveTask to be called")
	}
	if saved.FailureReason == "" {
		t.Errorf("expected FailureReason populated, got empty")
	}
}
