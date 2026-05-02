package diff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
)

// gitInit prepares a small temp repo we can mutate from tests. Returns the
// repo path and a cleanup function that restores HOME / WATCHFIRE_HOME.
func gitInit(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()

	// Redirect ~/.watchfire to a per-test temp dir so cache writes don't
	// pollute the user's home.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	mustGit(t, repo, "init", "--quiet", "-b", "main")
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test")
	mustGit(t, repo, "config", "commit.gpgsign", "false")
	return repo
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, errBuf.String())
	}
	return out.String()
}

func writeFile(t *testing.T, repo, rel, content string) {
	t.Helper()
	path := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func deleteFile(t *testing.T, repo, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(repo, rel)); err != nil {
		t.Fatal(err)
	}
}

// seedTask creates the watchfire/<n>.yaml task file under repo/.watchfire/tasks
// so the cache key (task mtime) has something to read.
func seedTask(t *testing.T, repo string, n int) {
	t.Helper()
	taskFile := config.TaskFile(repo, n)
	if err := os.MkdirAll(filepath.Dir(taskFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFile, []byte(fmt.Sprintf("task_number: %d\n", n)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeBranchAndChanges seeds an initial commit on main, branches off into
// watchfire/<n>, applies the supplied mutator, and commits the result on
// the branch. Returns the branch name. Caller can then call TaskDiff on
// the still-existing branch (pre-merge path) or merge + delete to exercise
// the post-merge path.
func makeBranchAndChanges(t *testing.T, repo string, n int, mutate func()) string {
	t.Helper()
	writeFile(t, repo, "README.md", "initial\n")
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "--quiet", "-m", "init")

	branch := fmt.Sprintf("watchfire/%04d", n)
	mustGit(t, repo, "checkout", "--quiet", "-b", branch)

	mutate()

	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "--quiet", "-m", fmt.Sprintf("task %d", n))

	mustGit(t, repo, "checkout", "--quiet", "main")
	return branch
}

func mergeAndDelete(t *testing.T, repo string, n int) {
	t.Helper()
	branch := fmt.Sprintf("watchfire/%04d", n)
	mustGit(t, repo, "merge", "--quiet", "--no-ff", branch, "-m", fmt.Sprintf("Merge %s", branch))
	mustGit(t, repo, "branch", "--quiet", "-D", branch)
}

// pathsForFiles returns the set of paths in a FileDiffSet, for set-style
// assertions that don't depend on file ordering.
func pathsForFiles(set *FileDiffSet) map[string]FileDiff {
	out := map[string]FileDiff{}
	for _, f := range set.Files {
		out[f.Path] = f
	}
	return out
}

func TestTaskDiff_PreMerge_AddedModifiedDeletedRenamed(t *testing.T) {
	repo := gitInit(t)
	seedTask(t, repo, 1)

	makeBranchAndChanges(t, repo, 1, func() {
		// Need a baseline that has files we can later modify / delete /
		// rename. Commit them on main first via amending the initial set.
		writeFile(t, repo, "old_name.txt", "rename source\n")
		writeFile(t, repo, "to_modify.txt", "before\n")
		writeFile(t, repo, "to_delete.txt", "doomed\n")
	})
	// Move all initial files to main so add/modify/delete/rename detection has a baseline
	mustGit(t, repo, "branch", "--quiet", "-D", "watchfire/0001")
	mustGit(t, repo, "checkout", "--quiet", "main")
	writeFile(t, repo, "old_name.txt", "rename source\n")
	writeFile(t, repo, "to_modify.txt", "before\n")
	writeFile(t, repo, "to_delete.txt", "doomed\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "--quiet", "-m", "baseline")

	mustGit(t, repo, "checkout", "--quiet", "-b", "watchfire/0001")
	writeFile(t, repo, "added.txt", "brand new\n")
	writeFile(t, repo, "to_modify.txt", "after\n")
	deleteFile(t, repo, "to_delete.txt")
	deleteFile(t, repo, "old_name.txt")
	writeFile(t, repo, "new_name.txt", "rename source\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "--quiet", "-m", "task 1")
	mustGit(t, repo, "checkout", "--quiet", "main")

	set, err := TaskDiff(repo, "proj-1", 1)
	if err != nil {
		t.Fatalf("TaskDiff: %v", err)
	}

	files := pathsForFiles(set)

	if got := files["added.txt"]; got.Status != StatusAdded {
		t.Errorf("added.txt: status %q want %q", got.Status, StatusAdded)
	}
	if got := files["to_modify.txt"]; got.Status != StatusModified {
		t.Errorf("to_modify.txt: status %q want %q", got.Status, StatusModified)
	}
	if got := files["to_delete.txt"]; got.Status != StatusDeleted {
		t.Errorf("to_delete.txt: status %q want %q", got.Status, StatusDeleted)
	}
	renamed := files["new_name.txt"]
	if renamed.Status != StatusRenamed {
		t.Errorf("new_name.txt: status %q want %q", renamed.Status, StatusRenamed)
	}
	if renamed.OldPath != "old_name.txt" {
		t.Errorf("new_name.txt: old path %q want old_name.txt", renamed.OldPath)
	}

	if set.TotalAdditions == 0 || set.TotalDeletions == 0 {
		t.Errorf("expected non-zero +/- counts, got +%d -%d", set.TotalAdditions, set.TotalDeletions)
	}
}

func TestTaskDiff_PostMerge_FoundViaMergeCommit(t *testing.T) {
	repo := gitInit(t)
	seedTask(t, repo, 7)

	// Seed a baseline so the branch can mutate files vs main.
	writeFile(t, repo, "shared.txt", "v1\n")
	mustGit(t, repo, "add", "shared.txt")
	mustGit(t, repo, "commit", "--quiet", "-m", "baseline")

	mustGit(t, repo, "checkout", "--quiet", "-b", "watchfire/0007")
	writeFile(t, repo, "shared.txt", "v2\n")
	writeFile(t, repo, "task7-only.txt", "from task 7\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "--quiet", "-m", "task 7 work")
	mustGit(t, repo, "checkout", "--quiet", "main")
	mergeAndDelete(t, repo, 7)

	set, err := TaskDiff(repo, "proj-7", 7)
	if err != nil {
		t.Fatalf("TaskDiff: %v", err)
	}

	files := pathsForFiles(set)
	if _, ok := files["task7-only.txt"]; !ok {
		t.Errorf("expected task7-only.txt in post-merge diff, got %v", filenames(set))
	}
	if got := files["shared.txt"]; got.Status != StatusModified {
		t.Errorf("shared.txt: status %q want modified", got.Status)
	}
}

func TestTaskDiff_NoBranchNoMerge_EmptySet(t *testing.T) {
	repo := gitInit(t)
	seedTask(t, repo, 99)

	writeFile(t, repo, "README.md", "x\n")
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "--quiet", "-m", "init")

	set, err := TaskDiff(repo, "proj-99", 99)
	if err != nil {
		t.Fatalf("TaskDiff: %v", err)
	}
	if len(set.Files) != 0 {
		t.Errorf("expected empty FileDiffSet, got %d files", len(set.Files))
	}
}

func TestTaskDiff_BinaryFile(t *testing.T) {
	repo := gitInit(t)
	seedTask(t, repo, 2)

	writeFile(t, repo, "README.md", "x\n")
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "--quiet", "-m", "init")

	mustGit(t, repo, "checkout", "--quiet", "-b", "watchfire/0002")
	// Bytes that triggers git's binary detection (NUL bytes).
	binaryContent := []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 'h', 'i'}
	if err := os.WriteFile(filepath.Join(repo, "blob.bin"), binaryContent, 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "blob.bin")
	mustGit(t, repo, "commit", "--quiet", "-m", "add binary")
	mustGit(t, repo, "checkout", "--quiet", "main")

	set, err := TaskDiff(repo, "proj-bin", 2)
	if err != nil {
		t.Fatalf("TaskDiff: %v", err)
	}

	files := pathsForFiles(set)
	bin, ok := files["blob.bin"]
	if !ok {
		t.Fatalf("blob.bin missing from result: %v", filenames(set))
	}
	// `git diff` for a brand-new binary still emits "Binary files ..."; the
	// status comes from `--raw` as Added. Either way the consumer gets a
	// hunk-less marker entry — that's the contract we need.
	if len(bin.Hunks) > 0 {
		// If there is a hunk, it must be the binary marker (no lines)
		if len(bin.Hunks[0].Lines) != 0 {
			t.Errorf("blob.bin: expected hunk-less marker, got %+v", bin.Hunks[0])
		}
	}
}

func TestTaskDiff_Truncation(t *testing.T) {
	repo := gitInit(t)
	seedTask(t, repo, 3)

	writeFile(t, repo, "README.md", "x\n")
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "--quiet", "-m", "init")

	mustGit(t, repo, "checkout", "--quiet", "-b", "watchfire/0003")
	var sb strings.Builder
	for i := 0; i < MaxDiffLines+500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	writeFile(t, repo, "huge.txt", sb.String())
	mustGit(t, repo, "add", "huge.txt")
	mustGit(t, repo, "commit", "--quiet", "-m", "huge")
	mustGit(t, repo, "checkout", "--quiet", "main")

	set, err := TaskDiff(repo, "proj-trunc", 3)
	if err != nil {
		t.Fatalf("TaskDiff: %v", err)
	}
	if !set.Truncated {
		t.Errorf("expected Truncated=true on a >MaxDiffLines diff")
	}

	totalLines := 0
	for _, f := range set.Files {
		for _, h := range f.Hunks {
			totalLines += len(h.Lines)
		}
	}
	if totalLines > MaxDiffLines {
		t.Errorf("recorded %d lines exceeds cap %d", totalLines, MaxDiffLines)
	}
}

func TestTaskDiff_CacheHonoursTaskMTime(t *testing.T) {
	repo := gitInit(t)
	seedTask(t, repo, 4)

	writeFile(t, repo, "README.md", "x\n")
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "--quiet", "-m", "init")

	mustGit(t, repo, "checkout", "--quiet", "-b", "watchfire/0004")
	writeFile(t, repo, "x.txt", "hello\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "--quiet", "-m", "task 4")
	mustGit(t, repo, "checkout", "--quiet", "main")

	set1, err := TaskDiff(repo, "proj-cache", 4)
	if err != nil {
		t.Fatalf("first TaskDiff: %v", err)
	}
	if len(set1.Files) == 0 {
		t.Fatalf("expected non-empty initial result")
	}

	// Tear down the branch *and* avoid creating a merge commit. If the
	// cache works, the second call returns the same result even though
	// neither resolution path can find anything to diff.
	mustGit(t, repo, "branch", "--quiet", "-D", "watchfire/0004")

	set2, err := TaskDiff(repo, "proj-cache", 4)
	if err != nil {
		t.Fatalf("second TaskDiff: %v", err)
	}
	if len(set2.Files) != len(set1.Files) {
		t.Errorf("expected cache hit to return %d files, got %d", len(set1.Files), len(set2.Files))
	}
}

func TestParseHunkHeader(t *testing.T) {
	cases := []struct {
		in       string
		oldStart int
		oldLines int
		newStart int
		newLines int
		header   string
	}{
		{"@@ -1,3 +1,4 @@", 1, 3, 1, 4, ""},
		{"@@ -10 +12,2 @@ func foo()", 10, 1, 12, 2, "func foo()"},
		{"@@ -0,0 +1,5 @@", 0, 0, 1, 5, ""},
	}
	for _, c := range cases {
		h := parseHunkHeader(c.in)
		if h.OldStart != c.oldStart || h.OldLines != c.oldLines {
			t.Errorf("%q old: got %d,%d want %d,%d", c.in, h.OldStart, h.OldLines, c.oldStart, c.oldLines)
		}
		if h.NewStart != c.newStart || h.NewLines != c.newLines {
			t.Errorf("%q new: got %d,%d want %d,%d", c.in, h.NewStart, h.NewLines, c.newStart, c.newLines)
		}
		if h.Header != c.header {
			t.Errorf("%q header: got %q want %q", c.in, h.Header, c.header)
		}
	}
}

func filenames(set *FileDiffSet) []string {
	out := make([]string, 0, len(set.Files))
	for _, f := range set.Files {
		out = append(out, f.Path)
	}
	return out
}
