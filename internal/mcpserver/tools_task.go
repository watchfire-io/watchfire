package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/watchfire-io/watchfire/proto"
)

var taskTools = []toolDef{
	newTool(groupTask, "create_task",
		"Create a task in a Watchfire project through the daemon's validated write path. A task is a unit of work a sandboxed coding agent executes in an isolated git worktree, merged into the project's default branch on success. status \"draft\" (the default) files the task for review without running anything; status \"ready\" queues it for execution — and may immediately auto-start an agent if the project has auto_start_tasks enabled (check get_project). \"agent\" overrides the project's default backend for this task and must be a registered backend name. Returns the created task including its assigned task_number.",
		handleCreateTask,
		enumProperty("status", "draft", "ready"),
		defaultProperty("status", `"draft"`)),
	newTool(groupTask, "list_tasks",
		"List the tasks of a Watchfire project: number, title, status (draft | ready | done), success flag for done tasks, agent override, and position. Soft-deleted (trashed) tasks are excluded unless include_deleted is true. Use get_task for the full prompt, acceptance criteria, failure reason, and timestamps of one task.",
		handleListTasks),
	newTool(groupTask, "get_task",
		"Get one task in full: title, prompt, acceptance criteria, status (draft | ready | done), success, failure_reason (why the agent reported failure), merge_failure_reason (why the post-task merge failed, if it did), agent override, position, session count, and timestamps. This is where to check the outcome after a run finishes.",
		handleGetTask),
	newTool(groupTask, "update_task",
		"Update fields of an existing task: title, prompt, acceptance_criteria, status, agent (backend override; empty string clears it back to the project default), position. Only the fields you pass change. Status may only be flipped between \"draft\" and \"ready\" here — \"done\" is written by the executing agent, not by clients. Setting \"ready\" may immediately auto-start an agent if the project has auto_start_tasks enabled. The daemon rejects invalid edits (e.g. to a task an agent is currently running); its error is returned as-is.",
		handleUpdateTask,
		enumProperty("status", "draft", "ready")),
	newTool(groupTask, "delete_task",
		"Soft-delete a task: it moves to the project's trash and stops being eligible to run. This is reversible — a human can restore it from the Trash view in the Watchfire TUI or GUI. Permanent deletion is intentionally not available over MCP.",
		handleDeleteTask),
}

type createTaskArgs struct {
	Project            string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	Title              string `json:"title" jsonschema:"Short task title."`
	Prompt             string `json:"prompt" jsonschema:"Full instructions for the coding agent: what to build or change, with enough context to work autonomously."`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty" jsonschema:"Verifiable conditions that define success for the task."`
	Status             string `json:"status,omitempty" jsonschema:"Initial status. \"draft\" files the task for review; \"ready\" queues it to run and may auto-start an agent if the project has auto_start_tasks enabled. Defaults to \"draft\"."`
	Agent              string `json:"agent,omitempty" jsonschema:"Agent backend override for this task (e.g. \"claude-code\"). Must be a registered backend name; omit to use the project default."`
	Position           *int32 `json:"position,omitempty" jsonschema:"Position in the task list; omit to append at the end."`
}

type listTasksArgs struct {
	Project        string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	IncludeDeleted bool   `json:"include_deleted,omitempty" jsonschema:"Also include soft-deleted (trashed) tasks. Defaults to false."`
}

// taskRefArgs identifies one task for get_task / delete_task.
type taskRefArgs struct {
	Project    string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	TaskNumber int32  `json:"task_number" jsonschema:"Task number within the project (see list_tasks)."`
}

type updateTaskArgs struct {
	Project            string  `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
	TaskNumber         int32   `json:"task_number" jsonschema:"Task number within the project (see list_tasks)."`
	Title              *string `json:"title,omitempty" jsonschema:"New title."`
	Prompt             *string `json:"prompt,omitempty" jsonschema:"New agent instructions."`
	AcceptanceCriteria *string `json:"acceptance_criteria,omitempty" jsonschema:"New acceptance criteria."`
	Status             *string `json:"status,omitempty" jsonschema:"New status: \"draft\" or \"ready\" only. Setting \"ready\" may auto-start an agent if the project has auto_start_tasks enabled."`
	Agent              *string `json:"agent,omitempty" jsonschema:"Agent backend override (e.g. \"claude-code\"). Pass an empty string to clear the override back to the project default."`
	Position           *int32  `json:"position,omitempty" jsonschema:"New position in the task list."`
}

// taskSummary is one list_tasks row.
type taskSummary struct {
	TaskNumber int32  `json:"task_number"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Success    *bool  `json:"success,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Position   int32  `json:"position"`
	DeletedAt  string `json:"deleted_at,omitempty"`
}

// taskDetail is the full task view returned by create/get/update/delete.
type taskDetail struct {
	TaskID             string `json:"task_id"`
	TaskNumber         int32  `json:"task_number"`
	Title              string `json:"title"`
	Prompt             string `json:"prompt"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Status             string `json:"status"`
	Success            *bool  `json:"success,omitempty"`
	FailureReason      string `json:"failure_reason,omitempty"`
	MergeFailureReason string `json:"merge_failure_reason,omitempty"`
	Agent              string `json:"agent,omitempty"`
	Position           int32  `json:"position"`
	AgentSessions      int32  `json:"agent_sessions"`
	CreatedAt          string `json:"created_at,omitempty"`
	StartedAt          string `json:"started_at,omitempty"`
	CompletedAt        string `json:"completed_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
	DeletedAt          string `json:"deleted_at,omitempty"`
}

func handleCreateTask(ctx context.Context, s *server, args createTaskArgs) (any, error) {
	if strings.TrimSpace(args.Title) == "" {
		return nil, fmt.Errorf("\"title\" is required")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return nil, fmt.Errorf("\"prompt\" is required")
	}
	status := args.Status
	if status == "" {
		status = "draft"
	}
	if status != "draft" && status != "ready" {
		return nil, fmt.Errorf("invalid status %q: must be \"draft\" or \"ready\"", status)
	}

	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	if args.Agent != "" {
		if err := s.validateAgent(ctx, args.Agent); err != nil {
			return nil, err
		}
	}

	req := &pb.CreateTaskRequest{
		ProjectId: projectID,
		Title:     args.Title,
		Prompt:    args.Prompt,
		Status:    status,
	}
	if args.AcceptanceCriteria != "" {
		req.AcceptanceCriteria = &args.AcceptanceCriteria
	}
	if args.Agent != "" {
		req.Agent = &args.Agent
	}
	req.Position = args.Position

	t, err := s.tasks.CreateTask(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}
	return protoTaskDetail(t), nil
}

func handleListTasks(ctx context.Context, s *server, args listTasksArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	list, err := s.tasks.ListTasks(ctx, &pb.ListTasksRequest{
		ProjectId:      projectID,
		IncludeDeleted: args.IncludeDeleted,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	rows := make([]taskSummary, 0, len(list.Tasks))
	for _, t := range list.Tasks {
		rows = append(rows, taskSummary{
			TaskNumber: t.TaskNumber,
			Title:      t.Title,
			Status:     t.Status,
			Success:    t.Success,
			Agent:      t.Agent,
			Position:   t.Position,
			DeletedAt:  formatTimestamp(t.DeletedAt),
		})
	}

	return struct {
		Tasks []taskSummary `json:"tasks"`
	}{rows}, nil
}

func handleGetTask(ctx context.Context, s *server, args taskRefArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	t, err := s.tasks.GetTask(ctx, &pb.TaskId{ProjectId: projectID, TaskNumber: args.TaskNumber})
	if err != nil {
		return nil, fmt.Errorf("failed to get task %d: %w", args.TaskNumber, err)
	}
	return protoTaskDetail(t), nil
}

func handleUpdateTask(ctx context.Context, s *server, args updateTaskArgs) (any, error) {
	if args.Title == nil && args.Prompt == nil && args.AcceptanceCriteria == nil &&
		args.Status == nil && args.Agent == nil && args.Position == nil {
		return nil, fmt.Errorf("nothing to update: pass at least one of title, prompt, acceptance_criteria, status, agent, position")
	}
	if args.Status != nil && *args.Status != "draft" && *args.Status != "ready" {
		return nil, fmt.Errorf("invalid status %q: only \"draft\" and \"ready\" may be set here (\"done\" is written by the executing agent)", *args.Status)
	}

	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}
	if args.Agent != nil && *args.Agent != "" {
		if err := s.validateAgent(ctx, *args.Agent); err != nil {
			return nil, err
		}
	}

	t, err := s.tasks.UpdateTask(ctx, &pb.UpdateTaskRequest{
		ProjectId:          projectID,
		TaskNumber:         args.TaskNumber,
		Title:              args.Title,
		Prompt:             args.Prompt,
		AcceptanceCriteria: args.AcceptanceCriteria,
		Status:             args.Status,
		Agent:              args.Agent,
		Position:           args.Position,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update task %d: %w", args.TaskNumber, err)
	}
	return protoTaskDetail(t), nil
}

func handleDeleteTask(ctx context.Context, s *server, args taskRefArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	t, err := s.tasks.DeleteTask(ctx, &pb.TaskId{ProjectId: projectID, TaskNumber: args.TaskNumber})
	if err != nil {
		return nil, fmt.Errorf("failed to delete task %d: %w", args.TaskNumber, err)
	}

	return struct {
		Deleted bool       `json:"deleted"`
		Note    string     `json:"note"`
		Task    taskDetail `json:"task"`
	}{
		Deleted: true,
		Note:    "Task moved to trash; restorable from the Watchfire TUI/GUI Trash view.",
		Task:    protoTaskDetail(t),
	}, nil
}

// validateAgent checks an agent backend override against the daemon's
// registry, returning the valid backend names on mismatch.
func (s *server) validateAgent(ctx context.Context, name string) error {
	list, err := s.settings.ListAgents(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to list agent backends: %w", err)
	}
	names := make([]string, 0, len(list.Agents))
	for _, a := range list.Agents {
		if a.Name == name {
			return nil
		}
		names = append(names, a.Name)
	}
	return fmt.Errorf("unknown agent %q; valid agents: %s", name, strings.Join(names, ", "))
}

func protoTaskDetail(t *pb.Task) taskDetail {
	d := taskDetail{
		TaskID:             t.TaskId,
		TaskNumber:         t.TaskNumber,
		Title:              t.Title,
		Prompt:             t.Prompt,
		AcceptanceCriteria: t.AcceptanceCriteria,
		Status:             t.Status,
		Success:            t.Success,
		Agent:              t.Agent,
		Position:           t.Position,
		AgentSessions:      t.AgentSessions,
		CreatedAt:          formatTimestamp(t.CreatedAt),
		StartedAt:          formatTimestamp(t.StartedAt),
		CompletedAt:        formatTimestamp(t.CompletedAt),
		UpdatedAt:          formatTimestamp(t.UpdatedAt),
		DeletedAt:          formatTimestamp(t.DeletedAt),
	}
	if t.FailureReason != nil {
		d.FailureReason = *t.FailureReason
	}
	if t.MergeFailureReason != nil {
		d.MergeFailureReason = *t.MergeFailureReason
	}
	return d
}

// enumProperty constrains a string property of an inferred input schema to a
// fixed set of values.
func enumProperty(name string, values ...string) func(*jsonschema.Schema) {
	return func(s *jsonschema.Schema) {
		enum := make([]any, len(values))
		for i, v := range values {
			enum[i] = v
		}
		mustProperty(s, name).Enum = enum
	}
}

// defaultProperty sets the default (raw JSON) applied by the SDK when the
// property is omitted from a tool call.
func defaultProperty(name, rawJSON string) func(*jsonschema.Schema) {
	return func(s *jsonschema.Schema) {
		mustProperty(s, name).Default = json.RawMessage(rawJSON)
	}
}

func mustProperty(s *jsonschema.Schema, name string) *jsonschema.Schema {
	p, ok := s.Properties[name]
	if !ok {
		panic(fmt.Sprintf("schema has no property %q", name))
	}
	return p
}
