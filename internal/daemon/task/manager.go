// Package task handles task management for the daemon.
package task

import (
	"fmt"
	"sort"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// Manager handles task operations.
type Manager struct{}

// NewManager creates a new task manager.
func NewManager() *Manager {
	return &Manager{}
}

// ListOptions contains options for listing tasks.
type ListOptions struct {
	Status         *string
	IncludeDeleted bool
}

// CreateOptions contains options for creating a task.
type CreateOptions struct {
	Title              string
	Prompt             string
	AcceptanceCriteria string
	Status             string
	Position           *int
}

// UpdateOptions contains options for updating a task.
type UpdateOptions struct {
	TaskNumber         int
	Title              *string
	Prompt             *string
	AcceptanceCriteria *string
	Status             *string
	Success            *bool
	FailureReason      *string
	Position           *int
}

// ListTasks returns tasks for a project.
func (m *Manager) ListTasks(projectPath string, opts ListOptions) ([]*models.Task, error) {
	var tasks []*models.Task
	var err error

	if opts.IncludeDeleted {
		tasks, err = config.LoadAllTasks(projectPath)
	} else {
		tasks, err = config.LoadActiveTasks(projectPath)
	}
	if err != nil {
		return nil, err
	}

	// Filter by status if specified
	if opts.Status != nil {
		var filtered []*models.Task
		for _, t := range tasks {
			if string(t.Status) == *opts.Status {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	// Sort by position, then by task number
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Position != tasks[j].Position {
			return tasks[i].Position < tasks[j].Position
		}
		return tasks[i].TaskNumber < tasks[j].TaskNumber
	})

	return tasks, nil
}

// GetTask retrieves a task by number.
func (m *Manager) GetTask(projectPath string, taskNumber int) (*models.Task, error) {
	task, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %d", taskNumber)
	}
	return task, nil
}

// CreateTask creates a new task.
func (m *Manager) CreateTask(projectPath string, opts CreateOptions) (*models.Task, error) {
	// Load project to get next task number
	project, err := config.LoadProject(projectPath)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", projectPath)
	}

	// Generate task ID (8-char alphanumeric)
	taskID := generateTaskID()
	taskNumber := project.NextTaskNumber

	// Create task
	task := models.NewTask(taskID, taskNumber, opts.Title, opts.Prompt)
	task.AcceptanceCriteria = opts.AcceptanceCriteria

	// Set status
	if opts.Status != "" {
		task.Status = models.TaskStatus(opts.Status)
	}

	// Set position
	if opts.Position != nil {
		task.Position = *opts.Position
	} else {
		task.Position = taskNumber
	}

	// Save task
	if err := config.SaveTask(projectPath, task); err != nil {
		return nil, err
	}

	// Increment next task number
	project.NextTaskNumber++
	project.UpdatedAt = time.Now().UTC()
	if err := config.SaveProject(projectPath, project); err != nil {
		return nil, err
	}

	return task, nil
}

// UpdateTask updates an existing task.
func (m *Manager) UpdateTask(projectPath string, opts UpdateOptions) (*models.Task, error) {
	task, err := config.LoadTask(projectPath, opts.TaskNumber)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %d", opts.TaskNumber)
	}

	// Apply updates
	if opts.Title != nil {
		task.Title = *opts.Title
	}
	if opts.Prompt != nil {
		task.Prompt = *opts.Prompt
	}
	if opts.AcceptanceCriteria != nil {
		task.AcceptanceCriteria = *opts.AcceptanceCriteria
	}
	if opts.Status != nil {
		task.Status = models.TaskStatus(*opts.Status)
	}
	if opts.Success != nil {
		task.Success = opts.Success
	}
	if opts.FailureReason != nil {
		task.FailureReason = *opts.FailureReason
	}
	if opts.Position != nil {
		task.Position = *opts.Position
	}

	task.UpdatedAt = time.Now().UTC()

	// Save task
	if err := config.SaveTask(projectPath, task); err != nil {
		return nil, err
	}

	return task, nil
}

// DeleteTask soft-deletes a task.
func (m *Manager) DeleteTask(projectPath string, taskNumber int) (*models.Task, error) {
	task, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %d", taskNumber)
	}

	task.Delete()

	if err := config.SaveTask(projectPath, task); err != nil {
		return nil, err
	}

	return task, nil
}

// RestoreTask restores a soft-deleted task.
func (m *Manager) RestoreTask(projectPath string, taskNumber int) (*models.Task, error) {
	task, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %d", taskNumber)
	}

	task.Restore()

	if err := config.SaveTask(projectPath, task); err != nil {
		return nil, err
	}

	return task, nil
}

// EmptyTrash permanently deletes all soft-deleted tasks.
func (m *Manager) EmptyTrash(projectPath string) error {
	tasks, err := config.LoadDeletedTasks(projectPath)
	if err != nil {
		return err
	}

	for _, task := range tasks {
		if err := config.DeleteTaskFile(projectPath, task.TaskNumber); err != nil {
			return err
		}
	}

	return nil
}

// generateTaskID generates an 8-character alphanumeric task ID.
func generateTaskID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	// Use time-based seed for simplicity (in production, use crypto/rand)
	t := time.Now().UnixNano()
	for i := range b {
		b[i] = chars[(t>>uint(i*5))%int64(len(chars))]
	}
	return string(b)
}
