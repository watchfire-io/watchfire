package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/daemon/task"
	pb "github.com/watchfire-io/watchfire/proto"
)

type taskService struct {
	pb.UnimplementedTaskServiceServer
	manager *task.Manager
}

func (s *taskService) ListTasks(_ context.Context, req *pb.ListTasksRequest) (*pb.TaskList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	opts := task.ListOptions{
		IncludeDeleted: req.IncludeDeleted,
	}
	if req.Status != nil {
		opts.Status = req.Status
	}

	tasks, err := s.manager.ListTasks(projectPath, opts)
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
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	t, err := s.manager.GetTask(projectPath, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) CreateTask(_ context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	opts := task.CreateOptions{
		Title:  req.Title,
		Prompt: req.Prompt,
		Status: req.Status,
	}
	if req.AcceptanceCriteria != nil {
		opts.AcceptanceCriteria = *req.AcceptanceCriteria
	}
	if req.Agent != nil {
		opts.Agent = *req.Agent
	}
	if req.Position != nil {
		pos := int(*req.Position)
		opts.Position = &pos
	}

	t, err := s.manager.CreateTask(projectPath, opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) UpdateTask(_ context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
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
	if req.Agent != nil {
		opts.Agent = req.Agent
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

	t, err := s.manager.UpdateTask(projectPath, opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) DeleteTask(_ context.Context, req *pb.TaskId) (*pb.Task, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	t, err := s.manager.DeleteTask(projectPath, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) RestoreTask(_ context.Context, req *pb.TaskId) (*pb.Task, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	t, err := s.manager.RestoreTask(projectPath, int(req.TaskNumber))
	if err != nil {
		return nil, err
	}
	return modelToProtoTask(t, req.ProjectId), nil
}

func (s *taskService) PermanentDeleteTask(_ context.Context, req *pb.TaskId) (*emptypb.Empty, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	check := func(taskNumber int) (bool, error) {
		return branchSafeToDelete(projectPath, taskNumber), nil
	}
	if err := s.manager.PermanentDelete(projectPath, int(req.TaskNumber), check); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// branchSafeToDelete reports whether the worktree branch for a task can be
// safely discarded as part of a permanent delete. True when the branch is
// already merged, no longer exists, or never existed; false when it exists
// and has unmerged commits relative to the current branch.
func branchSafeToDelete(projectPath string, taskNumber int) bool {
	branchName := fmt.Sprintf("watchfire/%04d", taskNumber)
	listCmd := exec.Command("git", "branch", "--list", branchName)
	listCmd.Dir = projectPath
	out, err := listCmd.Output()
	if err != nil {
		// Git unavailable / not a repo — don't block destructive cleanup.
		return true
	}
	if strings.TrimSpace(string(out)) == "" {
		return true
	}

	target := "main"
	revCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	revCmd.Dir = projectPath
	if revOut, revErr := revCmd.Output(); revErr == nil {
		t := strings.TrimSpace(string(revOut))
		if t != "" && t != "HEAD" {
			target = t
		}
	}

	mergedCmd := exec.Command("git", "branch", "--merged", target, "--list", branchName)
	mergedCmd.Dir = projectPath
	mergedOut, err := mergedCmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(mergedOut)) != ""
}

func (s *taskService) BulkUpdateStatus(_ context.Context, req *pb.BulkUpdateStatusRequest) (*pb.TaskList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	nums := make([]int, 0, len(req.TaskNumbers))
	for _, n := range req.TaskNumbers {
		nums = append(nums, int(n))
	}

	tasks, err := s.manager.BulkUpdateStatus(projectPath, nums, req.NewStatus)
	if err != nil {
		return nil, err
	}

	list := &pb.TaskList{Tasks: make([]*pb.Task, 0, len(tasks))}
	for _, t := range tasks {
		list.Tasks = append(list.Tasks, modelToProtoTask(t, req.ProjectId))
	}
	return list, nil
}

func (s *taskService) EmptyTrash(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	if err := s.manager.EmptyTrash(projectPath); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
