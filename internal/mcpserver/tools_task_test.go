package mcpserver

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/watchfire-io/watchfire/proto"
)

// fakeProjectClient serves ListProjects for project resolution; every other
// method panics via the embedded nil interface.
type fakeProjectClient struct {
	pb.ProjectServiceClient
	projects []*pb.Project
}

func (f *fakeProjectClient) ListProjects(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.ProjectList, error) {
	return &pb.ProjectList{Projects: f.projects}, nil
}

type fakeSettingsClient struct {
	pb.SettingsServiceClient
	agents []*pb.AgentInfo
}

func (f *fakeSettingsClient) ListAgents(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.AgentList, error) {
	return &pb.AgentList{Agents: f.agents}, nil
}

type fakeTaskClient struct {
	pb.TaskServiceClient
	createFn func(*pb.CreateTaskRequest) (*pb.Task, error)
	listFn   func(*pb.ListTasksRequest) (*pb.TaskList, error)
	getFn    func(*pb.TaskId) (*pb.Task, error)
	updateFn func(*pb.UpdateTaskRequest) (*pb.Task, error)
	deleteFn func(*pb.TaskId) (*pb.Task, error)
}

func (f *fakeTaskClient) CreateTask(_ context.Context, req *pb.CreateTaskRequest, _ ...grpc.CallOption) (*pb.Task, error) {
	return f.createFn(req)
}

func (f *fakeTaskClient) ListTasks(_ context.Context, req *pb.ListTasksRequest, _ ...grpc.CallOption) (*pb.TaskList, error) {
	return f.listFn(req)
}

func (f *fakeTaskClient) GetTask(_ context.Context, req *pb.TaskId, _ ...grpc.CallOption) (*pb.Task, error) {
	return f.getFn(req)
}

func (f *fakeTaskClient) UpdateTask(_ context.Context, req *pb.UpdateTaskRequest, _ ...grpc.CallOption) (*pb.Task, error) {
	return f.updateFn(req)
}

func (f *fakeTaskClient) DeleteTask(_ context.Context, req *pb.TaskId, _ ...grpc.CallOption) (*pb.Task, error) {
	return f.deleteFn(req)
}

func testServer(tasks *fakeTaskClient) *server {
	return &server{
		projects: &fakeProjectClient{projects: []*pb.Project{{ProjectId: "id-demo", Name: "demo"}}},
		settings: &fakeSettingsClient{agents: []*pb.AgentInfo{
			{Name: "claude-code"}, {Name: "codex"}, {Name: "gemini"},
		}},
		tasks:            tasks,
		defaultProjectID: "id-demo",
	}
}

func boolPtr(b bool) *bool { return &b }

func strPtr(s string) *string { return &s }

func TestCreateTaskValidation(t *testing.T) {
	s := testServer(&fakeTaskClient{})
	ctx := context.Background()

	cases := []struct {
		name    string
		args    createTaskArgs
		wantErr string
	}{
		{"missing title", createTaskArgs{Prompt: "do it"}, `"title" is required`},
		{"missing prompt", createTaskArgs{Title: "t"}, `"prompt" is required`},
		{"bad status", createTaskArgs{Title: "t", Prompt: "p", Status: "done"}, "invalid status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handleCreateTask(ctx, s, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("want error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestCreateTaskUnknownAgent(t *testing.T) {
	s := testServer(&fakeTaskClient{})

	_, err := handleCreateTask(context.Background(), s, createTaskArgs{
		Title: "t", Prompt: "p", Agent: "gpt-9",
	})
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"gpt-9"`) {
		t.Errorf("error should name the bad agent, got: %v", err)
	}
	for _, valid := range []string{"claude-code", "codex", "gemini"} {
		if !strings.Contains(msg, valid) {
			t.Errorf("error should list valid agent %q, got: %v", valid, err)
		}
	}
}

func TestCreateTaskHappyPath(t *testing.T) {
	var got *pb.CreateTaskRequest
	s := testServer(&fakeTaskClient{
		createFn: func(req *pb.CreateTaskRequest) (*pb.Task, error) {
			got = req
			return &pb.Task{
				TaskId: "abc12345", TaskNumber: 7, Title: req.Title,
				Prompt: req.Prompt, Status: req.Status, Position: 7,
			}, nil
		},
	})

	out, err := handleCreateTask(context.Background(), s, createTaskArgs{
		Title: "Build the thing", Prompt: "Instructions", AcceptanceCriteria: "It works",
		Agent: "codex",
	})
	if err != nil {
		t.Fatalf("handleCreateTask: %v", err)
	}

	if got.ProjectId != "id-demo" {
		t.Errorf("project_id = %q, want id-demo (default project)", got.ProjectId)
	}
	if got.Status != "draft" {
		t.Errorf("status = %q, want draft default", got.Status)
	}
	if got.AcceptanceCriteria == nil || *got.AcceptanceCriteria != "It works" {
		t.Errorf("acceptance_criteria not passed through: %v", got.AcceptanceCriteria)
	}
	if got.Agent == nil || *got.Agent != "codex" {
		t.Errorf("agent override not passed through: %v", got.Agent)
	}

	detail, ok := out.(taskDetail)
	if !ok {
		t.Fatalf("result type %T, want taskDetail", out)
	}
	if detail.TaskNumber != 7 || detail.TaskID != "abc12345" || detail.Status != "draft" {
		t.Errorf("unexpected detail: %+v", detail)
	}
}

func TestListTasksHappyPath(t *testing.T) {
	s := testServer(&fakeTaskClient{
		listFn: func(req *pb.ListTasksRequest) (*pb.TaskList, error) {
			if req.ProjectId != "id-demo" {
				t.Errorf("project_id = %q, want id-demo", req.ProjectId)
			}
			if !req.IncludeDeleted {
				t.Error("include_deleted not passed through")
			}
			return &pb.TaskList{Tasks: []*pb.Task{
				{TaskNumber: 1, Title: "one", Status: "done", Success: boolPtr(true), Position: 1},
				{TaskNumber: 2, Title: "two", Status: "ready", Agent: "codex", Position: 2},
			}}, nil
		},
	})

	out, err := handleListTasks(context.Background(), s, listTasksArgs{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("handleListTasks: %v", err)
	}
	rows := out.(struct {
		Tasks []taskSummary `json:"tasks"`
	}).Tasks
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Success == nil || !*rows[0].Success {
		t.Errorf("row 0 success not mapped: %+v", rows[0])
	}
	if rows[1].Agent != "codex" {
		t.Errorf("row 1 agent = %q, want codex", rows[1].Agent)
	}
}

func TestGetTaskHappyPath(t *testing.T) {
	s := testServer(&fakeTaskClient{
		getFn: func(req *pb.TaskId) (*pb.Task, error) {
			if req.TaskNumber != 4 {
				t.Errorf("task_number = %d, want 4", req.TaskNumber)
			}
			return &pb.Task{
				TaskId: "zzz", TaskNumber: 4, Title: "t", Status: "done",
				Success: boolPtr(false), FailureReason: strPtr("blocked on X"),
			}, nil
		},
	})

	out, err := handleGetTask(context.Background(), s, taskRefArgs{TaskNumber: 4})
	if err != nil {
		t.Fatalf("handleGetTask: %v", err)
	}
	detail := out.(taskDetail)
	if detail.FailureReason != "blocked on X" {
		t.Errorf("failure_reason = %q, want blocked on X", detail.FailureReason)
	}
	if detail.Success == nil || *detail.Success {
		t.Errorf("success not mapped: %+v", detail)
	}
}

func TestUpdateTaskValidation(t *testing.T) {
	s := testServer(&fakeTaskClient{})
	ctx := context.Background()

	if _, err := handleUpdateTask(ctx, s, updateTaskArgs{TaskNumber: 1}); err == nil ||
		!strings.Contains(err.Error(), "nothing to update") {
		t.Errorf("want nothing-to-update error, got: %v", err)
	}

	if _, err := handleUpdateTask(ctx, s, updateTaskArgs{TaskNumber: 1, Status: strPtr("done")}); err == nil ||
		!strings.Contains(err.Error(), "invalid status") {
		t.Errorf("want invalid-status error for done, got: %v", err)
	}

	_, err := handleUpdateTask(ctx, s, updateTaskArgs{TaskNumber: 1, Agent: strPtr("gpt-9")})
	if err == nil || !strings.Contains(err.Error(), "claude-code") {
		t.Errorf("unknown-agent error should list valid backends, got: %v", err)
	}
}

func TestUpdateTaskHappyPath(t *testing.T) {
	var got *pb.UpdateTaskRequest
	s := testServer(&fakeTaskClient{
		updateFn: func(req *pb.UpdateTaskRequest) (*pb.Task, error) {
			got = req
			return &pb.Task{TaskNumber: req.TaskNumber, Status: *req.Status}, nil
		},
	})

	// An empty agent string clears the override and must not be validated.
	out, err := handleUpdateTask(context.Background(), s, updateTaskArgs{
		TaskNumber: 3, Status: strPtr("ready"), Agent: strPtr(""),
	})
	if err != nil {
		t.Fatalf("handleUpdateTask: %v", err)
	}
	if got.TaskNumber != 3 || got.Status == nil || *got.Status != "ready" {
		t.Errorf("request not mapped: %+v", got)
	}
	if got.Agent == nil || *got.Agent != "" {
		t.Errorf("empty agent (clear override) not passed through: %v", got.Agent)
	}
	if got.Title != nil || got.Prompt != nil || got.Position != nil {
		t.Errorf("unset fields must stay nil: %+v", got)
	}
	if out.(taskDetail).Status != "ready" {
		t.Errorf("unexpected detail: %+v", out)
	}
}

func TestUpdateTaskSurfacesDaemonError(t *testing.T) {
	s := testServer(&fakeTaskClient{
		updateFn: func(*pb.UpdateTaskRequest) (*pb.Task, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := handleUpdateTask(context.Background(), s, updateTaskArgs{
		TaskNumber: 9, Title: strPtr("x"),
	})
	if err == nil || !strings.Contains(err.Error(), "failed to update task 9") {
		t.Errorf("daemon error should be surfaced with task context, got: %v", err)
	}
}

func TestDeleteTaskHappyPath(t *testing.T) {
	s := testServer(&fakeTaskClient{
		deleteFn: func(req *pb.TaskId) (*pb.Task, error) {
			if req.ProjectId != "id-demo" || req.TaskNumber != 5 {
				t.Errorf("unexpected request: %+v", req)
			}
			return &pb.Task{TaskNumber: 5, Title: "bye", Status: "draft"}, nil
		},
	})

	out, err := handleDeleteTask(context.Background(), s, taskRefArgs{TaskNumber: 5})
	if err != nil {
		t.Fatalf("handleDeleteTask: %v", err)
	}
	res := out.(struct {
		Deleted bool       `json:"deleted"`
		Note    string     `json:"note"`
		Task    taskDetail `json:"task"`
	})
	if !res.Deleted || res.Task.TaskNumber != 5 {
		t.Errorf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Note, "restorable") {
		t.Errorf("note should say the delete is reversible, got: %q", res.Note)
	}
}

// TestTaskToolsRegistered guards the registry wiring: all five task tools are
// present under the task group.
func TestTaskToolsRegistered(t *testing.T) {
	want := map[string]bool{
		"create_task": false, "list_tasks": false, "get_task": false,
		"update_task": false, "delete_task": false,
	}
	for _, td := range allTools() {
		if _, ok := want[td.name]; ok {
			want[td.name] = true
			if td.group != groupTask {
				t.Errorf("tool %s registered under group %q, want %q", td.name, td.group, groupTask)
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %s missing from allTools()", name)
		}
	}
}
