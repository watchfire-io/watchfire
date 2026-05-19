package models

import (
	"time"

	"gopkg.in/yaml.v3"
)

// TaskStatus represents the status of a task.
type TaskStatus string

// Task statuses.
const (
	TaskStatusDraft TaskStatus = "draft"
	TaskStatusReady TaskStatus = "ready"
	TaskStatusDone  TaskStatus = "done"
)

// Task represents a task definition.
// This corresponds to task YAML files in .watchfire/tasks/ directory.
type Task struct {
	Version            int        `yaml:"version"`
	TaskID             string     `yaml:"task_id"`     // 8-char alphanumeric, internal only
	TaskNumber         int        `yaml:"task_number"` // Sequential within project, user-facing
	Title              string     `yaml:"title"`
	Prompt             string     `yaml:"prompt"`
	AcceptanceCriteria string     `yaml:"acceptance_criteria,omitempty"`
	Agent              string     `yaml:"agent,omitempty"`          // Backend name override; empty = use project default
	Status             TaskStatus `yaml:"status"`                         // draft | ready | done
	Success            *bool      `yaml:"success,omitempty"`              // Only when status=done
	FailureReason      string     `yaml:"failure_reason,omitempty"`       // Only when success=false (agent reported)
	MergeFailureReason string     `yaml:"merge_failure_reason,omitempty"` // v5.0 — populated when the post-task auto-merge fails (success can stay true; the agent's work is fine but main is dirty / conflicted)
	Position           int        `yaml:"position"`                       // Display/work ordering
	AgentSessions      int        `yaml:"agent_sessions"`
	CreatedAt          time.Time  `yaml:"created_at"`
	StartedAt          *time.Time `yaml:"started_at,omitempty"`   // When agent first started
	CompletedAt        *time.Time `yaml:"completed_at,omitempty"` // When status changed to done
	UpdatedAt          time.Time  `yaml:"updated_at"`
	DeletedAt          *time.Time `yaml:"deleted_at,omitempty"` // Soft delete timestamp
}

// NewTask creates a new task with default values. Position is left at the
// zero value — the task manager owns work-queue ordering and fills it in.
func NewTask(id string, taskNumber int, title, prompt string) *Task {
	now := time.Now().UTC()
	return &Task{
		Version:       1,
		TaskID:        id,
		TaskNumber:    taskNumber,
		Title:         title,
		Prompt:        prompt,
		Status:        TaskStatusDraft,
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

// taskTimeFields are the YAML keys whose value type is time.Time or
// *time.Time. Some agents emit `started_at: ""` (literal empty string) when
// generating new task files; gopkg.in/yaml.v3 rejects that with a parse
// error which used to poison the entire LoadAllTasks call and silently halt
// wildfire chaining (v7.2.0 fix).
var taskTimeFields = map[string]struct{}{
	"created_at":   {},
	"started_at":   {},
	"completed_at": {},
	"updated_at":   {},
	"deleted_at":   {},
}

// UnmarshalYAML treats empty-string scalars on time-typed fields as null so a
// stray `started_at: ""` (or any other timestamp written as `""`) decodes to
// the zero time / nil pointer instead of erroring out. Everything else falls
// through to the default decoder.
func (t *Task) UnmarshalYAML(node *yaml.Node) error {
	if node != nil && node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			val := node.Content[i+1]
			if key == nil || val == nil {
				continue
			}
			if key.Kind != yaml.ScalarNode || val.Kind != yaml.ScalarNode {
				continue
			}
			if _, ok := taskTimeFields[key.Value]; !ok {
				continue
			}
			if val.Value == "" {
				val.Tag = "!!null"
			}
		}
	}
	type rawTask Task
	return node.Decode((*rawTask)(t))
}
