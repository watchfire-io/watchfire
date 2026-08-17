package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

// ccFixture builds a deps set backed by real on-disk projects (under a
// temp $HOME) with an in-memory integrations config and fake agent
// hooks. Using the real task manager + config loaders keeps the retry /
// cancel semantics honest — they exercise the same BulkUpdateStatus /
// SaveTask paths production uses.
type ccFixture struct {
	t            *testing.T
	integrations *models.IntegrationsConfig
	taskMgr      *task.Manager

	agentTask    int  // task the fake running agent works on; 0 = none
	agentRunning bool //
	stopCalls    []string
	stopErr      error
}

func newCCFixture(t *testing.T) *ccFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return &ccFixture{
		t:            t,
		integrations: models.NewIntegrationsConfig(),
		taskMgr:      task.NewManager(),
	}
}

func (f *ccFixture) deps() commandContextDeps {
	return commandContextDeps{
		LoadIntegrations:  func() (*models.IntegrationsConfig, error) { return f.integrations, nil },
		LoadProjectsIndex: config.LoadProjectsIndex,
		LoadProject:       config.LoadProject,
		ListTasks:         f.taskMgr.ListTasks,
		GetTask:           f.taskMgr.GetTask,
		SetTaskStatus:     f.taskMgr.BulkUpdateStatus,
		SaveTask:          config.SaveTask,
		AgentTaskNumber: func(projectID string) (int, bool) {
			if !f.agentRunning {
				return 0, false
			}
			return f.agentTask, true
		},
		StopAgentByUser: func(projectID string) error {
			f.stopCalls = append(f.stopCalls, projectID)
			return f.stopErr
		},
	}
}

// addProject registers a project on disk and returns its path.
func (f *ccFixture) addProject(id, name string, mutate func(*models.Project)) string {
	f.t.Helper()
	path := f.t.TempDir()
	if err := config.EnsureProjectDir(path); err != nil {
		f.t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject(id, name, path)
	if mutate != nil {
		mutate(proj)
	}
	if err := config.SaveProject(path, proj); err != nil {
		f.t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(id, name, path); err != nil {
		f.t.Fatalf("RegisterProject: %v", err)
	}
	return path
}

func (f *ccFixture) addTask(projectPath string, t *models.Task) {
	f.t.Helper()
	if t.Version == 0 {
		t.Version = 1
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
		t.UpdatedAt = now
	}
	if err := config.SaveTask(projectPath, t); err != nil {
		f.t.Fatalf("SaveTask: %v", err)
	}
}

func projectIDs(infos []echo.ProjectInfo) []string {
	out := make([]string, 0, len(infos))
	for _, p := range infos {
		out = append(out, p.ID)
	}
	return out
}

func TestFindProjectsDiscordScoping(t *testing.T) {
	f := newCCFixture(t)
	f.addProject("p-bound", "Bound", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-other"
	})
	f.addProject("p-open", "Open", nil)
	f.addProject("p-muted", "Muted", nil)
	f.addProject("p-archived", "Archived", func(p *models.Project) { p.Status = "archived" })
	f.integrations.Discord = []models.DiscordEndpoint{
		{ID: "d1", GuildID: "g-1", ProjectMuteIDs: []string{"p-muted"}},
	}

	// Guild with a matching endpoint: covers everything active except
	// the endpoint's muted projects.
	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())
	infos, err := cc.FindProjects(context.Background())
	if err != nil {
		t.Fatalf("FindProjects: %v", err)
	}
	got := projectIDs(infos)
	want := map[string]bool{"p-bound": true, "p-open": true}
	if len(got) != len(want) {
		t.Fatalf("guild g-1 projects = %v, want ids %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("guild g-1 unexpectedly sees %s (all: %v)", id, got)
		}
	}

	// Guild with only a per-project binding: sees just the bound project.
	cc = newCommandContext(commandScope{GuildID: "g-other"}, f.deps())
	infos, err = cc.FindProjects(context.Background())
	if err != nil {
		t.Fatalf("FindProjects: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "p-bound" {
		t.Fatalf("guild g-other projects = %v, want [p-bound]", projectIDs(infos))
	}

	// Unknown guild: nothing.
	cc = newCommandContext(commandScope{GuildID: "g-unknown"}, f.deps())
	infos, err = cc.FindProjects(context.Background())
	if err != nil {
		t.Fatalf("FindProjects: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("unknown guild sees projects: %v", projectIDs(infos))
	}
}

func TestFindProjectsSlackScoping(t *testing.T) {
	f := newCCFixture(t)
	f.addProject("p-chan", "Chan", func(p *models.Project) {
		p.Integrations.SlackChannel = "#eng"
	})
	f.addProject("p-plain", "Plain", nil)

	// No OAuth workspace recorded: only channel-bound projects opt in.
	cc := newCommandContext(commandScope{TeamID: "T-1"}, f.deps())
	infos, err := cc.FindProjects(context.Background())
	if err != nil {
		t.Fatalf("FindProjects: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "p-chan" {
		t.Fatalf("secret-only slack scope = %v, want [p-chan]", projectIDs(infos))
	}

	// OAuth workspace recorded and matching: every active project.
	f.integrations.Inbound.SlackTeamID = "T-1"
	infos, err = cc.FindProjects(context.Background())
	if err != nil {
		t.Fatalf("FindProjects: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("matching team sees %v, want both projects", projectIDs(infos))
	}

	// Different workspace: nothing, even the channel-bound project.
	cc = newCommandContext(commandScope{TeamID: "T-2"}, f.deps())
	infos, err = cc.FindProjects(context.Background())
	if err != nil {
		t.Fatalf("FindProjects: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("mismatched team sees projects: %v", projectIDs(infos))
	}
}

func TestLookupTaskScopedAndSoftDeleteAware(t *testing.T) {
	f := newCCFixture(t)
	visible := f.addProject("p-vis", "Visible", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-1"
	})
	hidden := f.addProject("p-hid", "Hidden", nil)

	f.addTask(visible, &models.Task{TaskID: "vis00007", TaskNumber: 7, Title: "in scope", Status: models.TaskStatusReady})
	deleted := time.Now().UTC()
	f.addTask(visible, &models.Task{TaskID: "vis00008", TaskNumber: 8, Title: "trashed", Status: models.TaskStatusReady, DeletedAt: &deleted})
	f.addTask(hidden, &models.Task{TaskID: "hid00009", TaskNumber: 9, Title: "out of scope", Status: models.TaskStatusReady})

	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())

	// By number.
	tk, info, err := cc.LookupTask(context.Background(), "7")
	if err != nil {
		t.Fatalf("LookupTask(7): %v", err)
	}
	if tk.TaskNumber != 7 || info.ID != "p-vis" {
		t.Fatalf("LookupTask(7) = task %d in %s", tk.TaskNumber, info.ID)
	}

	// By task_id.
	tk, _, err = cc.LookupTask(context.Background(), "vis00007")
	if err != nil || tk.TaskNumber != 7 {
		t.Fatalf("LookupTask(vis00007) = %v, %v", tk, err)
	}

	// Soft-deleted task is invisible.
	if _, _, err = cc.LookupTask(context.Background(), "8"); !errors.Is(err, echo.ErrTaskNotFound) {
		t.Fatalf("LookupTask(deleted) err = %v, want ErrTaskNotFound", err)
	}

	// Task in an unmapped project is invisible.
	if _, _, err = cc.LookupTask(context.Background(), "9"); !errors.Is(err, echo.ErrTaskNotFound) {
		t.Fatalf("LookupTask(out of scope) err = %v, want ErrTaskNotFound", err)
	}
}

func TestListTopActiveTasksOrderAndLimit(t *testing.T) {
	f := newCCFixture(t)
	path := f.addProject("p-1", "Proj", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-1"
	})
	f.addTask(path, &models.Task{TaskID: "t0000001", TaskNumber: 1, Title: "ready-1", Status: models.TaskStatusReady, Position: 1})
	f.addTask(path, &models.Task{TaskID: "t0000002", TaskNumber: 2, Title: "in-dev", Status: models.TaskStatusReady, Position: 2})
	f.addTask(path, &models.Task{TaskID: "t0000003", TaskNumber: 3, Title: "ready-3", Status: models.TaskStatusReady, Position: 3})
	f.addTask(path, &models.Task{TaskID: "t0000004", TaskNumber: 4, Title: "draft", Status: models.TaskStatusDraft, Position: 4})
	f.agentRunning = true
	f.agentTask = 2

	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())
	tasks, err := cc.ListTopActiveTasks(context.Background(), "p-1", 2)
	if err != nil {
		t.Fatalf("ListTopActiveTasks: %v", err)
	}
	if len(tasks) != 2 || tasks[0].TaskNumber != 2 || tasks[1].TaskNumber != 1 {
		nums := make([]int, len(tasks))
		for i, tk := range tasks {
			nums[i] = tk.TaskNumber
		}
		t.Fatalf("active tasks = %v, want [2 1] (in-flight first, then ready queue, capped)", nums)
	}

	// Without a running agent: plain ready queue in canonical order.
	f.agentRunning = false
	tasks, err = cc.ListTopActiveTasks(context.Background(), "p-1", 10)
	if err != nil {
		t.Fatalf("ListTopActiveTasks: %v", err)
	}
	if len(tasks) != 3 || tasks[0].TaskNumber != 1 || tasks[2].TaskNumber != 3 {
		t.Fatalf("ready queue = %v tasks, first=%d", len(tasks), tasks[0].TaskNumber)
	}
}

func TestRetryFlipsDoneTaskBackToReady(t *testing.T) {
	f := newCCFixture(t)
	path := f.addProject("p-1", "Proj", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-1"
	})
	failed := false
	done := time.Now().UTC()
	f.addTask(path, &models.Task{
		TaskID: "t0000005", TaskNumber: 5, Title: "failed run",
		Status: models.TaskStatusDone, Success: &failed,
		FailureReason: "boom", CompletedAt: &done,
	})

	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())
	if err := cc.Retry(context.Background(), "p-1", 5); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	tk, err := config.LoadTask(path, 5)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Status != models.TaskStatusReady {
		t.Fatalf("status = %s, want ready", tk.Status)
	}
	if tk.Success != nil || tk.FailureReason != "" || tk.CompletedAt != nil {
		t.Fatalf("terminal fields not cleared: success=%v reason=%q completed=%v", tk.Success, tk.FailureReason, tk.CompletedAt)
	}

	// Already-ready is an idempotent no-op.
	if err := cc.Retry(context.Background(), "p-1", 5); err != nil {
		t.Fatalf("Retry(ready): %v", err)
	}
}

func TestRetryRefusals(t *testing.T) {
	f := newCCFixture(t)
	path := f.addProject("p-1", "Proj", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-1"
	})
	f.addTask(path, &models.Task{TaskID: "t0000006", TaskNumber: 6, Title: "working", Status: models.TaskStatusReady})
	deleted := time.Now().UTC()
	f.addTask(path, &models.Task{TaskID: "t0000007", TaskNumber: 7, Title: "trashed", Status: models.TaskStatusDone, DeletedAt: &deleted})

	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())

	// Task the agent is currently working: refuse.
	f.agentRunning = true
	f.agentTask = 6
	if err := cc.Retry(context.Background(), "p-1", 6); err == nil {
		t.Fatal("Retry(running task) succeeded, want refusal")
	}

	// Soft-deleted: refuse.
	if err := cc.Retry(context.Background(), "p-1", 7); err == nil {
		t.Fatal("Retry(deleted task) succeeded, want refusal")
	}

	// Project not mapped to the calling guild: refuse.
	other := newCommandContext(commandScope{GuildID: "g-unmapped"}, f.deps())
	if err := other.Retry(context.Background(), "p-1", 6); err == nil {
		t.Fatal("Retry(unmapped project) succeeded, want refusal")
	}
}

func TestCancelRunningTaskStopsAgentAndRecordsReason(t *testing.T) {
	f := newCCFixture(t)
	path := f.addProject("p-1", "Proj", func(p *models.Project) {
		p.Integrations.SlackChannel = "#eng"
	})
	f.addTask(path, &models.Task{TaskID: "t0000009", TaskNumber: 9, Title: "in flight", Status: models.TaskStatusReady})
	f.agentRunning = true
	f.agentTask = 9

	cc := newCommandContext(commandScope{TeamID: "T-1"}, f.deps())
	if err := cc.Cancel(context.Background(), "p-1", 9, ""); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(f.stopCalls) != 1 || f.stopCalls[0] != "p-1" {
		t.Fatalf("StopAgentByUser calls = %v, want [p-1]", f.stopCalls)
	}
	tk, err := config.LoadTask(path, 9)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Status != models.TaskStatusDone || tk.Success == nil || *tk.Success {
		t.Fatalf("task = status %s success %v, want done + failed", tk.Status, tk.Success)
	}
	if tk.FailureReason != "cancelled via Slack" {
		t.Fatalf("failure_reason = %q, want default slack reason", tk.FailureReason)
	}
}

func TestCancelQueuedTaskWithExplicitReason(t *testing.T) {
	f := newCCFixture(t)
	path := f.addProject("p-1", "Proj", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-1"
	})
	f.addTask(path, &models.Task{TaskID: "t0000010", TaskNumber: 10, Title: "queued", Status: models.TaskStatusReady})

	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())
	if err := cc.Cancel(context.Background(), "p-1", 10, "superseded by 0011"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(f.stopCalls) != 0 {
		t.Fatalf("StopAgentByUser called for a non-running task: %v", f.stopCalls)
	}
	tk, err := config.LoadTask(path, 10)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Status != models.TaskStatusDone || tk.FailureReason != "superseded by 0011" {
		t.Fatalf("task = status %s reason %q", tk.Status, tk.FailureReason)
	}
}

func TestCancelRefusals(t *testing.T) {
	f := newCCFixture(t)
	path := f.addProject("p-1", "Proj", func(p *models.Project) {
		p.Integrations.DiscordGuildID = "g-1"
	})
	ok := true
	f.addTask(path, &models.Task{TaskID: "t0000011", TaskNumber: 11, Title: "finished", Status: models.TaskStatusDone, Success: &ok})
	f.addTask(path, &models.Task{TaskID: "t0000012", TaskNumber: 12, Title: "running", Status: models.TaskStatusReady})

	cc := newCommandContext(commandScope{GuildID: "g-1"}, f.deps())

	// Already done: refuse.
	if err := cc.Cancel(context.Background(), "p-1", 11, ""); err == nil {
		t.Fatal("Cancel(done task) succeeded, want refusal")
	}

	// Stop failure propagates and the task is left unmarked.
	f.agentRunning = true
	f.agentTask = 12
	f.stopErr = errors.New("pty wedged")
	if err := cc.Cancel(context.Background(), "p-1", 12, ""); err == nil {
		t.Fatal("Cancel with failing stop succeeded, want error")
	}
	tk, err := config.LoadTask(path, 12)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Status != models.TaskStatusReady {
		t.Fatalf("task marked %s despite stop failure, want ready", tk.Status)
	}
}
