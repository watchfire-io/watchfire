package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeBranchTestRepo lays down a temp git repo with one commit on `main`,
// optionally seeds a few `watchfire/<n>` branches (with optional companion
// worktree directories under `.watchfire/worktrees/<n>` to exercise the
// presence flag), and returns the project path. The unmerged branches
// add a fresh commit on top of main; the merged ones leave the branch at
// main HEAD so the daemon's `git branch --merged main` filter sees them.
func makeBranchTestRepo(t *testing.T, branches []branchSpec) string {
	t.Helper()
	dir := t.TempDir()

	must := func(cmd *exec.Cmd) {
		t.Helper()
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, out)
		}
	}

	must(exec.Command("git", "init", "-q", "-b", "main"))
	must(exec.Command("git", "config", "user.email", "test@example.com"))
	must(exec.Command("git", "config", "user.name", "Test"))
	must(exec.Command("git", "config", "commit.gpgsign", "false"))

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(exec.Command("git", "add", "README.md"))
	must(exec.Command("git", "commit", "-q", "-m", "init"))

	// Phase 1: create branches + their commits. We add files only via
	// explicit `git add <path>` (NOT `-A`) so that worktree dirs created
	// in phase 2 don't get swept into a later branch's commit and then
	// removed by `git checkout main`.
	for _, b := range branches {
		must(exec.Command("git", "branch", b.name))
		if b.unmergedCommit {
			must(exec.Command("git", "checkout", "-q", b.name))
			rel := "work-" + b.padded + ".txt"
			path := filepath.Join(dir, rel)
			if err := os.WriteFile(path, []byte("work\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			must(exec.Command("git", "add", rel))
			must(exec.Command("git", "commit", "-q", "-m", "work on "+b.name))
			must(exec.Command("git", "checkout", "-q", "main"))
		}
	}

	// Phase 2: lay down the worktree dirs. Done after every branch is
	// committed so untracked-then-checkout-main can't wipe them.
	for _, b := range branches {
		if b.worktreeOnDisk {
			wtDir := filepath.Join(dir, ".watchfire", "worktrees", b.padded)
			if err := os.MkdirAll(wtDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wtDir, ".keep"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	return dir
}

type branchSpec struct {
	name           string
	padded         string
	unmergedCommit bool
	worktreeOnDisk bool
}

// TestListGitBranchesPopulatesWorktreeFlag covers the spec's
// "Worktree present/absent" requirement: a worktree directory at
// `.watchfire/worktrees/<n>` flips the proto's worktree_path to a
// non-empty string, and a missing directory leaves it empty.
func TestListGitBranchesPopulatesWorktreeFlag(t *testing.T) {
	repo := makeBranchTestRepo(t, []branchSpec{
		{name: "watchfire/0001", padded: "0001", unmergedCommit: true, worktreeOnDisk: true},
		{name: "watchfire/0002", padded: "0002", unmergedCommit: true, worktreeOnDisk: false},
	})

	got, err := listGitBranches(repo, "proj-1")
	if err != nil {
		t.Fatalf("listGitBranches: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(got))
	}

	byName := map[string]int{}
	for i, b := range got {
		byName[b.GetName()] = i
	}

	wt0001 := got[byName["watchfire/0001"]]
	if wt0001.GetWorktreePath() == "" {
		t.Errorf("watchfire/0001 should have non-empty WorktreePath, got empty")
	}
	wt0002 := got[byName["watchfire/0002"]]
	if wt0002.GetWorktreePath() != "" {
		t.Errorf("watchfire/0002 should have empty WorktreePath, got %q", wt0002.GetWorktreePath())
	}
}

// TestListGitBranchesMergedOrphanDefinition covers the rollup primitive
// the TUI uses to compute the merged-orphan count: status == "merged"
// AND worktree directory absent. A merged branch with the worktree
// still on disk is NOT an orphan; an unmerged branch never is.
func TestListGitBranchesMergedOrphanDefinition(t *testing.T) {
	repo := makeBranchTestRepo(t, []branchSpec{
		{name: "watchfire/0010", padded: "0010", unmergedCommit: false, worktreeOnDisk: false}, // merged + no worktree → orphan
		{name: "watchfire/0011", padded: "0011", unmergedCommit: false, worktreeOnDisk: true},  // merged + worktree → not orphan
		{name: "watchfire/0012", padded: "0012", unmergedCommit: true, worktreeOnDisk: false},  // unmerged + no worktree → not orphan
	})

	got, err := listGitBranches(repo, "proj-1")
	if err != nil {
		t.Fatalf("listGitBranches: %v", err)
	}

	byName := map[string]int{}
	for i, b := range got {
		byName[b.GetName()] = i
	}

	orphan := got[byName["watchfire/0010"]]
	if orphan.GetStatus() != "merged" {
		t.Errorf("watchfire/0010 status: want merged, got %q", orphan.GetStatus())
	}
	if orphan.GetWorktreePath() != "" {
		t.Errorf("watchfire/0010 should have no worktree dir")
	}

	merged := got[byName["watchfire/0011"]]
	if merged.GetStatus() != "merged" {
		t.Errorf("watchfire/0011 status: want merged, got %q", merged.GetStatus())
	}
	if merged.GetWorktreePath() == "" {
		t.Errorf("watchfire/0011 should have worktree dir")
	}

	unmerged := got[byName["watchfire/0012"]]
	if unmerged.GetStatus() == "merged" {
		t.Errorf("watchfire/0012 status: want unmerged-ish, got %q", unmerged.GetStatus())
	}
}

// TestListGitBranchesSortedByCommitDesc verifies the daemon sorts the
// returned branches most-recent first, so the TUI doesn't have to.
func TestListGitBranchesSortedByCommitDesc(t *testing.T) {
	repo := makeBranchTestRepo(t, []branchSpec{
		{name: "watchfire/0001", padded: "0001", unmergedCommit: true},
		{name: "watchfire/0002", padded: "0002", unmergedCommit: true},
		{name: "watchfire/0003", padded: "0003", unmergedCommit: true},
	})

	got, err := listGitBranches(repo, "proj-1")
	if err != nil {
		t.Fatalf("listGitBranches: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].GetCommitTimestamp() < got[i].GetCommitTimestamp() {
			t.Errorf("branches not sorted desc by commit ts at index %d: %d < %d", i,
				got[i-1].GetCommitTimestamp(), got[i].GetCommitTimestamp())
		}
	}
}

func TestTaskNumberFromBranch(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"watchfire/0001", 1},
		{"watchfire/0087", 87},
		{"watchfire/9999", 9999},
		{"main", 0},
		{"", 0},
		{"feature/foo", 0},
	}
	for _, c := range cases {
		if got := taskNumberFromBranch(c.in); got != c.want {
			t.Errorf("taskNumberFromBranch(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
