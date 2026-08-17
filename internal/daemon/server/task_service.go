package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
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

// ListMalformedTasks returns task files that exist on disk but failed to load
// (e.g. an agent hand-authored a `title:` with an unquoted `: `). These used
// to be silently dropped with only a daemon log line; surfacing them lets the
// GUI/TUI show a non-silent "N task file(s) failed to load" affordance.
func (s *taskService) ListMalformedTasks(_ context.Context, req *pb.ListMalformedTasksRequest) (*pb.MalformedTaskList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	malformed, err := s.manager.ListMalformedTasks(projectPath)
	if err != nil {
		return nil, err
	}

	list := &pb.MalformedTaskList{Tasks: make([]*pb.MalformedTask, 0, len(malformed))}
	for _, mf := range malformed {
		list.Tasks = append(list.Tasks, &pb.MalformedTask{
			TaskNumber: int32(mf.TaskNumber),
			FileName:   mf.FileName,
			Error:      mf.Error,
		})
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

// CreateTasksBatch parses a quick-add text blob (each top-level bullet
// becomes one task — see task.ParseQuickAdd) and creates every task through
// the validated create path with consecutive numbers and positions appended
// in input order. v10 Torch: the shared backend for the GUI quick-add modal,
// the TUI quick-add overlay, and `watchfire task quick`.
func (s *taskService) CreateTasksBatch(_ context.Context, req *pb.CreateTasksBatchRequest) (*pb.TaskList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	items := task.ParseQuickAdd(req.Text)
	if len(items) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no tasks found in input")
	}

	tasks, err := s.manager.CreateTasksBatch(projectPath, items, req.Status)
	if err != nil {
		if strings.Contains(err.Error(), "invalid status") {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, err
	}

	list := &pb.TaskList{Tasks: make([]*pb.Task, 0, len(tasks))}
	for _, t := range tasks {
		list.Tasks = append(list.Tasks, modelToProtoTask(t, req.ProjectId))
	}
	return list, nil
}

// ArchiveRetrofitTasks archives (soft-deletes) the done tasks folded into
// the definition by retrofit-definition runs — every done, non-deleted task
// at or below the project's retrofit watermark. dry_run returns the
// candidates without touching anything, so surfaces can render the
// "Archive N folded tasks" confirmation. The actual archive is reversible
// from Trash and keeps counting in insights (RetrofitArchived flag).
// Confirmation is a surface concern: this RPC trusts that the caller asked.
func (s *taskService) ArchiveRetrofitTasks(_ context.Context, req *pb.ArchiveRetrofitRequest) (*pb.TaskList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	var tasks []*models.Task
	if req.DryRun {
		tasks, err = s.manager.RetrofitArchiveCandidates(projectPath)
	} else {
		tasks, err = s.manager.ArchiveRetrofitTasks(projectPath)
	}
	if err != nil {
		return nil, err
	}

	list := &pb.TaskList{Tasks: make([]*pb.Task, 0, len(tasks))}
	for _, t := range tasks {
		list.Tasks = append(list.Tasks, modelToProtoTask(t, req.ProjectId))
	}
	return list, nil
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

// ReorderTasks rewrites task positions densely (1..N) in the order given by
// req.TaskNumbers. Mirrors ProjectService.ReorderProjects on the task plane —
// the v7 drag-to-reorder UI in #0098/#0099 depends on this RPC.
func (s *taskService) ReorderTasks(_ context.Context, req *pb.ReorderTasksRequest) (*pb.TaskList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	nums := make([]int, 0, len(req.TaskNumbers))
	for _, n := range req.TaskNumbers {
		nums = append(nums, int(n))
	}

	tasks, err := s.manager.ReorderTasks(projectPath, nums)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "task not found") || strings.Contains(msg, "duplicate task in reorder request") {
			return nil, status.Error(codes.InvalidArgument, msg)
		}
		return nil, status.Error(codes.Internal, msg)
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
