package agent

import (
	"fmt"
	"log"
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
		if strings.Contains(string(output), "already exists") {
			// Stale branch from a previous run — delete it and retry with -b
			// so the new branch starts from current HEAD (not the old commit).
			log.Printf("[worktree] Branch %s already exists — deleting stale branch and recreating from HEAD", branchName)
			delCmd := exec.Command("git", "branch", "-D", branchName)
			delCmd.Dir = projectPath
			if delOut, delErr := delCmd.CombinedOutput(); delErr != nil {
				return "", fmt.Errorf("failed to delete stale branch %s: %s: %w", branchName, strings.TrimSpace(string(delOut)), delErr)
			}
			cmd = exec.Command("git", "worktree", "add", worktreePath, "-b", branchName)
			cmd.Dir = projectPath
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("failed to create worktree after branch delete: %s: %w", string(output), err)
			}
		} else {
			return "", fmt.Errorf("failed to create worktree: %s: %w", string(output), err)
		}
	}

	return worktreePath, nil
}

// MergeWorktree merges the worktree branch into the target branch using --no-ff.
// Must be run from the project root (main worktree).
// Returns (true, nil) if merge succeeded, (false, nil) if no file differences, or (false, err) on failure.
func MergeWorktree(projectPath string, taskNumber int, targetBranch string) (bool, error) {
	padded := fmt.Sprintf("%04d", taskNumber)
	branchName := fmt.Sprintf("watchfire/%s", padded)

	// Check current branch — only checkout if not already on target
	revParse := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	revParse.Dir = projectPath
	currentBranch, err := revParse.Output()
	if err != nil {
		return false, fmt.Errorf("failed to determine current branch: %w", err)
	}

	if strings.TrimSpace(string(currentBranch)) != targetBranch {
		checkout := exec.Command("git", "checkout", targetBranch)
		checkout.Dir = projectPath
		if output, err := checkout.CombinedOutput(); err != nil {
			return false, fmt.Errorf("failed to checkout %s: %s: %w", targetBranch, strings.TrimSpace(string(output)), err)
		}
	}

	// Log branch positions for debugging
	mainHead := exec.Command("git", "rev-parse", "--short", targetBranch)
	mainHead.Dir = projectPath
	if out, err := mainHead.Output(); err == nil {
		log.Printf("[merge] %s HEAD: %s", targetBranch, strings.TrimSpace(string(out)))
	}
	branchHead := exec.Command("git", "rev-parse", "--short", branchName)
	branchHead.Dir = projectPath
	if out, err := branchHead.Output(); err == nil {
		log.Printf("[merge] %s HEAD: %s", branchName, strings.TrimSpace(string(out)))
	}

	// Check for actual content differences (not just commit ancestry).
	// git diff --stat catches changes even after cherry-picks/rebases where
	// git log main..branch would incorrectly report no new commits.
	diffCheck := exec.Command("git", "diff", "--stat", targetBranch, branchName)
	diffCheck.Dir = projectPath
	diffOutput, err := diffCheck.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check branch diff: %w", err)
	}
	if len(strings.TrimSpace(string(diffOutput))) == 0 {
		log.Printf("[merge] Branch %s has no file differences vs %s — nothing to merge", branchName, targetBranch)
		return false, nil
	}

	log.Printf("[merge] Branch %s has changes:\n%s", branchName, strings.TrimSpace(string(diffOutput)))

	// Merge with --no-ff
	merge := exec.Command("git", "merge", "--no-ff", branchName, "-m",
		fmt.Sprintf("Merge %s", branchName))
	merge.Dir = projectPath
	if output, err := merge.CombinedOutput(); err != nil {
		// Abort the failed merge to restore a clean working directory
		abortCmd := exec.Command("git", "merge", "--abort")
		abortCmd.Dir = projectPath
		if abortOut, abortErr := abortCmd.CombinedOutput(); abortErr != nil {
			log.Printf("[merge] Warning: git merge --abort failed: %s", strings.TrimSpace(string(abortOut)))
		} else {
			log.Printf("[merge] Aborted failed merge — working directory restored")
		}
		return false, fmt.Errorf("merge failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Force-refresh working directory to match the merged state.
	// This ensures the project root's files reflect the merge even if
	// git's working tree cache is stale.
	reset := exec.Command("git", "reset", "--hard", "HEAD")
	reset.Dir = projectPath
	if output, err := reset.CombinedOutput(); err != nil {
		log.Printf("[merge] Warning: git reset --hard failed after merge: %s", strings.TrimSpace(string(output)))
	}

	// Verify merge landed
	verify := exec.Command("git", "log", "--oneline", "-1")
	verify.Dir = projectPath
	if out, err := verify.Output(); err == nil {
		log.Printf("[merge] HEAD after merge: %s", strings.TrimSpace(string(out)))
	}

	return true, nil
}

// RemoveWorktree removes the worktree directory and deletes the branch.
// If merged is true, force-deletes the branch (-D). Otherwise uses safe delete (-d)
// which refuses if the branch has unmerged changes, preserving work.
func RemoveWorktree(projectPath string, taskNumber int, merged bool) error {
	padded := fmt.Sprintf("%04d", taskNumber)
	worktreePath := filepath.Join(projectPath, ".watchfire", "worktrees", padded)
	branchName := fmt.Sprintf("watchfire/%s", padded)

	// Remove worktree (unregisters + removes directory)
	// Use --force to handle untracked files (build artifacts, node_modules, etc.)
	remove := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	remove.Dir = projectPath
	if output, err := remove.CombinedOutput(); err != nil {
		outStr := string(output)
		log.Printf("[worktree] git worktree remove failed: %s", strings.TrimSpace(outStr))

		// Prune stale worktree tracking
		prune := exec.Command("git", "worktree", "prune")
		prune.Dir = projectPath
		_ = prune.Run()

		// Fallback: force-remove the directory if it still exists
		if _, statErr := os.Stat(worktreePath); statErr == nil {
			log.Printf("[worktree] Fallback: removing directory %s", worktreePath)
			if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
				log.Printf("[worktree] Failed to remove directory: %v", rmErr)
			}
			// Prune again after manual removal
			prune2 := exec.Command("git", "worktree", "prune")
			prune2.Dir = projectPath
			_ = prune2.Run()
		}
	}

	// Delete branch: use -D (force) only if merge succeeded, otherwise -d (safe)
	// which refuses to delete branches with unmerged changes.
	deleteFlag := "-d"
	if merged {
		deleteFlag = "-D"
	}
	deleteBranch := exec.Command("git", "branch", deleteFlag, branchName)
	deleteBranch.Dir = projectPath
	if output, err := deleteBranch.CombinedOutput(); err != nil {
		if merged {
			log.Printf("[worktree] Branch delete failed for %s: %s", branchName, strings.TrimSpace(string(output)))
		} else {
			log.Printf("[worktree] WARNING: Branch %s has unmerged changes, keeping branch for safety", branchName)
		}
	} else {
		log.Printf("[worktree] Deleted branch %s", branchName)
	}

	return nil
}
