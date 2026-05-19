package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/watchfire-io/watchfire/internal/models"
)

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

// SaveTask saves a task to its YAML file.
func SaveTask(projectPath string, task *models.Task) error {
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

// LoadAllTasks loads all tasks from a project's tasks directory.
func LoadAllTasks(projectPath string) ([]*models.Task, error) {
	tasksDir := ProjectTasksDir(projectPath)

	if !FileExists(tasksDir) {
		return []*models.Task{}, nil
	}

	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, err
	}

	var tasks []*models.Task
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}

		// Parse task number from filename
		numStr := strings.TrimSuffix(name, ".yaml")
		taskNum, err := strconv.Atoi(numStr)
		if err != nil {
			continue // Skip invalid filenames
		}

		// A single malformed task file (e.g. an agent emitting
		// `started_at: ""` that the strict time decoder rejects) used to
		// abort the whole list and silently halt wildfire chaining — the
		// chain saw a load error and bailed without surfacing anything
		// to the user. Per-file errors are now logged and skipped so the
		// chain can still pick up the remaining tasks (v7.2.0 fix).
		task, err := LoadTask(projectPath, taskNum)
		if err != nil {
			log.Printf("[task-load] skipping %s in %s: %v", name, tasksDir, err)
			continue
		}
		if task != nil {
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
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
