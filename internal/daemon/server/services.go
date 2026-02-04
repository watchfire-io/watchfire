package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

// ============================================================================
// gRPC Service Definitions (inline since proto generation not yet available)
// ============================================================================

// ProjectServiceServer is the server interface for ProjectService.
type ProjectServiceServer interface {
	ListProjects(context.Context, *emptypb.Empty) (*ProjectList, error)
	GetProject(context.Context, *ProjectID) (*Project, error)
	CreateProject(context.Context, *CreateProjectRequest) (*Project, error)
	UpdateProject(context.Context, *UpdateProjectRequest) (*Project, error)
	DeleteProject(context.Context, *ProjectID) (*emptypb.Empty, error)
}

// TaskServiceServer is the server interface for TaskService.
type TaskServiceServer interface {
	ListTasks(context.Context, *ListTasksRequest) (*TaskList, error)
	GetTask(context.Context, *TaskID) (*Task, error)
	CreateTask(context.Context, *CreateTaskRequest) (*Task, error)
	UpdateTask(context.Context, *UpdateTaskRequest) (*Task, error)
	DeleteTask(context.Context, *TaskID) (*Task, error)
	RestoreTask(context.Context, *TaskID) (*Task, error)
}

// DaemonServiceServer is the server interface for DaemonService.
type DaemonServiceServer interface {
	GetStatus(context.Context, *emptypb.Empty) (*DaemonStatus, error)
	Shutdown(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
}

// AgentServiceServer is the server interface for AgentService.
type AgentServiceServer interface {
	StartAgent(context.Context, *StartAgentRequest) (*AgentStatus, error)
	StopAgent(context.Context, *ProjectID) (*emptypb.Empty, error)
	GetAgentStatus(context.Context, *ProjectID) (*AgentStatus, error)
}

// ============================================================================
// Message Types
// ============================================================================

// RequestMeta contains metadata about the client making a request.
type RequestMeta struct {
	Origin   string
	ClientID string
	Version  string
}

// Project represents a project in the gRPC API.
type Project struct {
	ProjectID        string
	Name             string
	Path             string
	Status           string
	Color            string
	DefaultBranch    string
	DefaultAgent     string
	Sandbox          string
	AutoMerge        bool
	AutoDeleteBranch bool
	AutoStartTasks   bool
	Definition       string
	CreatedAt        *timestamppb.Timestamp
	UpdatedAt        *timestamppb.Timestamp
	NextTaskNumber   int32
}

// ProjectID identifies a project by its unique ID.
type ProjectID struct {
	Meta      *RequestMeta
	ProjectID string
}

// ProjectList holds a list of projects.
type ProjectList struct {
	Projects []*Project
}

// CreateProjectRequest contains the fields for creating a new project.
type CreateProjectRequest struct {
	Meta             *RequestMeta
	Path             string
	Name             string
	Definition       string
	DefaultBranch    string
	AutoMerge        bool
	AutoDeleteBranch bool
	AutoStartTasks   bool
}

// UpdateProjectRequest contains the fields for updating an existing project.
type UpdateProjectRequest struct {
	Meta             *RequestMeta
	ProjectID        string
	Name             *string
	Color            *string
	DefaultBranch    *string
	DefaultAgent     *string
	AutoMerge        *bool
	AutoDeleteBranch *bool
	AutoStartTasks   *bool
	Definition       *string
}

// Task represents a task in the gRPC API.
type Task struct {
	TaskID             string
	TaskNumber         int32
	ProjectID          string
	Title              string
	Prompt             string
	AcceptanceCriteria string
	Status             string
	Success            *bool
	FailureReason      *string
	Position           int32
	AgentSessions      int32
	CreatedAt          *timestamppb.Timestamp
	StartedAt          *timestamppb.Timestamp
	CompletedAt        *timestamppb.Timestamp
	UpdatedAt          *timestamppb.Timestamp
	DeletedAt          *timestamppb.Timestamp
}

// TaskID identifies a task by project ID and task number.
type TaskID struct {
	Meta       *RequestMeta
	ProjectID  string
	TaskNumber int32
}

// TaskList holds a list of tasks.
type TaskList struct {
	Tasks []*Task
}

// ListTasksRequest contains the parameters for listing tasks.
type ListTasksRequest struct {
	Meta           *RequestMeta
	ProjectID      string
	Status         *string
	IncludeDeleted bool
}

// CreateTaskRequest contains the fields for creating a new task.
type CreateTaskRequest struct {
	Meta               *RequestMeta
	ProjectID          string
	Title              string
	Prompt             string
	AcceptanceCriteria *string
	Status             string
	Position           *int32
}

// UpdateTaskRequest contains the fields for updating an existing task.
type UpdateTaskRequest struct {
	Meta               *RequestMeta
	ProjectID          string
	TaskNumber         int32
	Title              *string
	Prompt             *string
	AcceptanceCriteria *string
	Status             *string
	Success            *bool
	FailureReason      *string
	Position           *int32
}

// DaemonStatus represents the current status of the daemon.
type DaemonStatus struct {
	Host           string
	Port           int32
	Pid            int32
	StartedAt      *timestamppb.Timestamp
	ActiveAgents   int32
	ActiveProjects []string
}

// AgentStatus represents the current status of an agent.
type AgentStatus struct {
	ProjectID   string
	ProjectName string
	Mode        string // "chat" | "task" | "wildfire"
	TaskNumber  int32
	TaskTitle   string
	IsRunning   bool
}

// StartAgentRequest contains the parameters for starting an agent.
type StartAgentRequest struct {
	Meta       *RequestMeta
	ProjectID  string
	Mode       string // "chat" | "task" | "wildfire"
	TaskNumber int32
}

// ScreenBuffer represents the current screen content of an agent session.
type ScreenBuffer struct {
	ProjectID string
	Lines     []string
	CursorRow int32
	CursorCol int32
	Rows      int32
	Cols      int32
}

// SubscribeScreenRequest contains the parameters for subscribing to screen updates.
type SubscribeScreenRequest struct {
	Meta      *RequestMeta
	ProjectID string
}

// ScrollbackRequest contains the parameters for fetching scrollback history.
type ScrollbackRequest struct {
	Meta      *RequestMeta
	ProjectID string
	Offset    int32
	Limit     int32
}

// ScrollbackLines holds lines from the scrollback buffer.
type ScrollbackLines struct {
	Lines      []string
	TotalLines int32
}

// SendInputRequest contains data to send as input to an agent session.
type SendInputRequest struct {
	Meta      *RequestMeta
	ProjectID string
	Data      []byte
}

// ResizeRequest contains the new dimensions for resizing an agent terminal.
type ResizeRequest struct {
	Meta      *RequestMeta
	ProjectID string
	Rows      int32
	Cols      int32
}

// ============================================================================
// Service Registration Functions
// ============================================================================

// RegisterProjectServiceServer registers the ProjectServiceServer with the gRPC server.
func RegisterProjectServiceServer(s *grpc.Server, srv ProjectServiceServer) {
	// In real implementation, this would use generated code from protoc
	// For now, we'll implement a simple registration
}

// RegisterTaskServiceServer registers the TaskServiceServer with the gRPC server.
func RegisterTaskServiceServer(s *grpc.Server, srv TaskServiceServer) {
	// In real implementation, this would use generated code from protoc
}

// RegisterDaemonServiceServer registers the DaemonServiceServer with the gRPC server.
func RegisterDaemonServiceServer(s *grpc.Server, srv DaemonServiceServer) {
	// In real implementation, this would use generated code from protoc
}

// RegisterAgentServiceServer registers the AgentServiceServer with the gRPC server.
func RegisterAgentServiceServer(s *grpc.Server, srv AgentServiceServer) {
	// In real implementation, this would use generated code from protoc
}

// ============================================================================
// Service Implementations
// ============================================================================

type projectService struct {
	manager *project.Manager
}

func (s *projectService) ListProjects(ctx context.Context, _ *emptypb.Empty) (*ProjectList, error) {
	projects, err := s.manager.ListProjects()
	if err != nil {
		return nil, err
	}

	list := &ProjectList{Projects: make([]*Project, 0, len(projects))}
	for _, p := range projects {
		list.Projects = append(list.Projects, modelToProtoProject(p))
	}
	return list, nil
}

func (s *projectService) GetProject(ctx context.Context, req *ProjectID) (*Project, error) {
	p, err := s.manager.GetProject(req.ProjectID)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(p), nil
}

func (s *projectService) CreateProject(ctx context.Context, req *CreateProjectRequest) (*Project, error) {
	p, err := s.manager.CreateProject(project.CreateOptions{
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
	return modelToProtoProject(p), nil
}

func (s *projectService) UpdateProject(ctx context.Context, req *UpdateProjectRequest) (*Project, error) {
	opts := project.UpdateOptions{ProjectID: req.ProjectID}
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

	p, err := s.manager.UpdateProject(opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(p), nil
}

func (s *projectService) DeleteProject(ctx context.Context, req *ProjectID) (*emptypb.Empty, error) {
	err := s.manager.DeleteProject(req.ProjectID)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

type taskService struct {
	manager *task.Manager
}

func (s *taskService) ListTasks(ctx context.Context, req *ListTasksRequest) (*TaskList, error) {
	// Get project path from ID
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectID)
	}

	tasks, err := s.manager.ListTasks(entry.Path, task.ListOptions{
		Status:         req.Status,
		IncludeDeleted: req.IncludeDeleted,
	})
	if err != nil {
		return nil, err
	}

	list := &TaskList{Tasks: make([]*Task, 0, len(tasks))}
	for _, t := range tasks {
		list.Tasks = append(list.Tasks, modelToProtoTask(t, req.ProjectID))
	}
	return list, nil
}

func (s *taskService) GetTask(ctx context.Context, req *TaskID) (*Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectID)
	}

	t, err := s.manager.GetTask(entry.Path, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectID), nil
}

func (s *taskService) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectID)
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
	return modelToProtoTask(t, req.ProjectID), nil
}

func (s *taskService) UpdateTask(ctx context.Context, req *UpdateTaskRequest) (*Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectID)
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
	return modelToProtoTask(t, req.ProjectID), nil
}

func (s *taskService) DeleteTask(ctx context.Context, req *TaskID) (*Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectID)
	}

	t, err := s.manager.DeleteTask(entry.Path, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectID), nil
}

func (s *taskService) RestoreTask(ctx context.Context, req *TaskID) (*Task, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectID)
	}

	t, err := s.manager.RestoreTask(entry.Path, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectID), nil
}

type daemonService struct {
	server *Server
}

func (s *daemonService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*DaemonStatus, error) {
	info, err := config.LoadDaemonInfo()
	if err != nil {
		return nil, err
	}

	agents := s.server.agentManager.ListAgents()
	activeProjects := make([]string, 0, len(agents))
	for _, a := range agents {
		activeProjects = append(activeProjects, a.ProjectID)
	}

	return &DaemonStatus{
		Host:           info.Host,
		Port:           int32(info.Port),
		Pid:            int32(info.PID),
		StartedAt:      timestamppb.New(info.StartedAt),
		ActiveAgents:   int32(len(agents)),
		ActiveProjects: activeProjects,
	}, nil
}

func (s *daemonService) Shutdown(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	// Signal shutdown - this will be caught by the main loop
	go func() {
		time.Sleep(100 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
	}()
	return &emptypb.Empty{}, nil
}

type agentService struct {
	manager *agent.Manager
}

func (s *agentService) StartAgent(ctx context.Context, req *StartAgentRequest) (*AgentStatus, error) {
	return nil, fmt.Errorf("agent spawning not yet implemented")
}

func (s *agentService) StopAgent(ctx context.Context, req *ProjectID) (*emptypb.Empty, error) {
	return nil, fmt.Errorf("agent management not yet implemented")
}

func (s *agentService) GetAgentStatus(ctx context.Context, req *ProjectID) (*AgentStatus, error) {
	return &AgentStatus{
		ProjectID: req.ProjectID,
		IsRunning: false,
	}, nil
}

// ============================================================================
// Conversion Functions
// ============================================================================

func modelToProtoProject(p *models.Project) *Project {
	return &Project{
		ProjectID:        p.ProjectID,
		Name:             p.Name,
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
	}
}

func modelToProtoTask(t *models.Task, projectID string) *Task {
	protoTask := &Task{
		TaskID:             t.TaskID,
		TaskNumber:         int32(t.TaskNumber),
		ProjectID:          projectID,
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
