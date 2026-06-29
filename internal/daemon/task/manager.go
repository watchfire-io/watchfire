// Package task handles task management for the daemon.
package task

import (
	"fmt"
	"os"
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
	Agent              string
	Status             string
	Position           *int
}

// UpdateOptions contains options for updating a task.
type UpdateOptions struct {
	TaskNumber         int
	Title              *string
	Prompt             *string
	AcceptanceCriteria *string
	Agent              *string
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

	// Canonical order: oldest first by (position ASC, task_number ASC).
	// Position is the manual-override knob; task_number is the implicit
	// creation-order tiebreaker. Applied here at the task manager so every
	// caller — TUI, GUI, any gRPC consumer, plus the start-all and wildfire
	// next-task pickers that take tasks[0] — sees the same ordering.
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Position != tasks[j].Position {
			return tasks[i].Position < tasks[j].Position
		}
		return tasks[i].TaskNumber < tasks[j].TaskNumber
	})

	return tasks, nil
}

// ListMalformedTasks returns the task files in a project that exist on disk
// but failed to parse — files that LoadAllTasks silently skips. The GUI/TUI
// surface these so a broken task file is visible instead of just vanishing.
func (m *Manager) ListMalformedTasks(projectPath string) ([]config.MalformedTaskFile, error) {
	return config.LoadMalformedTasks(projectPath)
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
	// Sync next_task_number in case agents created files directly
	_ = config.SyncNextTaskNumber(projectPath)

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
	task.Agent = opts.Agent

	// Set status
	if opts.Status != "" {
		task.Status = models.TaskStatus(opts.Status)
	}

	// Set position. Default: append to the bottom of the work queue
	// (max(active.position)+1, or 1 if there are no active tasks). Using
	// taskNumber as the default broke manual reorders — a new task could
	// jump ahead of tasks the user just dragged down. An explicit caller-
	// provided opts.Position still wins.
	if opts.Position != nil {
		task.Position = *opts.Position
	} else {
		active, err := config.LoadActiveTasks(projectPath)
		if err != nil {
			return nil, err
		}
		maxPos := 0
		for _, t := range active {
			if t.Position > maxPos {
				maxPos = t.Position
			}
		}
		task.Position = maxPos + 1
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
	if opts.Agent != nil {
		task.Agent = *opts.Agent
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

// BulkUpdateStatus sets the same status on every task in taskNumbers.
// Tasks whose status already matches are skipped. Missing tasks are silently
// ignored so a partially stale UI selection doesn't fail the whole request.
// Returns the updated tasks in the canonical newest-first order.
func (m *Manager) BulkUpdateStatus(projectPath string, taskNumbers []int, newStatus string) ([]*models.Task, error) {
	if newStatus != string(models.TaskStatusDraft) &&
		newStatus != string(models.TaskStatusReady) &&
		newStatus != string(models.TaskStatusDone) {
		return nil, fmt.Errorf("invalid status: %s", newStatus)
	}

	status := models.TaskStatus(newStatus)
	updated := make([]*models.Task, 0, len(taskNumbers))
	now := time.Now().UTC()
	for _, n := range taskNumbers {
		t, err := config.LoadTask(projectPath, n)
		if err != nil {
			return nil, err
		}
		if t == nil || t.IsDeleted() || t.Status == status {
			continue
		}
		t.Status = status
		t.UpdatedAt = now
		if status == models.TaskStatusDone {
			success := true
			t.Success = &success
			t.CompletedAt = &now
		} else {
			// Moving back out of done: clear terminal-only fields so future
			// rereads don't see a stale success/failure trace.
			t.Success = nil
			t.FailureReason = ""
			t.CompletedAt = nil
		}
		if err := config.SaveTask(projectPath, t); err != nil {
			return nil, err
		}
		updated = append(updated, t)
	}

	sort.Slice(updated, func(i, j int) bool {
		return updated[i].TaskNumber > updated[j].TaskNumber
	})
	return updated, nil
}

// ReorderTasks rewrites positions densely (1..N) in the order given by
// taskNumbers. Tasks not in the request preserve their current relative order
// (canonical Position ASC, TaskNumber ASC) and get appended after. Mirrors the
// shape of project.Manager.ReorderProjects.
func (m *Manager) ReorderTasks(projectPath string, taskNumbers []int) ([]*models.Task, error) {
	active, err := config.LoadActiveTasks(projectPath)
	if err != nil {
		return nil, err
	}

	byNumber := make(map[int]*models.Task, len(active))
	for _, t := range active {
		byNumber[t.TaskNumber] = t
	}

	seen := make(map[int]bool, len(taskNumbers))
	ordered := make([]*models.Task, 0, len(active))
	for _, n := range taskNumbers {
		if seen[n] {
			return nil, fmt.Errorf("duplicate task in reorder request: %d", n)
		}
		t, ok := byNumber[n]
		if !ok {
			return nil, fmt.Errorf("task not found: %d", n)
		}
		seen[n] = true
		ordered = append(ordered, t)
	}

	// Append any active tasks not mentioned in the request, in canonical order
	// (Position ASC, TaskNumber ASC). A buggy client could send a partial list;
	// silently parking the leftovers at the end of the queue is the conservative
	// fix — they keep their relative order and the next reorder normalises.
	leftovers := make([]*models.Task, 0, len(active)-len(ordered))
	for _, t := range active {
		if !seen[t.TaskNumber] {
			leftovers = append(leftovers, t)
		}
	}
	sort.Slice(leftovers, func(i, j int) bool {
		if leftovers[i].Position != leftovers[j].Position {
			return leftovers[i].Position < leftovers[j].Position
		}
		return leftovers[i].TaskNumber < leftovers[j].TaskNumber
	})
	ordered = append(ordered, leftovers...)

	now := time.Now().UTC()
	for i, t := range ordered {
		t.Position = i + 1
		t.UpdatedAt = now
		if err := config.SaveTask(projectPath, t); err != nil {
			return nil, err
		}
	}

	// Re-list so the response reflects the persisted state through the
	// canonical sort — keeps callers honest about what's on disk.
	return m.ListTasks(projectPath, ListOptions{})
}

// PermanentDelete hard-deletes a soft-deleted task: removes the task YAML and
// any sibling `<n>.metrics.yaml`. Refuses if the task isn't soft-deleted, or
// if branchMerged returns false for the task's `watchfire/<n>` branch — the
// caller wires branchMerged so the manager package stays free of git deps.
// branchMerged may be nil when the caller has already verified the branch is
// merged (or doesn't care, e.g. tests).
func (m *Manager) PermanentDelete(projectPath string, taskNumber int, branchMerged func(int) (bool, error)) error {
	t, err := config.LoadTask(projectPath, taskNumber)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("task not found: %d", taskNumber)
	}
	if !t.IsDeleted() {
		return fmt.Errorf("task %d is not soft-deleted; use DeleteTask first", taskNumber)
	}
	if branchMerged != nil {
		merged, err := branchMerged(taskNumber)
		if err != nil {
			return fmt.Errorf("branch check: %w", err)
		}
		if !merged {
			return fmt.Errorf("task %d branch is unmerged; merge or delete it first", taskNumber)
		}
	}

	if err := config.DeleteTaskFile(projectPath, taskNumber); err != nil {
		return err
	}
	// Best-effort sibling cleanup — a missing metrics file is fine.
	metricsPath := config.MetricsFile(projectPath, taskNumber)
	if config.FileExists(metricsPath) {
		if err := os.Remove(metricsPath); err != nil {
			return fmt.Errorf("remove metrics: %w", err)
		}
	}
	return nil
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
