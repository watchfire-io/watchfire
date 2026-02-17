package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/prompts"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// ============================================================================
// Service Implementations
// ============================================================================

// --- ProjectService ---

type projectService struct {
	pb.UnimplementedProjectServiceServer
	manager *project.Manager
}

func (s *projectService) ListProjects(_ context.Context, _ *emptypb.Empty) (*pb.ProjectList, error) {
	results, err := s.manager.ListProjects()
	if err != nil {
		return nil, err
	}

	list := &pb.ProjectList{Projects: make([]*pb.Project, 0, len(results))}
	for _, pwe := range results {
		list.Projects = append(list.Projects, modelToProtoProject(pwe))
	}
	return list, nil
}

func (s *projectService) GetProject(_ context.Context, req *pb.ProjectId) (*pb.Project, error) {
	pwe, err := s.manager.GetProject(req.ProjectId)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

func (s *projectService) CreateProject(_ context.Context, req *pb.CreateProjectRequest) (*pb.Project, error) {
	pwe, err := s.manager.CreateProject(project.CreateOptions{
		Path:             req.Path,
		Name:             req.Name,
		Definition:       req.Definition,
		DefaultBranch:    req.DefaultBranch,
		AutoMerge:        req.AutoMerge,
		AutoDeleteBranch: req.AutoDeleteBranch,
		AutoStartTasks:   req.AutoStartTasks,
	})
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

func (s *projectService) UpdateProject(_ context.Context, req *pb.UpdateProjectRequest) (*pb.Project, error) {
	opts := project.UpdateOptions{ProjectID: req.ProjectId}
	if req.Name != nil {
		opts.Name = req.Name
	}
	if req.Color != nil {
		opts.Color = req.Color
	}
	if req.DefaultBranch != nil {
		opts.DefaultBranch = req.DefaultBranch
	}
	if req.DefaultAgent != nil {
		opts.DefaultAgent = req.DefaultAgent
	}
	if req.AutoMerge != nil {
		opts.AutoMerge = req.AutoMerge
	}
	if req.AutoDeleteBranch != nil {
		opts.AutoDeleteBranch = req.AutoDeleteBranch
	}
	if req.AutoStartTasks != nil {
		opts.AutoStartTasks = req.AutoStartTasks
	}
	if req.Definition != nil {
		opts.Definition = req.Definition
	}

	pwe, err := s.manager.UpdateProject(opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

func (s *projectService) DeleteProject(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	err := s.manager.DeleteProject(req.ProjectId)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// --- TaskService ---

type taskService struct {
	pb.UnimplementedTaskServiceServer
	manager *task.Manager
}

func (s *taskService) ListTasks(_ context.Context, req *pb.ListTasksRequest) (*pb.TaskList, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	opts := task.ListOptions{
		IncludeDeleted: req.IncludeDeleted,
	}
	if req.Status != nil {
		opts.Status = req.Status
	}

	tasks, err := s.manager.ListTasks(entry.Path, opts)
	if err != nil {
		return nil, err
	}

	list := &pb.TaskList{Tasks: make([]*pb.Task, 0, len(tasks))}
	for _, t := range tasks {
		list.Tasks = append(list.Tasks, modelToProtoTask(t, req.ProjectId))
	}
	return list, nil
}

func (s *taskService) GetTask(_ context.Context, req *pb.TaskId) (*pb.Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	t, err := s.manager.GetTask(entry.Path, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) CreateTask(_ context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	opts := task.CreateOptions{
		Title:  req.Title,
		Prompt: req.Prompt,
		Status: req.Status,
	}
	if req.AcceptanceCriteria != nil {
		opts.AcceptanceCriteria = *req.AcceptanceCriteria
	}
	if req.Position != nil {
		pos := int(*req.Position)
		opts.Position = &pos
	}

	t, err := s.manager.CreateTask(entry.Path, opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) UpdateTask(_ context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	opts := task.UpdateOptions{TaskNumber: int(req.TaskNumber)}
	if req.Title != nil {
		opts.Title = req.Title
	}
	if req.Prompt != nil {
		opts.Prompt = req.Prompt
	}
	if req.AcceptanceCriteria != nil {
		opts.AcceptanceCriteria = req.AcceptanceCriteria
	}
	if req.Status != nil {
		opts.Status = req.Status
	}
	if req.Success != nil {
		opts.Success = req.Success
	}
	if req.FailureReason != nil {
		opts.FailureReason = req.FailureReason
	}
	if req.Position != nil {
		pos := int(*req.Position)
		opts.Position = &pos
	}

	t, err := s.manager.UpdateTask(entry.Path, opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) DeleteTask(_ context.Context, req *pb.TaskId) (*pb.Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	t, err := s.manager.DeleteTask(entry.Path, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) RestoreTask(_ context.Context, req *pb.TaskId) (*pb.Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	t, err := s.manager.RestoreTask(entry.Path, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) EmptyTrash(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	if err := s.manager.EmptyTrash(entry.Path); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// --- DaemonService ---

type daemonService struct {
	pb.UnimplementedDaemonServiceServer
	server *Server
}

func (s *daemonService) GetStatus(_ context.Context, _ *emptypb.Empty) (*pb.DaemonStatus, error) {
	info, err := config.LoadDaemonInfo()
	if err != nil {
		return nil, err
	}

	agents := s.server.agentManager.ListAgents()
	activeProjects := make([]string, 0, len(agents))
	for _, a := range agents {
		activeProjects = append(activeProjects, a.ProjectID)
	}

	updateAvailable, updateVersion, updateURL := s.server.GetUpdateState()

	return &pb.DaemonStatus{
		Host:             info.Host,
		Port:             int32(info.Port),
		Pid:              int32(info.PID),
		StartedAt:        timestamppb.New(info.StartedAt),
		ActiveAgents:     int32(len(agents)),
		ActiveProjects:   activeProjects,
		UpdateAvailable:  updateAvailable,
		UpdateVersion:    updateVersion,
		UpdateUrl:        updateURL,
	}, nil
}

func (s *daemonService) Shutdown(_ context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
	}()
	return &emptypb.Empty{}, nil
}

// --- AgentService ---

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

	// Task/Wildfire mode: look up task details and compose prompts
	var taskTitle, taskPrompt, taskSystemPrompt string
	var taskNumber int32
	if mode == agent.ModeTask {
		taskMgr := task.NewManager()
		t, err := taskMgr.GetTask(entry.Path, int(req.TaskNumber))
		if err != nil {
			return nil, fmt.Errorf("failed to load task: %w", err)
		}
		if t.Status == models.TaskStatusDone {
			return nil, fmt.Errorf("task #%04d is already done", req.TaskNumber)
		}
		taskTitle = t.Title
		taskSystemPrompt = prompts.ComposeTaskSystemPrompt(
			proj, int(req.TaskNumber), t.Title, t.Prompt, t.AcceptanceCriteria,
		)
		taskPrompt = prompts.ComposeTaskUserPrompt(int(req.TaskNumber), t.Title)
		taskNumber = req.TaskNumber
	} else if mode == agent.ModeStartAll {
		taskMgr := task.NewManager()
		readyStatus := string(models.TaskStatusReady)
		tasks, err := taskMgr.ListTasks(entry.Path, task.ListOptions{Status: &readyStatus})
		if err != nil {
			return nil, fmt.Errorf("failed to list ready tasks: %w", err)
		}
		if len(tasks) == 0 {
			return nil, fmt.Errorf("no ready tasks found for start-all mode")
		}
		t := tasks[0]
		taskTitle = t.Title
		taskSystemPrompt = prompts.ComposeTaskSystemPrompt(
			proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria,
		)
		taskPrompt = prompts.ComposeTaskUserPrompt(t.TaskNumber, t.Title)
		taskNumber = int32(t.TaskNumber)
	} else if mode == agent.ModeWildfire {
		// Wildfire initial start: determine phase based on available tasks
		taskMgr := task.NewManager()
		var wildfirePhase agent.WildfirePhase

		// 1. Check for ready tasks → Execute phase
		readyStatus := string(models.TaskStatusReady)
		readyTasks, err := taskMgr.ListTasks(entry.Path, task.ListOptions{Status: &readyStatus})
		if err != nil {
			return nil, fmt.Errorf("failed to list ready tasks: %w", err)
		}
		if len(readyTasks) > 0 {
			t := readyTasks[0]
			wildfirePhase = agent.WildfirePhaseExecute
			taskTitle = t.Title
			taskSystemPrompt = prompts.ComposeTaskSystemPrompt(
				proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria,
			)
			taskPrompt = prompts.ComposeTaskUserPrompt(t.TaskNumber, t.Title)
			taskNumber = int32(t.TaskNumber)
		} else {
			// 2. Check for draft tasks → Refine phase
			draftStatus := string(models.TaskStatusDraft)
			draftTasks, err := taskMgr.ListTasks(entry.Path, task.ListOptions{Status: &draftStatus})
			if err != nil {
				return nil, fmt.Errorf("failed to list draft tasks: %w", err)
			}
			if len(draftTasks) > 0 {
				t := draftTasks[0]
				wildfirePhase = agent.WildfirePhaseRefine
				taskTitle = t.Title
				taskNumber = int32(t.TaskNumber)
				taskSystemPrompt = prompts.ComposeWildfireRefineSystemPrompt(
					proj, t.TaskNumber, t.Title, t.Prompt, t.AcceptanceCriteria,
				)
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
			ProjectPath:      entry.Path,
			ProjectColor:     proj.Color,
			Mode:             mode,
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
	} else if mode == agent.ModeGenerateDefinition {
		// Generate definition mode: run at project root, no worktree
		taskSystemPrompt = prompts.ComposeGenerateDefinitionSystemPrompt(proj)
		taskPrompt = prompts.ComposeGenerateDefinitionUserPrompt()

		running, err := s.manager.StartAgent(agent.StartOptions{
			ProjectID:        req.ProjectId,
			ProjectName:      proj.Name,
			ProjectPath:      entry.Path,
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

		// Watch project for signal file
		if s.watcher != nil {
			_ = s.watcher.WatchProject(req.ProjectId, entry.Path)
		}

		return &pb.AgentStatus{
			ProjectId:   running.ProjectID,
			ProjectName: running.ProjectName,
			Mode:        string(running.Mode),
			IsRunning:   true,
		}, nil
	} else if mode == agent.ModeGenerateTasks {
		// Generate tasks mode: run at project root, no worktree
		taskSystemPrompt = prompts.ComposeGenerateTasksSystemPrompt(proj)
		taskPrompt = prompts.ComposeGenerateTasksUserPrompt()

		running, err := s.manager.StartAgent(agent.StartOptions{
			ProjectID:        req.ProjectId,
			ProjectName:      proj.Name,
			ProjectPath:      entry.Path,
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

		// Watch project for signal file
		if s.watcher != nil {
			_ = s.watcher.WatchProject(req.ProjectId, entry.Path)
		}

		return &pb.AgentStatus{
			ProjectId:   running.ProjectID,
			ProjectName: running.ProjectName,
			Mode:        string(running.Mode),
			IsRunning:   true,
		}, nil
	} else {
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

func (s *agentService) StopAgent(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	if err := s.manager.StopAgentByUser(req.ProjectId); err != nil {
		return nil, err
	}
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

	// Include current issue if any
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
			// Send issue update (nil means cleared, send empty issue)
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

// --- LogService ---

type logService struct {
	pb.UnimplementedLogServiceServer
}

func (s *logService) ListLogs(_ context.Context, req *pb.ListLogsRequest) (*pb.LogList, error) {
	logs, err := config.ListLogs(req.ProjectId)
	if err != nil {
		return nil, fmt.Errorf("failed to list logs: %w", err)
	}

	list := &pb.LogList{Logs: make([]*pb.LogEntry, 0, len(logs))}
	for _, l := range logs {
		list.Logs = append(list.Logs, &pb.LogEntry{
			LogId:         l.LogID,
			ProjectId:     l.ProjectID,
			TaskNumber:    int32(l.TaskNumber),
			SessionNumber: int32(l.SessionNumber),
			Agent:         l.Agent,
			Mode:          l.Mode,
			StartedAt:     l.StartedAt,
			EndedAt:       l.EndedAt,
			Status:        l.Status,
		})
	}
	return list, nil
}

func (s *logService) GetLog(_ context.Context, req *pb.GetLogRequest) (*pb.LogContent, error) {
	entry, content, err := config.ReadLog(req.ProjectId, req.LogId)
	if err != nil {
		return nil, fmt.Errorf("failed to read log: %w", err)
	}

	return &pb.LogContent{
		Entry: &pb.LogEntry{
			LogId:         entry.LogID,
			ProjectId:     entry.ProjectID,
			TaskNumber:    int32(entry.TaskNumber),
			SessionNumber: int32(entry.SessionNumber),
			Agent:         entry.Agent,
			Mode:          entry.Mode,
			StartedAt:     entry.StartedAt,
			EndedAt:       entry.EndedAt,
			Status:        entry.Status,
		},
		Content: content,
	}, nil
}

// --- SettingsService ---

type settingsService struct {
	pb.UnimplementedSettingsServiceServer
}

func (s *settingsService) GetSettings(_ context.Context, _ *emptypb.Empty) (*pb.Settings, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}
	return modelToProtoSettings(settings), nil
}

func (s *settingsService) UpdateSettings(_ context.Context, req *pb.UpdateSettingsRequest) (*pb.Settings, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}

	if req.Defaults != nil {
		settings.Defaults.AutoMerge = req.Defaults.AutoMerge
		settings.Defaults.AutoDeleteBranch = req.Defaults.AutoDeleteBranch
		settings.Defaults.AutoStartTasks = req.Defaults.AutoStartTasks
		if req.Defaults.DefaultBranch != "" {
			settings.Defaults.DefaultBranch = req.Defaults.DefaultBranch
		}
		if req.Defaults.DefaultSandbox != "" {
			settings.Defaults.DefaultSandbox = req.Defaults.DefaultSandbox
		}
		if req.Defaults.DefaultAgent != "" {
			settings.Defaults.DefaultAgent = req.Defaults.DefaultAgent
		}
	}

	if req.Updates != nil {
		settings.Updates.CheckOnStartup = req.Updates.CheckOnStartup
		if req.Updates.CheckFrequency != "" {
			settings.Updates.CheckFrequency = req.Updates.CheckFrequency
		}
		settings.Updates.AutoDownload = req.Updates.AutoDownload
	}

	if req.Appearance != nil {
		if req.Appearance.Theme != "" {
			settings.Appearance.Theme = req.Appearance.Theme
		}
	}

	// Merge agent configs
	for name, agentCfg := range req.Agents {
		if settings.Agents == nil {
			settings.Agents = make(map[string]*models.AgentConfig)
		}
		settings.Agents[name] = &models.AgentConfig{Path: agentCfg.Path}
	}

	if err := config.SaveSettings(settings); err != nil {
		return nil, fmt.Errorf("failed to save settings: %w", err)
	}
	return modelToProtoSettings(settings), nil
}

// --- BranchService ---

type branchService struct {
	pb.UnimplementedBranchServiceServer
}

func (s *branchService) ListBranches(_ context.Context, req *pb.ProjectId) (*pb.BranchList, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	branches, err := listGitBranches(entry.Path, req.ProjectId)
	if err != nil {
		return nil, err
	}
	return &pb.BranchList{Branches: branches}, nil
}

func (s *branchService) GetBranch(_ context.Context, req *pb.BranchId) (*pb.Branch, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	branches, err := listGitBranches(entry.Path, req.ProjectId)
	if err != nil {
		return nil, err
	}
	for _, b := range branches {
		if b.Name == req.BranchName {
			return b, nil
		}
	}
	return nil, fmt.Errorf("branch not found: %s", req.BranchName)
}

func (s *branchService) MergeBranch(_ context.Context, req *pb.MergeBranchRequest) (*pb.Branch, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	proj, err := config.LoadProject(entry.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load project: %w", err)
	}

	taskNum := taskNumberFromBranch(req.BranchName)
	merged, err := agent.MergeWorktree(entry.Path, taskNum, proj.DefaultBranch)
	if err != nil {
		return nil, fmt.Errorf("merge failed: %w", err)
	}

	if req.DeleteAfterMerge && merged {
		_ = agent.RemoveWorktree(entry.Path, taskNum, true)
	}

	status := "unmerged"
	if merged {
		status = "merged"
	}
	return &pb.Branch{
		Name:      req.BranchName,
		ProjectId: req.ProjectId,
		TaskNumber: int32(taskNum),
		Status:    status,
	}, nil
}

func (s *branchService) DeleteBranch(_ context.Context, req *pb.BranchId) (*emptypb.Empty, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	taskNum := taskNumberFromBranch(req.BranchName)
	if err := agent.RemoveWorktree(entry.Path, taskNum, false); err != nil {
		return nil, fmt.Errorf("failed to delete branch: %w", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *branchService) PruneBranches(_ context.Context, req *pb.ProjectId) (*pb.BranchList, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	// Prune git worktrees first
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = entry.Path
	_ = pruneCmd.Run()

	// Return remaining branches
	branches, err := listGitBranches(entry.Path, req.ProjectId)
	if err != nil {
		return nil, err
	}
	return &pb.BranchList{Branches: branches}, nil
}

func (s *branchService) BulkMerge(_ context.Context, req *pb.BulkBranchRequest) (*pb.BranchList, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	proj, err := config.LoadProject(entry.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load project: %w", err)
	}

	var results []*pb.Branch
	for _, branchName := range req.BranchNames {
		taskNum := taskNumberFromBranch(branchName)
		merged, err := agent.MergeWorktree(entry.Path, taskNum, proj.DefaultBranch)
		status := "unmerged"
		if err == nil && merged {
			status = "merged"
		}
		results = append(results, &pb.Branch{
			Name:       branchName,
			ProjectId:  req.ProjectId,
			TaskNumber: int32(taskNum),
			Status:     status,
		})
	}
	return &pb.BranchList{Branches: results}, nil
}

func (s *branchService) BulkDelete(_ context.Context, req *pb.BulkBranchRequest) (*emptypb.Empty, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	for _, branchName := range req.BranchNames {
		taskNum := taskNumberFromBranch(branchName)
		_ = agent.RemoveWorktree(entry.Path, taskNum, false)
	}
	return &emptypb.Empty{}, nil
}

// listGitBranches lists all watchfire/* branches for a project.
func listGitBranches(projectPath, projectID string) ([]*pb.Branch, error) {
	cmd := exec.Command("git", "branch", "--list", "watchfire/*", "--format=%(refname:short)")
	cmd.Dir = projectPath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	// Get the default branch for merge status checks
	proj, _ := config.LoadProject(projectPath)
	defaultBranch := "main"
	if proj != nil && proj.DefaultBranch != "" {
		defaultBranch = proj.DefaultBranch
	}

	var branches []*pb.Branch
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		taskNum := taskNumberFromBranch(line)
		status := branchMergeStatus(projectPath, line, defaultBranch)
		worktreePath := ""
		padded := fmt.Sprintf("%04d", taskNum)
		wtPath := filepath.Join(projectPath, ".watchfire", "worktrees", padded)
		if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
			worktreePath = wtPath
		}

		branches = append(branches, &pb.Branch{
			Name:         line,
			ProjectId:    projectID,
			TaskNumber:   int32(taskNum),
			Status:       status,
			WorktreePath: worktreePath,
		})
	}
	return branches, nil
}

// taskNumberFromBranch extracts the task number from a "watchfire/NNNN" branch name.
func taskNumberFromBranch(branchName string) int {
	parts := strings.SplitN(branchName, "/", 2)
	if len(parts) != 2 {
		return 0
	}
	var num int
	fmt.Sscanf(parts[1], "%d", &num)
	return num
}

// branchMergeStatus checks if a branch has been merged into the target branch.
func branchMergeStatus(projectPath, branchName, targetBranch string) string {
	// Check if branch is merged
	cmd := exec.Command("git", "branch", "--merged", targetBranch, "--list", branchName)
	cmd.Dir = projectPath
	output, err := cmd.Output()
	if err != nil {
		return "unmerged"
	}
	if strings.TrimSpace(string(output)) != "" {
		return "merged"
	}

	// Check if worktree exists — if not, it's orphaned
	taskNum := taskNumberFromBranch(branchName)
	padded := fmt.Sprintf("%04d", taskNum)
	wtPath := filepath.Join(projectPath, ".watchfire", "worktrees", padded)
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		// No worktree and not merged — check if there's a matching task
		if _, taskErr := config.LoadTask(projectPath, taskNum); taskErr != nil {
			return "orphaned"
		}
	}
	return "unmerged"
}

// ============================================================================
// Conversion Functions
// ============================================================================

func modelToProtoProject(pwe project.ProjectWithEntry) *pb.Project {
	p := pwe.Project
	return &pb.Project{
		ProjectId:        p.ProjectID,
		Name:             p.Name,
		Path:             pwe.Path,
		Status:           p.Status,
		Color:            p.Color,
		DefaultBranch:    p.DefaultBranch,
		DefaultAgent:     p.DefaultAgent,
		Sandbox:          p.Sandbox,
		AutoMerge:        p.AutoMerge,
		AutoDeleteBranch: p.AutoDeleteBranch,
		AutoStartTasks:   p.AutoStartTasks,
		Definition:       p.Definition,
		CreatedAt:        timestamppb.New(p.CreatedAt),
		UpdatedAt:        timestamppb.New(p.UpdatedAt),
		NextTaskNumber:   int32(p.NextTaskNumber),
		Position:         int32(pwe.Position),
	}
}

func modelToProtoTask(t *models.Task, projectID string) *pb.Task {
	protoTask := &pb.Task{
		TaskId:             t.TaskID,
		TaskNumber:         int32(t.TaskNumber),
		ProjectId:          projectID,
		Title:              t.Title,
		Prompt:             t.Prompt,
		AcceptanceCriteria: t.AcceptanceCriteria,
		Status:             string(t.Status),
		Position:           int32(t.Position),
		AgentSessions:      int32(t.AgentSessions),
		CreatedAt:          timestamppb.New(t.CreatedAt),
		UpdatedAt:          timestamppb.New(t.UpdatedAt),
	}

	if t.Success != nil {
		protoTask.Success = t.Success
	}
	if t.FailureReason != "" {
		protoTask.FailureReason = &t.FailureReason
	}
	if t.StartedAt != nil {
		protoTask.StartedAt = timestamppb.New(*t.StartedAt)
	}
	if t.CompletedAt != nil {
		protoTask.CompletedAt = timestamppb.New(*t.CompletedAt)
	}
	if t.DeletedAt != nil {
		protoTask.DeletedAt = timestamppb.New(*t.DeletedAt)
	}

	return protoTask
}

func modelToProtoSettings(s *models.Settings) *pb.Settings {
	agents := make(map[string]*pb.AgentConfig, len(s.Agents))
	for name, cfg := range s.Agents {
		agents[name] = &pb.AgentConfig{Path: cfg.Path}
	}
	return &pb.Settings{
		Version: int32(s.Version),
		Agents:  agents,
		Defaults: &pb.DefaultsConfig{
			AutoMerge:        s.Defaults.AutoMerge,
			AutoDeleteBranch: s.Defaults.AutoDeleteBranch,
			AutoStartTasks:   s.Defaults.AutoStartTasks,
			DefaultBranch:    s.Defaults.DefaultBranch,
			DefaultSandbox:   s.Defaults.DefaultSandbox,
			DefaultAgent:     s.Defaults.DefaultAgent,
		},
		Updates: &pb.UpdatesConfig{
			CheckOnStartup: s.Updates.CheckOnStartup,
			CheckFrequency: s.Updates.CheckFrequency,
			AutoDownload:   s.Updates.AutoDownload,
		},
		Appearance: &pb.AppearanceConfig{
			Theme: s.Appearance.Theme,
		},
	}
}
