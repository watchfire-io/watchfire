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
	GetProject(context.Context, *ProjectId) (*Project, error)
	CreateProject(context.Context, *CreateProjectRequest) (*Project, error)
	UpdateProject(context.Context, *UpdateProjectRequest) (*Project, error)
	DeleteProject(context.Context, *ProjectId) (*emptypb.Empty, error)
}

// TaskServiceServer is the server interface for TaskService.
type TaskServiceServer interface {
	ListTasks(context.Context, *ListTasksRequest) (*TaskList, error)
	GetTask(context.Context, *TaskId) (*Task, error)
	CreateTask(context.Context, *CreateTaskRequest) (*Task, error)
	UpdateTask(context.Context, *UpdateTaskRequest) (*Task, error)
	DeleteTask(context.Context, *TaskId) (*Task, error)
	RestoreTask(context.Context, *TaskId) (*Task, error)
}

// DaemonServiceServer is the server interface for DaemonService.
type DaemonServiceServer interface {
	GetStatus(context.Context, *emptypb.Empty) (*DaemonStatus, error)
	Shutdown(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
}

// ============================================================================
// Message Types
// ============================================================================

type RequestMeta struct {
	Origin   string
	ClientId string
	Version  string
}

type Project struct {
	ProjectId        string
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

type ProjectId struct {
	Meta      *RequestMeta
	ProjectId string
}

type ProjectList struct {
	Projects []*Project
}

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

type UpdateProjectRequest struct {
	Meta             *RequestMeta
	ProjectId        string
	Name             *string
	Color            *string
	DefaultBranch    *string
	DefaultAgent     *string
	AutoMerge        *bool
	AutoDeleteBranch *bool
	AutoStartTasks   *bool
	Definition       *string
}

type Task struct {
	TaskId             string
	TaskNumber         int32
	ProjectId          string
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

type TaskId struct {
	Meta       *RequestMeta
	ProjectId  string
	TaskNumber int32
}

type TaskList struct {
	Tasks []*Task
}

type ListTasksRequest struct {
	Meta           *RequestMeta
	ProjectId      string
	Status         *string
	IncludeDeleted bool
}

type CreateTaskRequest struct {
	Meta               *RequestMeta
	ProjectId          string
	Title              string
	Prompt             string
	AcceptanceCriteria *string
	Status             string
	Position           *int32
}

type UpdateTaskRequest struct {
	Meta               *RequestMeta
	ProjectId          string
	TaskNumber         int32
	Title              *string
	Prompt             *string
	AcceptanceCriteria *string
	Status             *string
	Success            *bool
	FailureReason      *string
	Position           *int32
}

type DaemonStatus struct {
	Host           string
	Port           int32
	Pid            int32
	StartedAt      *timestamppb.Timestamp
	ActiveAgents   int32
	ActiveProjects []string
}

// ============================================================================
// Service Registration Functions
// ============================================================================

func RegisterProjectServiceServer(s *grpc.Server, srv ProjectServiceServer) {
	// In real implementation, this would use generated code from protoc
	// For now, we'll implement a simple registration
}

func RegisterTaskServiceServer(s *grpc.Server, srv TaskServiceServer) {
	// In real implementation, this would use generated code from protoc
}

func RegisterDaemonServiceServer(s *grpc.Server, srv DaemonServiceServer) {
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

func (s *projectService) GetProject(ctx context.Context, req *ProjectId) (*Project, error) {
	p, err := s.manager.GetProject(req.ProjectId)
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

	p, err := s.manager.UpdateProject(opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(p), nil
}

func (s *projectService) DeleteProject(ctx context.Context, req *ProjectId) (*emptypb.Empty, error) {
	err := s.manager.DeleteProject(req.ProjectId)
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
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
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
		list.Tasks = append(list.Tasks, modelToProtoTask(t, req.ProjectId))
	}
	return list, nil
}

func (s *taskService) GetTask(ctx context.Context, req *TaskId) (*Task, error) {
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

func (s *taskService) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
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

func (s *taskService) UpdateTask(ctx context.Context, req *UpdateTaskRequest) (*Task, error) {
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

func (s *taskService) DeleteTask(ctx context.Context, req *TaskId) (*Task, error) {
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

func (s *taskService) RestoreTask(ctx context.Context, req *TaskId) (*Task, error) {
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

type daemonService struct {
	server *Server
}

func (s *daemonService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*DaemonStatus, error) {
	info, err := config.LoadDaemonInfo()
	if err != nil {
		return nil, err
	}

	return &DaemonStatus{
		Host:           info.Host,
		Port:           int32(info.Port),
		Pid:            int32(info.PID),
		StartedAt:      timestamppb.New(info.StartedAt),
		ActiveAgents:   0, // Will be implemented later
		ActiveProjects: []string{},
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

// ============================================================================
// Conversion Functions
// ============================================================================

func modelToProtoProject(p *models.Project) *Project {
	return &Project{
		ProjectId:        p.ProjectID,
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
	task := &Task{
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
		task.Success = t.Success
	}
	if t.FailureReason != "" {
		task.FailureReason = &t.FailureReason
	}
	if t.StartedAt != nil {
		task.StartedAt = timestamppb.New(*t.StartedAt)
	}
	if t.CompletedAt != nil {
		task.CompletedAt = timestamppb.New(*t.CompletedAt)
	}
	if t.DeletedAt != nil {
		task.DeletedAt = timestamppb.New(*t.DeletedAt)
	}

	return task
}
