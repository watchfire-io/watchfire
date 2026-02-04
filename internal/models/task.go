package models

import "time"

// TaskStatus represents the status of a task.
type TaskStatus string

const (
	TaskStatusDraft TaskStatus = "draft"
	TaskStatusReady TaskStatus = "ready"
	TaskStatusDone  TaskStatus = "done"
)

// Task represents a task definition.
// This corresponds to task YAML files in .watchfire/tasks/ directory.
type Task struct {
	Version            int        `yaml:"version"`
	TaskID             string     `yaml:"task_id"`      // 8-char alphanumeric, internal only
	TaskNumber         int        `yaml:"task_number"`  // Sequential within project, user-facing
	Title              string     `yaml:"title"`
	Prompt             string     `yaml:"prompt"`
	AcceptanceCriteria string     `yaml:"acceptance_criteria,omitempty"`
	Status             TaskStatus `yaml:"status"` // draft | ready | done
	Success            *bool      `yaml:"success,omitempty"` // Only when status=done
	FailureReason      string     `yaml:"failure_reason,omitempty"` // Only when success=false
	Position           int        `yaml:"position"` // Display/work ordering
	AgentSessions      int        `yaml:"agent_sessions"`
	CreatedAt          time.Time  `yaml:"created_at"`
	StartedAt          *time.Time `yaml:"started_at,omitempty"`   // When agent first started
	CompletedAt        *time.Time `yaml:"completed_at,omitempty"` // When status changed to done
	UpdatedAt          time.Time  `yaml:"updated_at"`
	DeletedAt          *time.Time `yaml:"deleted_at,omitempty"`   // Soft delete timestamp
}

// NewTask creates a new task with default values.
func NewTask(id string, taskNumber int, title, prompt string) *Task {
	now := time.Now().UTC()
	return &Task{
		Version:       1,
		TaskID:        id,
		TaskNumber:    taskNumber,
		Title:         title,
		Prompt:        prompt,
		Status:        TaskStatusDraft,
		Position:      taskNumber, // Default position matches task number
		AgentSessions: 0,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// IsDeleted returns true if the task has been soft deleted.
func (t *Task) IsDeleted() bool {
	return t.DeletedAt != nil
}

// Delete soft deletes the task.
func (t *Task) Delete() {
	now := time.Now().UTC()
	t.DeletedAt = &now
	t.UpdatedAt = now
}

// Restore restores a soft deleted task.
func (t *Task) Restore() {
	t.DeletedAt = nil
	t.UpdatedAt = time.Now().UTC()
}

// MarkDone marks the task as done.
func (t *Task) MarkDone(success bool, failureReason string) {
	now := time.Now().UTC()
	t.Status = TaskStatusDone
	t.Success = &success
	if !success {
		t.FailureReason = failureReason
	}
	t.CompletedAt = &now
	t.UpdatedAt = now
}

// Start marks the task as started by an agent.
func (t *Task) Start() {
	now := time.Now().UTC()
	if t.StartedAt == nil {
		t.StartedAt = &now
	}
	t.AgentSessions++
	t.UpdatedAt = now
}
