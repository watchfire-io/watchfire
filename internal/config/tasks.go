package config

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/watchfire-io/watchfire/internal/models"
)

// ValidateTask checks that a task round-trips cleanly through the YAML
// marshal → unmarshal path the loader uses. A task whose scalars contain
// YAML-significant characters (a literal `: ` in the title, a leading `-`,
// embedded quotes, `#`, etc.) must survive serialization and re-loading
// byte-for-byte; if it doesn't, the file would be silently dropped by
// LoadAllTasks. SaveTask calls this before writing, so every daemon-side
// write is guaranteed to be loadable. (Agent-authored files written
// directly to disk bypass this — those are surfaced via LoadAllTasksWithErrors.)
func ValidateTask(task *models.Task) error {
	if task == nil {
		return fmt.Errorf("validate task: nil task")
	}

	data, err := yaml.Marshal(task)
	if err != nil {
		return fmt.Errorf("validate task #%04d: marshal: %w", task.TaskNumber, err)
	}

	var reloaded models.Task
	if err := yaml.Unmarshal(data, &reloaded); err != nil {
		return fmt.Errorf("validate task #%04d: does not round-trip through the loader: %w", task.TaskNumber, err)
	}

	// Re-marshal the reloaded copy and compare bytes. This catches any field
	// (not just the title) that fails to survive the round trip — the cheapest
	// robust structural-equality check for an arbitrary struct.
	reData, err := yaml.Marshal(&reloaded)
	if err != nil {
		return fmt.Errorf("validate task #%04d: re-marshal: %w", task.TaskNumber, err)
	}
	if !bytes.Equal(data, reData) {
		return fmt.Errorf("validate task #%04d: value changed across a save/load round trip (a scalar like the title may contain unescaped YAML-significant characters)", task.TaskNumber)
	}
	return nil
}

// MalformedTaskFile describes a task YAML file that exists on disk but failed
// to load (e.g. an agent hand-authored a `title:` with an unquoted `: ` that
// yaml.v3 parses as a nested mapping). These used to vanish with only a
// daemon log line; LoadAllTasksWithErrors returns them so the CLI/TUI/GUI can
// surface a non-silent "N task file(s) failed to load" affordance.
type MalformedTaskFile struct {
	TaskNumber int    // parsed from the filename (e.g. 0107.yaml -> 107)
	FileName   string // base name, e.g. "0107.yaml"
	Path       string // absolute path on disk
	Error      string // the parse/load error message
}

// LoadTask loads a task from its YAML file.
func LoadTask(projectPath string, taskNumber int) (*models.Task, error) {
	path := TaskFile(projectPath, taskNumber)

	if !FileExists(path) {
		return nil, nil
	}

	var task models.Task
	if err := LoadYAML(path, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

// SaveTask saves a task to its YAML file. The task is validated first so a
// daemon-side write can never produce a file the loader would later drop.
func SaveTask(projectPath string, task *models.Task) error {
	if err := ValidateTask(task); err != nil {
		return err
	}
	if err := os.MkdirAll(ProjectTasksDir(projectPath), 0o755); err != nil {
		return err
	}
	return SaveYAML(TaskFile(projectPath, task.TaskNumber), task)
}

// DeleteTaskFile permanently deletes a task file.
func DeleteTaskFile(projectPath string, taskNumber int) error {
	path := TaskFile(projectPath, taskNumber)
	if !FileExists(path) {
		return nil
	}
	return os.Remove(path)
}

// LoadAllTasks loads all tasks from a project's tasks directory. Malformed
// files are skipped (and logged); callers that need to surface them to the
// user should use LoadAllTasksWithErrors instead.
func LoadAllTasks(projectPath string) ([]*models.Task, error) {
	tasks, _, err := LoadAllTasksWithErrors(projectPath)
	return tasks, err
}

// LoadAllTasksWithErrors loads all tasks from a project's tasks directory,
// returning both the tasks that loaded cleanly and a list of files that
// failed to parse.
//
// A single malformed task file (e.g. an agent emitting `started_at: ""` that
// the strict time decoder rejects, or a `title:` with an unquoted `: ` that
// yaml.v3 parses as a nested mapping) used to abort the whole list and
// silently halt wildfire chaining — the chain saw a load error and bailed
// without surfacing anything to the user. Per-file errors are now logged and
// skipped so the chain can still pick up the remaining tasks (v7.2.0 fix),
// AND collected here so the CLI/TUI/GUI can show a non-silent
// "N task file(s) failed to load" affordance (v8.0 Inferno fix) instead of
// the file vanishing with only a daemon log line.
func LoadAllTasksWithErrors(projectPath string) ([]*models.Task, []MalformedTaskFile, error) {
	tasksDir := ProjectTasksDir(projectPath)

	if !FileExists(tasksDir) {
		return []*models.Task{}, nil, nil
	}

	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, nil, err
	}

	var tasks []*models.Task
	var malformed []MalformedTaskFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}

		// Skip sibling metrics files (`<n>.metrics.yaml`) — they are not
		// task files and parse into a different struct.
		if strings.HasSuffix(name, ".metrics.yaml") {
			continue
		}

		// Parse task number from filename
		numStr := strings.TrimSuffix(name, ".yaml")
		taskNum, err := strconv.Atoi(numStr)
		if err != nil {
			continue // Skip invalid filenames
		}

		task, err := LoadTask(projectPath, taskNum)
		if err != nil {
			log.Printf("[task-load] skipping %s in %s: %v", name, tasksDir, err)
			malformed = append(malformed, MalformedTaskFile{
				TaskNumber: taskNum,
				FileName:   name,
				Path:       TaskFile(projectPath, taskNum),
				Error:      err.Error(),
			})
			continue
		}
		if task != nil {
			tasks = append(tasks, task)
		}
	}

	return tasks, malformed, nil
}

// LoadMalformedTasks returns only the task files in a project that failed to
// load. Convenience wrapper over LoadAllTasksWithErrors for callers that just
// want the malformed set (CLI warning, gRPC ListMalformedTasks).
func LoadMalformedTasks(projectPath string) ([]MalformedTaskFile, error) {
	_, malformed, err := LoadAllTasksWithErrors(projectPath)
	return malformed, err
}

// LoadActiveTasks loads all non-deleted tasks from a project.
func LoadActiveTasks(projectPath string) ([]*models.Task, error) {
	tasks, err := LoadAllTasks(projectPath)
	if err != nil {
		return nil, err
	}

	var active []*models.Task
	for _, t := range tasks {
		if !t.IsDeleted() {
			active = append(active, t)
		}
	}
	return active, nil
}

// LoadDeletedTasks loads all soft-deleted tasks from a project.
func LoadDeletedTasks(projectPath string) ([]*models.Task, error) {
	tasks, err := LoadAllTasks(projectPath)
	if err != nil {
		return nil, err
	}

	var deleted []*models.Task
	for _, t := range tasks {
		if t.IsDeleted() {
			deleted = append(deleted, t)
		}
	}
	return deleted, nil
}

// GetNextTaskNumber returns the next available task number for a project.
func GetNextTaskNumber(projectPath string) (int, error) {
	project, err := LoadProject(projectPath)
	if err != nil {
		return 0, err
	}
	if project == nil {
		return 1, nil
	}
	return project.NextTaskNumber, nil
}

// SyncNextTaskNumber scans the tasks directory and updates next_task_number
// in project.yaml if it's behind the highest existing task file.
// This handles the case where agents create task files directly (bypassing
// the task manager) without incrementing the counter.
func SyncNextTaskNumber(projectPath string) error {
	project, err := LoadProject(projectPath)
	if err != nil || project == nil {
		return err
	}

	// Defensive: never round-trip an incomplete project struct back to disk.
	// LoadProject already rejects zero-valued reads, but keep the guard here
	// so this function — which is the one that historically produced the
	// data-loss bug — can never be the writer that clobbers good metadata.
	if project.Version == 0 || project.ProjectID == "" {
		return fmt.Errorf("refusing to sync next_task_number on incomplete project at %s", projectPath)
	}

	tasksDir := ProjectTasksDir(projectPath)
	if !FileExists(tasksDir) {
		return nil
	}

	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return err
	}

	highest := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		numStr := strings.TrimSuffix(name, ".yaml")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		if num > highest {
			highest = num
		}
	}

	// next_task_number should be highest + 1
	needed := highest + 1
	if needed > project.NextTaskNumber {
		project.NextTaskNumber = needed
		return SaveProject(projectPath, project)
	}
	return nil
}

// WatchTasksDir returns the path to watch for task file changes.
func WatchTasksDir(projectPath string) string {
	return filepath.Join(ProjectDir(projectPath), TasksDirName)
}

// HighestTaskNumber scans the tasks directory and returns the largest
// integer-named YAML file's number, or 0 when no task files exist. Used by
// the v6 (#0091) "reset task numbering" danger-zone action — unlike
// SyncNextTaskNumber it does NOT mutate state, just reports the count.
func HighestTaskNumber(projectPath string) (int, error) {
	tasksDir := ProjectTasksDir(projectPath)
	if !FileExists(tasksDir) {
		return 0, nil
	}
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		numStr := strings.TrimSuffix(name, ".yaml")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		if num > highest {
			highest = num
		}
	}
	return highest, nil
}
