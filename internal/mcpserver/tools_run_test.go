package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/watchfire-io/watchfire/proto"
)

// fakeAgentClient serves the three AgentService methods the run tools use;
// every other method panics via the embedded nil interface.
type fakeAgentClient struct {
	pb.AgentServiceClient
	statusFn     func(*pb.ProjectId) (*pb.AgentStatus, error)
	startFn      func(*pb.StartAgentRequest) (*pb.AgentStatus, error)
	stopFn       func(*pb.ProjectId) (*emptypb.Empty, error)
	scrollbackFn func(*pb.ScrollbackRequest) (*pb.ScrollbackLines, error)
}

func (f *fakeAgentClient) GetAgentStatus(_ context.Context, req *pb.ProjectId, _ ...grpc.CallOption) (*pb.AgentStatus, error) {
	return f.statusFn(req)
}

func (f *fakeAgentClient) StartAgent(_ context.Context, req *pb.StartAgentRequest, _ ...grpc.CallOption) (*pb.AgentStatus, error) {
	return f.startFn(req)
}

func (f *fakeAgentClient) StopAgent(_ context.Context, req *pb.ProjectId, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return f.stopFn(req)
}

func idleStatus(*pb.ProjectId) (*pb.AgentStatus, error) {
	return &pb.AgentStatus{ProjectId: "id-demo", IsRunning: false}, nil
}

func runTestServer(tasks *fakeTaskClient, agents *fakeAgentClient) *server {
	s := testServer(tasks)
	s.agents = agents
	return s
}

func TestRunTaskRequiresTaskNumber(t *testing.T) {
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{})

	_, err := handleRunTask(context.Background(), s, taskRefArgs{})
	if err == nil || !strings.Contains(err.Error(), `"task_number" is required`) {
		t.Errorf("want task_number-required error, got: %v", err)
	}
}

func TestRunTaskBusy(t *testing.T) {
	started := false
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		statusFn: func(*pb.ProjectId) (*pb.AgentStatus, error) {
			return &pb.AgentStatus{
				ProjectId: "id-demo", IsRunning: true,
				Mode: "wildfire", TaskNumber: 7, TaskTitle: "Build widget",
				WildfirePhase: "execute",
			}, nil
		},
		startFn: func(*pb.StartAgentRequest) (*pb.AgentStatus, error) {
			started = true
			return nil, nil
		},
	})

	_, err := handleRunTask(context.Background(), s, taskRefArgs{TaskNumber: 9})
	if err == nil {
		t.Fatal("expected busy error")
	}
	for _, want := range []string{"already running", "wildfire", "#7", "Build widget", "stop_agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("busy error should contain %q, got: %v", want, err)
		}
	}
	if started {
		t.Error("StartAgent must not be called on a busy project")
	}
}

func TestRunTaskHappyPath(t *testing.T) {
	var got *pb.StartAgentRequest
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		statusFn: idleStatus,
		startFn: func(req *pb.StartAgentRequest) (*pb.AgentStatus, error) {
			got = req
			return &pb.AgentStatus{
				ProjectId: req.ProjectId, ProjectName: "demo", IsRunning: true,
				Mode: req.Mode, TaskNumber: req.TaskNumber, TaskTitle: "Do it",
			}, nil
		},
	})

	out, err := handleRunTask(context.Background(), s, taskRefArgs{TaskNumber: 9})
	if err != nil {
		t.Fatalf("handleRunTask: %v", err)
	}

	if got.ProjectId != "id-demo" || got.Mode != "task" || got.TaskNumber != 9 {
		t.Errorf("unexpected request: %+v", got)
	}
	if got.Rows != 0 || got.Cols != 0 {
		t.Errorf("rows/cols must be 0 (daemon defaults), got %d/%d", got.Rows, got.Cols)
	}
	if got.Sandbox != "auto" {
		t.Errorf("sandbox = %q, want auto", got.Sandbox)
	}

	res := out.(runStarted)
	if !res.Started || !res.Agent.Running || res.Agent.TaskNumber != 9 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestRunAllAndWildfireModes(t *testing.T) {
	cases := []struct {
		name     string
		call     func(context.Context, *server) (any, error)
		wantMode string
	}{
		{"run_all", func(ctx context.Context, s *server) (any, error) {
			return handleRunAll(ctx, s, agentProjectArgs{})
		}, "start-all"},
		{"start_wildfire", func(ctx context.Context, s *server) (any, error) {
			return handleStartWildfire(ctx, s, agentProjectArgs{})
		}, "wildfire"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *pb.StartAgentRequest
			s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
				statusFn: idleStatus,
				startFn: func(req *pb.StartAgentRequest) (*pb.AgentStatus, error) {
					got = req
					return &pb.AgentStatus{ProjectId: req.ProjectId, IsRunning: true, Mode: req.Mode}, nil
				},
			})

			out, err := tc.call(context.Background(), s)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.Mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if got.TaskNumber != 0 || got.Sandbox != "auto" {
				t.Errorf("unexpected request: %+v", got)
			}
			if res := out.(runStarted); !res.Started {
				t.Errorf("unexpected result: %+v", res)
			}
		})
	}
}

func TestStopAgentIdle(t *testing.T) {
	stopped := false
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		statusFn: idleStatus,
		stopFn: func(*pb.ProjectId) (*emptypb.Empty, error) {
			stopped = true
			return &emptypb.Empty{}, nil
		},
	})

	out, err := handleStopAgent(context.Background(), s, agentProjectArgs{})
	if err != nil {
		t.Fatalf("handleStopAgent: %v", err)
	}
	res := out.(struct {
		Stopped bool   `json:"stopped"`
		Note    string `json:"note"`
	})
	if res.Stopped {
		t.Errorf("stopped should be false on an idle project: %+v", res)
	}
	if stopped {
		t.Error("StopAgent must not be called on an idle project")
	}
}

func TestStopAgentRunning(t *testing.T) {
	stopped := false
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		statusFn: func(*pb.ProjectId) (*pb.AgentStatus, error) {
			return &pb.AgentStatus{ProjectId: "id-demo", IsRunning: true, Mode: "task", TaskNumber: 3}, nil
		},
		stopFn: func(req *pb.ProjectId) (*emptypb.Empty, error) {
			if req.ProjectId != "id-demo" {
				t.Errorf("project_id = %q, want id-demo", req.ProjectId)
			}
			stopped = true
			return &emptypb.Empty{}, nil
		},
	})

	out, err := handleStopAgent(context.Background(), s, agentProjectArgs{})
	if err != nil {
		t.Fatalf("handleStopAgent: %v", err)
	}
	if !stopped {
		t.Fatal("StopAgent not called")
	}
	res := out.(struct {
		Stopped bool              `json:"stopped"`
		Was     agentStatusDetail `json:"was"`
	})
	if !res.Stopped || res.Was.TaskNumber != 3 || res.Was.Mode != "task" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestGetAgentStatusMapsIssue(t *testing.T) {
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{
		statusFn: func(*pb.ProjectId) (*pb.AgentStatus, error) {
			return &pb.AgentStatus{
				ProjectId: "id-demo", ProjectName: "demo", IsRunning: true,
				Mode: "task", TaskNumber: 5, TaskTitle: "t",
				Issue: &pb.AgentIssue{IssueType: "rate_limited", Message: "limit hit"},
			}, nil
		},
	})

	out, err := handleGetAgentStatus(context.Background(), s, agentProjectArgs{})
	if err != nil {
		t.Fatalf("handleGetAgentStatus: %v", err)
	}
	detail := out.(agentStatusDetail)
	if !detail.Running || detail.Mode != "task" || detail.TaskNumber != 5 {
		t.Errorf("unexpected detail: %+v", detail)
	}
	if detail.Issue == nil || detail.Issue.Type != "rate_limited" || detail.Issue.Message != "limit hit" {
		t.Errorf("issue not mapped: %+v", detail.Issue)
	}
}

func TestWaitForTaskValidation(t *testing.T) {
	s := runTestServer(&fakeTaskClient{}, &fakeAgentClient{})

	_, err := handleWaitForTask(context.Background(), s, waitForTaskArgs{})
	if err == nil || !strings.Contains(err.Error(), `"task_number" is required`) {
		t.Errorf("want task_number-required error, got: %v", err)
	}

	_, err = handleWaitForTask(context.Background(), s, waitForTaskArgs{TaskNumber: 1, TimeoutSeconds: -5})
	if err == nil || !strings.Contains(err.Error(), "timeout_seconds") {
		t.Errorf("want timeout validation error, got: %v", err)
	}
}

func TestWaitForTaskAlreadyDone(t *testing.T) {
	s := runTestServer(&fakeTaskClient{
		getFn: func(req *pb.TaskId) (*pb.Task, error) {
			return &pb.Task{
				TaskNumber: req.TaskNumber, Status: "done",
				Success: boolPtr(true),
			}, nil
		},
	}, &fakeAgentClient{})

	out, err := handleWaitForTask(context.Background(), s, waitForTaskArgs{TaskNumber: 4})
	if err != nil {
		t.Fatalf("handleWaitForTask: %v", err)
	}
	res := out.(waitForTaskResult)
	if res.Status != "done" || res.Success == nil || !*res.Success || res.TimedOut {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestWaitForTaskPollsUntilDone(t *testing.T) {
	var polls atomic.Int32
	s := runTestServer(&fakeTaskClient{
		getFn: func(req *pb.TaskId) (*pb.Task, error) {
			if polls.Add(1) < 3 {
				return &pb.Task{TaskNumber: req.TaskNumber, Status: "ready"}, nil
			}
			return &pb.Task{
				TaskNumber: req.TaskNumber, Status: "done",
				Success: boolPtr(false), FailureReason: strPtr("blocked on X"),
			}, nil
		},
	}, &fakeAgentClient{})
	s.pollInterval = 2 * time.Millisecond

	out, err := waitForTask(context.Background(), s, "id-demo", 4, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForTask: %v", err)
	}
	if polls.Load() != 3 {
		t.Errorf("polled %d times, want 3", polls.Load())
	}
	res := out.(waitForTaskResult)
	if res.Status != "done" || res.TimedOut {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.Success == nil || *res.Success || res.FailureReason != "blocked on X" {
		t.Errorf("failure outcome not mapped: %+v", res)
	}
}

func TestWaitForTaskTimeout(t *testing.T) {
	s := runTestServer(&fakeTaskClient{
		getFn: func(req *pb.TaskId) (*pb.Task, error) {
			return &pb.Task{TaskNumber: req.TaskNumber, Status: "ready"}, nil
		},
	}, &fakeAgentClient{
		statusFn: func(*pb.ProjectId) (*pb.AgentStatus, error) {
			return &pb.AgentStatus{ProjectId: "id-demo", IsRunning: true, Mode: "task", TaskNumber: 4}, nil
		},
	})
	s.pollInterval = 2 * time.Millisecond

	out, err := waitForTask(context.Background(), s, "id-demo", 4, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("timeout must be a result, not an error: %v", err)
	}
	res := out.(waitForTaskResult)
	if !res.TimedOut {
		t.Fatalf("timed_out not set: %+v", res)
	}
	if res.Status != "ready" {
		t.Errorf("status = %q, want current state ready", res.Status)
	}
	if res.Agent == nil || !res.Agent.Running || res.Agent.TaskNumber != 4 {
		t.Errorf("agent status missing on timeout: %+v", res.Agent)
	}
	if !strings.Contains(res.Note, "again") {
		t.Errorf("note should tell the client to call again, got: %q", res.Note)
	}
}

func TestWaitForTaskCancelled(t *testing.T) {
	s := runTestServer(&fakeTaskClient{
		getFn: func(req *pb.TaskId) (*pb.Task, error) {
			return &pb.Task{TaskNumber: req.TaskNumber, Status: "ready"}, nil
		},
	}, &fakeAgentClient{})
	s.pollInterval = 2 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := waitForTask(ctx, s, "id-demo", 4, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got: %v", err)
	}
}

func TestWaitForTaskPollError(t *testing.T) {
	s := runTestServer(&fakeTaskClient{
		getFn: func(*pb.TaskId) (*pb.Task, error) {
			return nil, errors.New("boom")
		},
	}, &fakeAgentClient{})

	_, err := waitForTask(context.Background(), s, "id-demo", 4, time.Second)
	if err == nil || !strings.Contains(err.Error(), "failed to poll task 4") {
		t.Errorf("poll error should be surfaced with task context, got: %v", err)
	}
}

// TestRunToolsRegistered guards the registry wiring: all six run tools are
// present, and only get_agent_status carries the inspect group (the group
// field drives --read-only filtering, which keeps that one observation tool).
func TestRunToolsRegistered(t *testing.T) {
	want := map[string]string{
		"run_task": groupRun, "run_all": groupRun, "start_wildfire": groupRun,
		"stop_agent": groupRun, "get_agent_status": groupInspect, "wait_for_task": groupRun,
	}
	seen := map[string]bool{}
	for _, td := range allTools() {
		group, ok := want[td.spec.Name]
		if !ok {
			continue
		}
		seen[td.spec.Name] = true
		if td.spec.Group != group {
			t.Errorf("tool %s registered under group %q, want %q", td.spec.Name, td.spec.Group, group)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("tool %s missing from allTools()", name)
		}
	}
}
