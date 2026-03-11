package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	pb "github.com/watchfire-io/watchfire/proto"
)

type branchService struct {
	pb.UnimplementedBranchServiceServer
}

func (s *branchService) ListBranches(_ context.Context, req *pb.ProjectId) (*pb.BranchList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	branches, err := listGitBranches(projectPath, req.ProjectId)
	if err != nil {
		return nil, err
	}
	return &pb.BranchList{Branches: branches}, nil
}

func (s *branchService) GetBranch(_ context.Context, req *pb.BranchId) (*pb.Branch, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	branches, err := listGitBranches(projectPath, req.ProjectId)
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
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	taskNum := taskNumberFromBranch(req.BranchName)
	merged, err := agent.MergeWorktree(projectPath, taskNum)
	if err != nil {
		return nil, fmt.Errorf("merge failed: %w", err)
	}

	if req.DeleteAfterMerge && merged {
		_ = agent.RemoveWorktree(projectPath, taskNum, true)
	}

	status := "unmerged"
	if merged {
		status = "merged"
	}
	return &pb.Branch{
		Name:       req.BranchName,
		ProjectId:  req.ProjectId,
		TaskNumber: int32(taskNum),
		Status:     status,
	}, nil
}

func (s *branchService) DeleteBranch(_ context.Context, req *pb.BranchId) (*emptypb.Empty, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	taskNum := taskNumberFromBranch(req.BranchName)
	if err := agent.RemoveWorktree(projectPath, taskNum, false); err != nil {
		return nil, fmt.Errorf("failed to delete branch: %w", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *branchService) PruneBranches(_ context.Context, req *pb.ProjectId) (*pb.BranchList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	// Prune git worktrees first
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = projectPath
	_ = pruneCmd.Run()

	// Return remaining branches
	branches, err := listGitBranches(projectPath, req.ProjectId)
	if err != nil {
		return nil, err
	}
	return &pb.BranchList{Branches: branches}, nil
}

func (s *branchService) BulkMerge(_ context.Context, req *pb.BulkBranchRequest) (*pb.BranchList, error) {
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	var results []*pb.Branch
	for _, branchName := range req.BranchNames {
		taskNum := taskNumberFromBranch(branchName)
		merged, err := agent.MergeWorktree(projectPath, taskNum)
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
	projectPath, err := getProjectPath(req.ProjectId)
	if err != nil {
		return nil, err
	}

	for _, branchName := range req.BranchNames {
		taskNum := taskNumberFromBranch(branchName)
		_ = agent.RemoveWorktree(projectPath, taskNum, false)
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

	// Detect current branch for merge status checks
	currentBranch := "main"
	revCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	revCmd.Dir = projectPath
	if revOut, revErr := revCmd.Output(); revErr == nil {
		currentBranch = strings.TrimSpace(string(revOut))
	}

	var branches []*pb.Branch
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		taskNum := taskNumberFromBranch(line)
		status := branchMergeStatus(projectPath, line, currentBranch)
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
	cmd := exec.Command("git", "branch", "--merged", targetBranch, "--list", branchName)
	cmd.Dir = projectPath
	output, err := cmd.Output()
	if err != nil {
		return "unmerged"
	}
	if strings.TrimSpace(string(output)) != "" {
		return "merged"
	}

	taskNum := taskNumberFromBranch(branchName)
	padded := fmt.Sprintf("%04d", taskNum)
	wtPath := filepath.Join(projectPath, ".watchfire", "worktrees", padded)
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		if _, taskErr := config.LoadTask(projectPath, taskNum); taskErr != nil {
			return "orphaned"
		}
	}
	return "unmerged"
}
