// Package diff produces structured per-task diffs for the v6.0 Ember inline
// diff viewer. Two resolution paths:
//
//   - pre-merge: the task's `watchfire/<n>` branch still exists. We diff
//     <merge-base>...HEAD on the branch.
//   - post-merge: the branch was deleted by auto-merge cleanup. We locate
//     the merge commit on the project's default branch via a `--grep`
//     scan and diff its first parent against the merge commit.
//
// Output is a structured FileDiffSet with per-hunk, per-line records — the
// GUI and TUI render natively from that, no unified-diff parsing required.
//
// Results are cached at `~/.watchfire/diff-cache/<project_id>/<n>.json`
// keyed off the task YAML mtime. The diff is immutable once the merge
// lands, so the cache is essentially write-once after the task settles.
package diff

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/watchfire-io/watchfire/internal/config"
)

// MaxDiffLines caps how many DiffLine records a single FileDiffSet can
// carry across all files. Past the cap, the renderer surfaces a
// "diff truncated, view in git" footer in both UIs.
const MaxDiffLines = 10000

// CacheDirName is the on-disk directory under ~/.watchfire/.
const CacheDirName = "diff-cache"

// FileStatus enumerates the kinds of changes a file can have inside a diff.
type FileStatus string

// FileStatus values.
const (
	StatusModified FileStatus = "modified"
	StatusAdded    FileStatus = "added"
	StatusDeleted  FileStatus = "deleted"
	StatusRenamed  FileStatus = "renamed"
)

// LineKind categorises a single line inside a hunk.
type LineKind string

// LineKind values.
const (
	LineContext LineKind = "context"
	LineAdd     LineKind = "add"
	LineDel     LineKind = "del"
)

// DiffLine is one line inside a Hunk. `text` excludes the leading +/-/space
// prefix; the kind tag carries that semantic.
type DiffLine struct {
	Kind LineKind `json:"kind"`
	Text string   `json:"text"`
}

// Hunk corresponds to a `@@ -<oldStart>,<oldLines> +<newStart>,<newLines> @@`
// region of a unified diff. `header` is the trailing context label git
// emits after the second `@@` (the function name, when available).
type Hunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Header   string     `json:"header"`
	Lines    []DiffLine `json:"lines"`
}

// FileDiff is one file-level entry inside a FileDiffSet. Binary files
// surface as `Status: modified, Hunks: [], Header: "Binary file changed"`
// — consumers can show a clear marker without trying to render bytes.
type FileDiff struct {
	Path    string     `json:"path"`
	Status  FileStatus `json:"status"`
	OldPath string     `json:"old_path,omitempty"`
	Hunks   []Hunk     `json:"hunks"`
}

// FileDiffSet is the top-level shape returned to the GUI / TUI.
type FileDiffSet struct {
	Files          []FileDiff `json:"files"`
	TotalAdditions int        `json:"total_additions"`
	TotalDeletions int        `json:"total_deletions"`
	Truncated      bool       `json:"truncated"`
}

// runner abstracts `exec.Command` so tests can inject a fake.
type runner func(dir string, args ...string) ([]byte, error)

func defaultRunner(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // args are produced internally
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s failed: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// TaskDiff computes the structured diff for a task. Steps:
//
//  1. Hit the on-disk cache keyed off the task YAML mtime.
//  2. Locate the diff range:
//     - branch `watchfire/<n>` exists → diff <merge-base>...HEAD on the branch
//     - else find the merge commit via `git log --grep="Merge watchfire/<n>"`
//     and diff <merge-commit>^..<merge-commit>
//  3. Run `git diff --no-color -M --raw` for status detection then
//     `git diff --no-color -M -p --no-prefix` for the structured patch.
//  4. Cap at MaxDiffLines lines, mark as truncated, persist to cache.
func TaskDiff(projectPath, projectID string, taskNumber int) (*FileDiffSet, error) {
	return taskDiffWithRunner(projectPath, projectID, taskNumber, defaultRunner)
}

func taskDiffWithRunner(projectPath, projectID string, taskNumber int, run runner) (*FileDiffSet, error) {
	if projectPath == "" {
		return nil, errors.New("projectPath required")
	}
	if taskNumber <= 0 {
		return nil, errors.New("taskNumber required")
	}

	taskFile := config.TaskFile(projectPath, taskNumber)
	mtime := fileMTime(taskFile)

	if cached, ok := readCache(projectID, taskNumber, mtime); ok {
		return cached, nil
	}

	rangeSpec, err := resolveDiffRange(projectPath, taskNumber, run)
	if err != nil {
		return nil, err
	}
	// `rangeSpec == ""` means no resolvable range — task never produced a
	// branch (agent crashed before commit) or the merge commit is gone.
	// Return an empty set so consumers render the empty state.
	if rangeSpec == "" {
		empty := &FileDiffSet{Files: []FileDiff{}}
		writeCache(projectID, taskNumber, mtime, empty)
		return empty, nil
	}

	rawOut, err := run(projectPath, "diff", "--no-color", "-M", "--raw", rangeSpec)
	if err != nil {
		return nil, err
	}
	patchOut, err := run(projectPath, "diff", "--no-color", "-M", "-p", "--no-prefix", rangeSpec)
	if err != nil {
		return nil, err
	}

	out := parseDiff(rawOut, patchOut)
	writeCache(projectID, taskNumber, mtime, out)
	return out, nil
}

// resolveDiffRange picks a `git diff` range spec for the task.
// Returns "" when neither the branch nor a merge commit can be found.
func resolveDiffRange(projectPath string, taskNumber int, run runner) (string, error) {
	branch := fmt.Sprintf("watchfire/%04d", taskNumber)

	// Pre-merge: branch still exists.
	if _, err := run(projectPath, "rev-parse", "--verify", "--quiet", branch); err == nil {
		baseRaw, mbErr := run(projectPath, "merge-base", "HEAD", branch)
		if mbErr != nil {
			// Fallback when there's no shared ancestor — diff against HEAD directly.
			return "HEAD..." + branch, nil
		}
		base := strings.TrimSpace(string(baseRaw))
		if base == "" {
			return "HEAD..." + branch, nil
		}
		return base + "..." + branch, nil
	}

	// Post-merge: branch deleted. Find the merge commit on the current
	// branch via the canonical "Merge watchfire/<n>" subject.
	grep := fmt.Sprintf("Merge %s", branch)
	logOut, err := run(projectPath,
		"log", "--first-parent", "--format=%H", "--grep="+grep, "-1",
	)
	if err != nil {
		return "", nil //nolint:nilerr // best-effort lookup; missing range is not an error
	}
	commit := strings.TrimSpace(string(logOut))
	if commit == "" {
		return "", nil
	}
	return commit + "^.." + commit, nil
}

// parseDiff turns `git diff --raw` + `git diff -p` output into a FileDiffSet.
// --raw gives us the file-level Status (A/M/D/R) for free; the patch output
// gives us the hunks.
func parseDiff(rawOut, patchOut []byte) *FileDiffSet {
	statusByPath := parseRaw(rawOut)
	files, totalAdd, totalDel, truncated := parsePatch(patchOut, statusByPath)
	return &FileDiffSet{
		Files:          files,
		TotalAdditions: totalAdd,
		TotalDeletions: totalDel,
		Truncated:      truncated,
	}
}

// rawEntry captures the Status / OldPath columns from `git diff --raw`.
type rawEntry struct {
	Status  FileStatus
	OldPath string
}

func parseRaw(rawOut []byte) map[string]rawEntry {
	out := map[string]rawEntry{}
	scanner := bufio.NewScanner(bytes.NewReader(rawOut))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		// Format: ":<srcMode> <dstMode> <srcSha> <dstSha> <statusLetter[score]>\t<path>[\t<newPath>]"
		line := scanner.Text()
		if !strings.HasPrefix(line, ":") {
			continue
		}
		// Split metadata from path(s)
		tab := strings.Index(line, "\t")
		if tab < 0 {
			continue
		}
		meta := line[:tab]
		paths := line[tab+1:]
		fields := strings.Fields(meta)
		if len(fields) < 5 {
			continue
		}
		statusLetter := fields[4][:1]

		var status FileStatus
		switch statusLetter {
		case "A":
			status = StatusAdded
		case "D":
			status = StatusDeleted
		case "R":
			status = StatusRenamed
		default:
			status = StatusModified
		}

		switch status {
		case StatusRenamed:
			parts := strings.SplitN(paths, "\t", 2)
			if len(parts) == 2 {
				out[parts[1]] = rawEntry{Status: StatusRenamed, OldPath: parts[0]}
			}
		default:
			out[paths] = rawEntry{Status: status}
		}
	}
	return out
}

// parsePatch walks the unified-diff output line by line. A new file starts
// when we see "diff --git ...". The cap kicks in when total recorded lines
// across all hunks reaches MaxDiffLines.
func parsePatch(patchOut []byte, statusByPath map[string]rawEntry) ([]FileDiff, int, int, bool) {
	files := []FileDiff{}
	totalAdd := 0
	totalDel := 0
	totalLines := 0
	truncated := false

	var current *FileDiff
	var hunk *Hunk

	flushFile := func() {
		if current == nil {
			return
		}
		if hunk != nil {
			current.Hunks = append(current.Hunks, *hunk)
			hunk = nil
		}
		files = append(files, *current)
		current = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(patchOut))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			path, oldPath := parseDiffGitHeader(line)
			fd := &FileDiff{Path: path, Hunks: []Hunk{}}
			if oldPath != "" && oldPath != path {
				fd.OldPath = oldPath
			}
			if entry, ok := statusByPath[path]; ok {
				fd.Status = entry.Status
				if entry.OldPath != "" {
					fd.OldPath = entry.OldPath
				}
			} else {
				fd.Status = StatusModified
			}
			current = fd

		case current != nil && strings.HasPrefix(line, "Binary files "):
			// `Binary files <old> and <new> differ` — emit a marker hunk.
			current.Hunks = []Hunk{{Header: "Binary file changed"}}

		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				continue
			}
			if hunk != nil {
				current.Hunks = append(current.Hunks, *hunk)
			}
			h := parseHunkHeader(line)
			hunk = &h

		case hunk != nil && len(line) > 0 && line[0] == '+' && !strings.HasPrefix(line, "+++"):
			if totalLines >= MaxDiffLines {
				truncated = true
				continue
			}
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: LineAdd, Text: line[1:]})
			totalAdd++
			totalLines++

		case hunk != nil && len(line) > 0 && line[0] == '-' && !strings.HasPrefix(line, "---"):
			if totalLines >= MaxDiffLines {
				truncated = true
				continue
			}
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: LineDel, Text: line[1:]})
			totalDel++
			totalLines++

		case hunk != nil && len(line) > 0 && line[0] == ' ':
			if totalLines >= MaxDiffLines {
				truncated = true
				continue
			}
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: LineContext, Text: line[1:]})
			totalLines++

		case hunk != nil && line == "":
			// Empty context line.
			if totalLines >= MaxDiffLines {
				truncated = true
				continue
			}
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: LineContext, Text: ""})
			totalLines++
		}
	}
	flushFile()

	return files, totalAdd, totalDel, truncated
}

// parseDiffGitHeader extracts new path / old path from a `diff --git a/X b/Y`
// line. With `--no-prefix` we get `diff --git X Y` instead. Either form is
// handled by stripping a leading `a/` or `b/` prefix when present.
func parseDiffGitHeader(line string) (newPath string, oldPath string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) != 2 {
		return rest, ""
	}
	return stripPrefix(parts[1]), stripPrefix(parts[0])
}

func stripPrefix(p string) string {
	if strings.HasPrefix(p, "a/") {
		return p[2:]
	}
	if strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// parseHunkHeader parses `@@ -<oldStart>[,<oldLines>] +<newStart>[,<newLines>] @@ <header>`.
func parseHunkHeader(line string) Hunk {
	// Strip the leading "@@ " and split on " @@".
	body := strings.TrimPrefix(line, "@@ ")
	closeIdx := strings.Index(body, " @@")
	if closeIdx < 0 {
		return Hunk{}
	}
	rangeSpec := body[:closeIdx]
	header := strings.TrimSpace(body[closeIdx+3:])

	parts := strings.Fields(rangeSpec)
	hunk := Hunk{Header: header}
	for _, part := range parts {
		switch {
		case strings.HasPrefix(part, "-"):
			start, lines := parseRange(part[1:])
			hunk.OldStart = start
			hunk.OldLines = lines
		case strings.HasPrefix(part, "+"):
			start, lines := parseRange(part[1:])
			hunk.NewStart = start
			hunk.NewLines = lines
		}
	}
	return hunk
}

func parseRange(s string) (start, lines int) {
	parts := strings.SplitN(s, ",", 2)
	start, _ = strconv.Atoi(parts[0])
	lines = 1
	if len(parts) == 2 {
		lines, _ = strconv.Atoi(parts[1])
	}
	return start, lines
}

func fileMTime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

// cachePath returns the per-task JSON cache path under
// `~/.watchfire/diff-cache/<project_id>/<task_number>.json`.
func cachePath(projectID string, taskNumber int) (string, error) {
	if projectID == "" {
		return "", errors.New("projectID required for cache path")
	}
	dir, err := config.GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CacheDirName, projectID, fmt.Sprintf("%04d.json", taskNumber)), nil
}

type cachedEntry struct {
	TaskMTime int64        `json:"task_mtime"`
	DiffSet   *FileDiffSet `json:"diff_set"`
}

func readCache(projectID string, taskNumber int, taskMTime int64) (*FileDiffSet, bool) {
	if projectID == "" {
		return nil, false
	}
	path, err := cachePath(projectID, taskNumber)
	if err != nil {
		return nil, false
	}
	bytes, err := os.ReadFile(path) //nolint:gosec // path is daemon-controlled
	if err != nil {
		return nil, false
	}
	var entry cachedEntry
	if err := json.Unmarshal(bytes, &entry); err != nil {
		return nil, false
	}
	if entry.TaskMTime != taskMTime || entry.DiffSet == nil {
		return nil, false
	}
	return entry.DiffSet, true
}

func writeCache(projectID string, taskNumber int, taskMTime int64, set *FileDiffSet) {
	if projectID == "" || set == nil {
		return
	}
	path, err := cachePath(projectID, taskNumber)
	if err != nil {
		return
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return
	}
	encoded, err := json.MarshalIndent(cachedEntry{TaskMTime: taskMTime, DiffSet: set}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, encoded, 0o644) //nolint:gosec // process-private cache
}
