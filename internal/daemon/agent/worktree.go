package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureWorktree creates a git worktree for the given task if it doesn't exist.
// Returns the worktree path.
// Path: <projectPath>/.watchfire/worktrees/<paddedTaskNumber>/
// Branch: watchfire/<paddedTaskNumber>
func EnsureWorktree(projectPath string, taskNumber int) (string, error) {
	padded := fmt.Sprintf("%04d", taskNumber)
	worktreePath := filepath.Join(projectPath, ".watchfire", "worktrees", padded)
	branchName := fmt.Sprintf("watchfire/%s", padded)

	// Reuse existing worktree
	if info, err := os.Stat(worktreePath); err == nil && info.IsDir() {
		return worktreePath, nil
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Join(projectPath, ".watchfire", "worktrees"), 0o755); err != nil {
		return "", fmt.Errorf("failed to create worktrees directory: %w", err)
	}

	// Prune stale worktree tracking (best-effort — handles manually deleted directories)
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = projectPath
	_ = pruneCmd.Run()

	// Try creating worktree with a new branch
	cmd := exec.Command("git", "worktree", "add", worktreePath, "-b", branchName)
	cmd.Dir = projectPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Branch may already exist — try without -b
		if strings.Contains(string(output), "already exists") {
			cmd = exec.Command("git", "worktree", "add", worktreePath, branchName)
			cmd.Dir = projectPath
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("failed to create worktree: %s: %w", string(output), err)
			}
		} else {
			return "", fmt.Errorf("failed to create worktree: %s: %w", string(output), err)
		}
	}

	return worktreePath, nil
}

// MergeWorktree merges the worktree branch into the target branch using --no-ff.
// Must be run from the project root (main worktree).
func MergeWorktree(projectPath string, taskNumber int, targetBranch string) error {
	padded := fmt.Sprintf("%04d", taskNumber)
	branchName := fmt.Sprintf("watchfire/%s", padded)

	// Checkout target branch
	checkout := exec.Command("git", "checkout", targetBranch)
	checkout.Dir = projectPath
	if output, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to checkout %s: %s: %w", targetBranch, string(output), err)
	}

	// Merge with --no-ff
	merge := exec.Command("git", "merge", "--no-ff", branchName, "-m",
		fmt.Sprintf("Merge %s", branchName))
	merge.Dir = projectPath
	if output, err := merge.CombinedOutput(); err != nil {
		return fmt.Errorf("merge failed: %s: %w", string(output), err)
	}

	return nil
}

// RemoveWorktree removes the worktree directory and deletes the branch.
func RemoveWorktree(projectPath string, taskNumber int) error {
	padded := fmt.Sprintf("%04d", taskNumber)
	worktreePath := filepath.Join(projectPath, ".watchfire", "worktrees", padded)
	branchName := fmt.Sprintf("watchfire/%s", padded)

	// Remove worktree (unregisters + removes directory)
	remove := exec.Command("git", "worktree", "remove", worktreePath)
	remove.Dir = projectPath
	if output, err := remove.CombinedOutput(); err != nil {
		// If worktree dir already gone, try pruning instead
		if strings.Contains(string(output), "is not a working tree") {
			prune := exec.Command("git", "worktree", "prune")
			prune.Dir = projectPath
			_ = prune.Run()
		} else {
			return fmt.Errorf("failed to remove worktree: %s: %w", string(output), err)
		}
	}

	// Delete branch (best-effort — may not be fully merged or may not exist)
	deleteBranch := exec.Command("git", "branch", "-d", branchName)
	deleteBranch.Dir = projectPath
	_ = deleteBranch.Run()

	return nil
}
