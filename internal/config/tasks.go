package config

import (
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

		task, err := LoadTask(projectPath, taskNum)
		if err != nil {
			return nil, err
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

// WatchTasksDir returns the path to watch for task file changes.
func WatchTasksDir(projectPath string) string {
	return filepath.Join(ProjectDir(projectPath), TasksDirName)
}
