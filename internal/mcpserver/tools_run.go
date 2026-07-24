package mcpserver

import (
	"context"
	"fmt"
	"time"

	pb "github.com/watchfire-io/watchfire/proto"
)

// wait_for_task polling parameters. The daemon has no blocking RPC by design
// (no sleeps in the daemon); synchronization lives entirely in this layer.
const (
	defaultWaitTimeout  = 300 * time.Second
	maxWaitTimeout      = 600 * time.Second
	defaultPollInterval = 2 * time.Second
)

var runTools = []toolDef{
	newTool(groupRun, "run_task",
		"Start a sandboxed coding agent on one task (status \"draft\" or \"ready\"; already-done tasks are rejected). The agent works in an isolated git worktree; when it marks the task done the daemon stops it and merges the branch into the project's default branch. Watchfire runs at most one agent per project: if one is already running this errors immediately, naming its mode and task — it never queues or replaces the run (use stop_agent or wait_for_task first). Returns the started agent status; call wait_for_task to block until the run completes.",
		handleRunTask),
	newTool(groupRun, "run_all",
		"Run every \"ready\" task of a project in sequence: the daemon starts the lowest-positioned ready task and automatically chains to the next one after each merge, until no ready tasks remain. Errors if no task is ready or an agent is already running (never queues or replaces a run). Use wait_for_task on individual task numbers to track progress, or get_agent_status to see which task is currently being worked.",
		handleRunAll),
	newTool(groupRun, "start_wildfire",
		"WARNING: wildfire is Watchfire's autonomous mode — a three-phase loop (Execute ready tasks -> Refine failed ones -> Generate NEW tasks from the project definition) that keeps running until no new work emerges. It can create and execute tasks you never reviewed, so only start it when the user explicitly wants autonomous operation. Errors if an agent is already running. Monitor with get_agent_status (wildfire_phase) and abort with stop_agent.",
		handleStartWildfire),
	newTool(groupRun, "stop_agent",
		"Stop the agent running in a project (SIGTERM to the sandboxed process; the daemon cleans up). Also breaks run_all / wildfire chaining — no next task is started. In-progress work in the task's worktree is left uncommitted or unmerged, not lost. Safe to call when nothing is running: returns stopped: false instead of an error.",
		handleStopAgent),
	newTool(groupRun, "get_agent_status",
		"Get live agent status for a project: whether an agent is running, its mode (chat | task | start-all | wildfire), the task number/title being worked, the wildfire phase, when the session started, and any blocking issue (auth_required / rate_limited) with its message and cooldown — how to tell a working agent from a stuck one.",
		handleGetAgentStatus),
	newTool(groupRun, "wait_for_task",
		"Block until a task reaches status \"done\" (polls the daemon every ~2s), then return its outcome: status, success, failure_reason, and seconds waited. This is the factory loop's synchronization point — call it right after run_task. On timeout (timeout_seconds, default 300, max 600) it returns the CURRENT state with timed_out: true plus live agent status; that is not an error — simply call wait_for_task again to keep waiting. Note \"done\" means the agent finished, not that it succeeded: always check the success flag.",
		handleWaitForTask,
		defaultProperty("timeout_seconds", "300"),
		rangeProperty("timeout_seconds", 1, 600)),
}

type agentProjectArgs struct {
	Project string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
}

type waitForTaskArgs struct {
	Project        string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	TaskNumber     int32  `json:"task_number" jsonschema:"Task number to wait for (see list_tasks)."`
	TimeoutSeconds int32  `json:"timeout_seconds,omitempty" jsonschema:"Seconds to wait before returning the current state with timed_out: true. Default 300, max 600. Call the tool again to keep waiting."`
}

// agentIssueDetail is the full AgentIssue view (get_agent_status), including
// the cooldown fields agentSummary omits.
type agentIssueDetail struct {
	Type          string `json:"type"`
	Message       string `json:"message,omitempty"`
	DetectedAt    string `json:"detected_at,omitempty"`
	ResetAt       string `json:"reset_at,omitempty"`
	CooldownUntil string `json:"cooldown_until,omitempty"`
}

type agentStatusDetail struct {
	ProjectID     string            `json:"project_id"`
	ProjectName   string            `json:"project_name,omitempty"`
	Running       bool              `json:"running"`
	Mode          string            `json:"mode,omitempty"`
	TaskNumber    int32             `json:"task_number,omitempty"`
	TaskTitle     string            `json:"task_title,omitempty"`
	WildfirePhase string            `json:"wildfire_phase,omitempty"`
	StartedAt     string            `json:"started_at,omitempty"`
	Issue         *agentIssueDetail `json:"issue,omitempty"`
}

// runStarted is the result of the three start tools.
type runStarted struct {
	Started bool              `json:"started"`
	Agent   agentStatusDetail `json:"agent"`
	Note    string            `json:"note,omitempty"`
}

type waitForTaskResult struct {
	TaskNumber         int32         `json:"task_number"`
	Status             string        `json:"status"`
	Success            *bool         `json:"success,omitempty"`
	FailureReason      string        `json:"failure_reason,omitempty"`
	MergeFailureReason string        `json:"merge_failure_reason,omitempty"`
	TimedOut           bool          `json:"timed_out,omitempty"`
	WaitedSeconds      int64         `json:"waited_seconds"`
	Agent              *agentSummary `json:"agent,omitempty"`
	AgentError         string        `json:"agent_status_error,omitempty"`
	Note               string        `json:"note,omitempty"`
}

func handleRunTask(ctx context.Context, s *server, args taskRefArgs) (any, error) {
	if args.TaskNumber <= 0 {
		return nil, fmt.Errorf("\"task_number\" is required")
	}
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoRunningAgent(ctx, projectID); err != nil {
		return nil, err
	}

	status, err := s.agents.StartAgent(ctx, startAgentRequest(projectID, "task", args.TaskNumber))
	if err != nil {
		return nil, fmt.Errorf("failed to start agent on task %d: %w", args.TaskNumber, err)
	}
	return runStarted{
		Started: true,
		Agent:   protoAgentStatus(status),
		Note:    fmt.Sprintf("Agent started on task #%d; call wait_for_task to block until it completes.", status.TaskNumber),
	}, nil
}

func handleRunAll(ctx context.Context, s *server, args agentProjectArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoRunningAgent(ctx, projectID); err != nil {
		return nil, err
	}

	status, err := s.agents.StartAgent(ctx, startAgentRequest(projectID, "start-all", 0))
	if err != nil {
		return nil, fmt.Errorf("failed to start run-all: %w", err)
	}
	return runStarted{
		Started: true,
		Agent:   protoAgentStatus(status),
		Note: fmt.Sprintf("Run-all started on task #%d; the daemon chains through the remaining ready tasks after each merge. "+
			"Use wait_for_task per task number or get_agent_status to follow progress.", status.TaskNumber),
	}, nil
}

func handleStartWildfire(ctx context.Context, s *server, args agentProjectArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoRunningAgent(ctx, projectID); err != nil {
		return nil, err
	}

	status, err := s.agents.StartAgent(ctx, startAgentRequest(projectID, "wildfire", 0))
	if err != nil {
		return nil, fmt.Errorf("failed to start wildfire: %w", err)
	}
	return runStarted{
		Started: true,
		Agent:   protoAgentStatus(status),
		Note: "Wildfire started: the autonomous Execute -> Refine -> Generate loop runs until no new work emerges and may create new tasks. " +
			"Monitor with get_agent_status; abort with stop_agent.",
	}, nil
}

func handleStopAgent(ctx context.Context, s *server, args agentProjectArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	// Pre-check so stopping an idle project is a no-op, not an error.
	status, err := s.agents.GetAgentStatus(ctx, &pb.ProjectId{ProjectId: projectID})
	if err != nil {
		return nil, fmt.Errorf("failed to check agent status: %w", err)
	}
	if !status.IsRunning {
		return struct {
			Stopped bool   `json:"stopped"`
			Note    string `json:"note"`
		}{false, "No agent was running for this project."}, nil
	}

	if _, err := s.agents.StopAgent(ctx, &pb.ProjectId{ProjectId: projectID}); err != nil {
		return nil, fmt.Errorf("failed to stop agent: %w", err)
	}
	return struct {
		Stopped bool              `json:"stopped"`
		Was     agentStatusDetail `json:"was"`
	}{true, protoAgentStatus(status)}, nil
}

func handleGetAgentStatus(ctx context.Context, s *server, args agentProjectArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	status, err := s.agents.GetAgentStatus(ctx, &pb.ProjectId{ProjectId: projectID})
	if err != nil {
		return nil, fmt.Errorf("failed to get agent status: %w", err)
	}
	return protoAgentStatus(status), nil
}

func handleWaitForTask(ctx context.Context, s *server, args waitForTaskArgs) (any, error) {
	if args.TaskNumber <= 0 {
		return nil, fmt.Errorf("\"task_number\" is required")
	}
	if args.TimeoutSeconds < 0 {
		return nil, fmt.Errorf("\"timeout_seconds\" must be positive")
	}
	timeout := time.Duration(args.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = defaultWaitTimeout
	} else if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}

	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	return waitForTask(ctx, s, projectID, args.TaskNumber, timeout)
}

// waitForTask polls GetTask until the task is done, the timeout elapses, or
// ctx is cancelled (MCP request cancellation). Timeout is a normal result
// (timed_out: true), not an error — the client re-calls to keep waiting.
func waitForTask(ctx context.Context, s *server, projectID string, taskNumber int32, timeout time.Duration) (any, error) {
	start := time.Now()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		t, err := s.tasks.GetTask(ctx, &pb.TaskId{ProjectId: projectID, TaskNumber: taskNumber})
		if err != nil {
			return nil, fmt.Errorf("failed to poll task %d: %w", taskNumber, err)
		}

		res := waitForTaskResult{
			TaskNumber:    t.TaskNumber,
			Status:        t.Status,
			Success:       t.Success,
			WaitedSeconds: int64(time.Since(start).Round(time.Second) / time.Second),
		}
		if t.FailureReason != nil {
			res.FailureReason = *t.FailureReason
		}
		if t.MergeFailureReason != nil {
			res.MergeFailureReason = *t.MergeFailureReason
		}
		if t.Status == "done" {
			return res, nil
		}

		poll := time.NewTimer(s.taskPollInterval())
		select {
		case <-ctx.Done():
			poll.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			poll.Stop()
			res.TimedOut = true
			res.Agent, res.AgentError = fetchAgentSummary(ctx, s, projectID)
			res.Note = "Timed out while the task is still in progress — this is not an error. Call wait_for_task again to keep waiting."
			return res, nil
		case <-poll.C:
		}
	}
}

// requireNoRunningAgent guards the start tools. The daemon's Manager.StartAgent
// REPLACES a running agent (stops it first), so without this pre-check a start
// tool would silently kill an in-flight run; the MCP contract is to refuse
// instead, naming what is running.
func (s *server) requireNoRunningAgent(ctx context.Context, projectID string) error {
	status, err := s.agents.GetAgentStatus(ctx, &pb.ProjectId{ProjectId: projectID})
	if err != nil {
		return fmt.Errorf("failed to check agent status: %w", err)
	}
	if !status.IsRunning {
		return nil
	}
	desc := fmt.Sprintf("mode %q", status.Mode)
	if status.TaskNumber > 0 {
		desc += fmt.Sprintf(", task #%d %q", status.TaskNumber, status.TaskTitle)
	}
	if status.WildfirePhase != "" {
		desc += fmt.Sprintf(", wildfire phase %q", status.WildfirePhase)
	}
	return fmt.Errorf("an agent is already running for this project (%s); runs are not queued — wait with wait_for_task or abort with stop_agent first", desc)
}

// startAgentRequest builds the common StartAgent request: rows/cols 0 (daemon
// default PTY size) and sandbox "auto" (project setting decides).
func startAgentRequest(projectID, mode string, taskNumber int32) *pb.StartAgentRequest {
	return &pb.StartAgentRequest{
		Meta:       &pb.RequestMeta{Origin: "mcp"},
		ProjectId:  projectID,
		Mode:       mode,
		TaskNumber: taskNumber,
		Sandbox:    "auto",
	}
}

func protoAgentStatus(st *pb.AgentStatus) agentStatusDetail {
	d := agentStatusDetail{
		ProjectID:     st.ProjectId,
		ProjectName:   st.ProjectName,
		Running:       st.IsRunning,
		Mode:          st.Mode,
		TaskNumber:    st.TaskNumber,
		TaskTitle:     st.TaskTitle,
		WildfirePhase: st.WildfirePhase,
		StartedAt:     formatTimestamp(st.StartedAt),
	}
	if st.Issue != nil {
		d.Issue = &agentIssueDetail{
			Type:          st.Issue.IssueType,
			Message:       st.Issue.Message,
			DetectedAt:    formatTimestamp(st.Issue.DetectedAt),
			ResetAt:       formatTimestamp(st.Issue.ResetAt),
			CooldownUntil: formatTimestamp(st.Issue.CooldownUntil),
		}
	}
	return d
}

func (s *server) taskPollInterval() time.Duration {
	if s.pollInterval > 0 {
		return s.pollInterval
	}
	return defaultPollInterval
}
