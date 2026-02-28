package server

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/analytics"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/prompts"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

type agentService struct {
	pb.UnimplementedAgentServiceServer
	manager *agent.Manager
	watcher *watcher.Watcher
}

func (s *agentService) StartAgent(_ context.Context, req *pb.StartAgentRequest) (*pb.AgentStatus, error) {
	// Resolve project path from ID
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	// Load project for name
	proj, err := config.LoadProject(entry.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load project: %w", err)
	}

	mode := agent.Mode(req.Mode)
	if mode == "" {
		mode = agent.ModeChat
	}

	analytics.Track("agent_started", posthog.NewProperties().
		Set("origin", req.GetMeta().GetOrigin()).
		Set("mode", string(mode)))

	// Task/Wildfire mode: look up task details and compose prompts
	var taskTitle, taskPrompt, taskSystemPrompt string
	var taskNumber int32

	switch mode {
	case agent.ModeTask:
		taskNumber, taskTitle, taskPrompt, taskSystemPrompt, err = s.setupTaskMode(entry.Path, proj, req.TaskNumber)
		if err != nil {
			return nil, err
		}
	case agent.ModeStartAll:
		taskNumber, taskTitle, taskPrompt, taskSystemPrompt, err = s.setupStartAllMode(entry.Path, proj)
		if err != nil {
			return nil, err
		}
	case agent.ModeWildfire:
		return s.startWildfireMode(req, entry.Path, proj)
	case agent.ModeGenerateDefinition:
		return s.startGenerateMode(req, entry.Path, proj, agent.ModeGenerateDefinition)
	case agent.ModeGenerateTasks:
		return s.startGenerateMode(req, entry.Path, proj, agent.ModeGenerateTasks)
	default:
		taskNumber = req.TaskNumber
	}

	running, err := s.manager.StartAgent(agent.StartOptions{
		ProjectID:        req.ProjectId,
		ProjectName:      proj.Name,
		ProjectPath:      entry.Path,
		ProjectColor:     proj.Color,
		Mode:             mode,
		TaskNumber:       int(taskNumber),
		TaskTitle:        taskTitle,
		TaskPrompt:       taskPrompt,
		TaskSystemPrompt: taskSystemPrompt,
		Rows:             int(req.Rows),
		Cols:             int(req.Cols),
	})
	if err != nil {
		return nil, err
	}

	// Watch project for task file changes
	if s.watcher != nil {
		_ = s.watcher.WatchProject(req.ProjectId, entry.Path)
	}

	return &pb.AgentStatus{
		ProjectId:     running.ProjectID,
		ProjectName:   running.ProjectName,
		Mode:          string(running.Mode),
		TaskNumber:    int32(running.TaskNumber),
		TaskTitle:     running.TaskTitle,
		IsRunning:     true,
		WildfirePhase: string(running.WildfirePhase),
	}, nil
}

func (s *agentService) setupTaskMode(projectPath string, proj *models.Project, reqTaskNumber int32) (taskNumber int32, taskTitle, taskPrompt, taskSystemPrompt string, err error) {
	taskMgr := task.NewManager()
	t, err := taskMgr.GetTask(projectPath, int(reqTaskNumber))
	if err != nil {
		return 0, "", "", "", fmt.Errorf("failed to load task: %w", err)
	}
	if t.Status == models.TaskStatusDone {
		return 0, "", "", "", fmt.Errorf("task #%04d is already done", reqTaskNumber)
	}
	return reqTaskNumber, t.Title,
		prompts.ComposeTaskUserPrompt(int(reqTaskNumber), t.Title),
		prompts.ComposeTaskSystemPrompt(proj, int(reqTaskNumber), t.Title, t.Prompt, t.AcceptanceCriteria),
		nil
}

func (s *agentService) setupStartAllMode(projectPath string, proj *models.Project) (taskNumber int32, taskTitle, taskPrompt, taskSystemPrompt string, err error) {
	taskMgr := task.NewManager()
	readyStatus := string(models.TaskStatusReady)
	tasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{Status: &readyStatus})
	if err != nil {
		return 0, "", "", "", fmt.Errorf("failed to list ready tasks: %w", err)
	}
	if len(tasks) == 0 {
		return 0, "", "", "", fmt.Errorf("no ready tasks found for start-all mode")
	}
	t := tasks[0]
	return int32(t.TaskNumber), t.Title,
		prompts.ComposeTaskUserPrompt(t.TaskNumber, t.Title),
		prompts.ComposeTaskSystemPrompt(proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria),
		nil
}

func (s *agentService) startWildfireMode(req *pb.StartAgentRequest, projectPath string, proj *models.Project) (*pb.AgentStatus, error) {
	taskMgr := task.NewManager()
	var wildfirePhase agent.WildfirePhase
	var taskTitle, taskPrompt, taskSystemPrompt string
	var taskNumber int32

	// 1. Check for ready tasks → Execute phase
	readyStatus := string(models.TaskStatusReady)
	readyTasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{Status: &readyStatus})
	if err != nil {
		return nil, fmt.Errorf("failed to list ready tasks: %w", err)
	}
	if len(readyTasks) > 0 {
		t := readyTasks[0]
		wildfirePhase = agent.WildfirePhaseExecute
		taskTitle = t.Title
		taskSystemPrompt = prompts.ComposeTaskSystemPrompt(proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria)
		taskPrompt = prompts.ComposeTaskUserPrompt(t.TaskNumber, t.Title)
		taskNumber = int32(t.TaskNumber)
	} else {
		// 2. Check for draft tasks → Refine phase
		draftStatus := string(models.TaskStatusDraft)
		draftTasks, err := taskMgr.ListTasks(projectPath, task.ListOptions{Status: &draftStatus})
		if err != nil {
			return nil, fmt.Errorf("failed to list draft tasks: %w", err)
		}
		if len(draftTasks) > 0 {
			t := draftTasks[0]
			wildfirePhase = agent.WildfirePhaseRefine
			taskTitle = t.Title
			taskNumber = int32(t.TaskNumber)
			taskSystemPrompt = prompts.ComposeWildfireRefineSystemPrompt(proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria)
			taskPrompt = prompts.ComposeWildfireRefineUserPrompt(t.TaskNumber, t.Title)
		} else {
			// 3. No tasks at all → Generate phase
			wildfirePhase = agent.WildfirePhaseGenerate
			taskSystemPrompt = prompts.ComposeWildfireGenerateSystemPrompt(proj)
			taskPrompt = prompts.ComposeWildfireGenerateUserPrompt()
		}
	}

	running, err := s.manager.StartAgent(agent.StartOptions{
		ProjectID:        req.ProjectId,
		ProjectName:      proj.Name,
		ProjectPath:      projectPath,
		ProjectColor:     proj.Color,
		Mode:             agent.ModeWildfire,
		WildfirePhase:    wildfirePhase,
		TaskNumber:       int(taskNumber),
		TaskTitle:        taskTitle,
		TaskPrompt:       taskPrompt,
		TaskSystemPrompt: taskSystemPrompt,
		Rows:             int(req.Rows),
		Cols:             int(req.Cols),
	})
	if err != nil {
		return nil, err
	}

	if s.watcher != nil {
		_ = s.watcher.WatchProject(req.ProjectId, projectPath)
	}

	return &pb.AgentStatus{
		ProjectId:     running.ProjectID,
		ProjectName:   running.ProjectName,
		Mode:          string(running.Mode),
		TaskNumber:    int32(running.TaskNumber),
		TaskTitle:     running.TaskTitle,
		IsRunning:     true,
		WildfirePhase: string(running.WildfirePhase),
	}, nil
}

func (s *agentService) startGenerateMode(req *pb.StartAgentRequest, projectPath string, proj *models.Project, mode agent.Mode) (*pb.AgentStatus, error) {
	var taskSystemPrompt, taskPrompt string
	switch mode {
	case agent.ModeGenerateDefinition:
		taskSystemPrompt = prompts.ComposeGenerateDefinitionSystemPrompt(proj)
		taskPrompt = prompts.ComposeGenerateDefinitionUserPrompt()
	case agent.ModeGenerateTasks:
		taskSystemPrompt = prompts.ComposeGenerateTasksSystemPrompt(proj)
		taskPrompt = prompts.ComposeGenerateTasksUserPrompt()
	}

	running, err := s.manager.StartAgent(agent.StartOptions{
		ProjectID:        req.ProjectId,
		ProjectName:      proj.Name,
		ProjectPath:      projectPath,
		ProjectColor:     proj.Color,
		Mode:             mode,
		TaskPrompt:       taskPrompt,
		TaskSystemPrompt: taskSystemPrompt,
		Rows:             int(req.Rows),
		Cols:             int(req.Cols),
	})
	if err != nil {
		return nil, err
	}

	if s.watcher != nil {
		_ = s.watcher.WatchProject(req.ProjectId, projectPath)
	}

	return &pb.AgentStatus{
		ProjectId:   running.ProjectID,
		ProjectName: running.ProjectName,
		Mode:        string(running.Mode),
		IsRunning:   true,
	}, nil
}

func (s *agentService) StopAgent(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	if err := s.manager.StopAgentByUser(req.ProjectId); err != nil {
		return nil, err
	}
	analytics.Track("agent_stopped", posthog.NewProperties().Set("origin", req.GetMeta().GetOrigin()))
	return &emptypb.Empty{}, nil
}

func (s *agentService) GetAgentStatus(_ context.Context, req *pb.ProjectId) (*pb.AgentStatus, error) {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return &pb.AgentStatus{
			ProjectId: req.ProjectId,
			IsRunning: false,
		}, nil
	}
	return buildAgentStatus(running), nil
}

// buildAgentStatus creates an AgentStatus proto from a RunningAgent.
func buildAgentStatus(running *agent.RunningAgent) *pb.AgentStatus {
	status := &pb.AgentStatus{
		ProjectId:     running.ProjectID,
		ProjectName:   running.ProjectName,
		Mode:          string(running.Mode),
		TaskNumber:    int32(running.TaskNumber),
		TaskTitle:     running.TaskTitle,
		IsRunning:     true,
		WildfirePhase: string(running.WildfirePhase),
	}

	if issue := running.Process.GetIssue(); issue != nil {
		status.Issue = issueToProto(issue)
	}

	return status
}

// issueToProto converts an agent.AgentIssue to a proto AgentIssue.
func issueToProto(issue *agent.AgentIssue) *pb.AgentIssue {
	if issue == nil {
		return nil
	}
	pbIssue := &pb.AgentIssue{
		IssueType:  string(issue.Type),
		DetectedAt: timestamppb.New(issue.DetectedAt),
		Message:    issue.Message,
	}
	if issue.ResetAt != nil {
		pbIssue.ResetAt = timestamppb.New(*issue.ResetAt)
	}
	if issue.CooldownUntil != nil {
		pbIssue.CooldownUntil = timestamppb.New(*issue.CooldownUntil)
	}
	return pbIssue
}

func (s *agentService) SubscribeRawOutput(req *pb.SubscribeRawOutputRequest, stream grpc.ServerStreamingServer[pb.RawOutputChunk]) error {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}

	subID := uuid.New().String()
	ch := running.Process.SubscribeRaw(subID)
	defer running.Process.UnsubscribeRaw(subID)

	// Send buffered output for late-join catch-up
	if buf := running.Process.GetRawBuffer(); len(buf) > 0 {
		if err := stream.Send(&pb.RawOutputChunk{
			ProjectId: req.ProjectId,
			Data:      buf,
		}); err != nil {
			return err
		}
	}

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.RawOutputChunk{
				ProjectId: req.ProjectId,
				Data:      data,
			}); err != nil {
				return err
			}
		case <-running.Process.Done():
			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *agentService) SubscribeScreen(req *pb.SubscribeScreenRequest, stream grpc.ServerStreamingServer[pb.ScreenBuffer]) error {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}

	subID := uuid.New().String()
	ch := running.Process.SubscribeScreen(subID)
	defer running.Process.UnsubscribeScreen(subID)

	// Send initial snapshot so the TUI sees the current screen when connecting
	if snapshot := running.Process.SnapshotScreen(); snapshot != nil {
		if err := stream.Send(&pb.ScreenBuffer{
			ProjectId:   snapshot.ProjectID,
			Lines:       snapshot.Lines,
			CursorRow:   int32(snapshot.CursorRow),
			CursorCol:   int32(snapshot.CursorCol),
			Rows:        int32(snapshot.Rows),
			Cols:        int32(snapshot.Cols),
			AnsiContent: snapshot.AnsiContent,
		}); err != nil {
			return err
		}
	}

	for {
		select {
		case update, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.ScreenBuffer{
				ProjectId:   update.ProjectID,
				Lines:       update.Lines,
				CursorRow:   int32(update.CursorRow),
				CursorCol:   int32(update.CursorCol),
				Rows:        int32(update.Rows),
				Cols:        int32(update.Cols),
				AnsiContent: update.AnsiContent,
			}); err != nil {
				return err
			}
		case <-running.Process.Done():
			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *agentService) SendInput(_ context.Context, req *pb.SendInputRequest) (*emptypb.Empty, error) {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return nil, fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}

	if err := running.Process.SendInput(req.Data); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *agentService) Resize(_ context.Context, req *pb.ResizeRequest) (*emptypb.Empty, error) {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return nil, fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}

	if err := running.Process.Resize(int(req.Rows), int(req.Cols)); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *agentService) GetScrollback(_ context.Context, req *pb.ScrollbackRequest) (*pb.ScrollbackLines, error) {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return nil, fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}

	lines, total := running.Process.GetScrollback(int(req.Offset), int(req.Limit))
	return &pb.ScrollbackLines{
		Lines:      lines,
		TotalLines: int32(total),
	}, nil
}

func (s *agentService) SubscribeAgentIssues(req *pb.SubscribeAgentIssuesRequest, stream grpc.ServerStreamingServer[pb.AgentIssue]) error {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}

	subID := uuid.New().String()
	ch := running.Process.SubscribeIssues(subID)
	defer running.Process.UnsubscribeIssues(subID)

	// Send current issue immediately if any
	if issue := running.Process.GetIssue(); issue != nil {
		if err := stream.Send(issueToProto(issue)); err != nil {
			return err
		}
	}

	for {
		select {
		case issue, ok := <-ch:
			if !ok {
				return nil
			}
			pbIssue := issueToProto(issue)
			if pbIssue == nil {
				pbIssue = &pb.AgentIssue{IssueType: ""}
			}
			if err := stream.Send(pbIssue); err != nil {
				return err
			}
		case <-running.Process.Done():
			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *agentService) ResumeAgent(_ context.Context, req *pb.ProjectId) (*pb.AgentStatus, error) {
	running, ok := s.manager.GetAgent(req.ProjectId)
	if !ok {
		return nil, fmt.Errorf("no agent running for project: %s", req.ProjectId)
	}
	running.Process.ClearIssue()
	return buildAgentStatus(running), nil
}
