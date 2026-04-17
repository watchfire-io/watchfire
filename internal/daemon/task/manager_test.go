package task

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// setupTempProject creates a minimal project on disk so Create/UpdateTask can
// round-trip against real YAML files. Returns the project path.
func setupTempProject(t *testing.T) string {
	t.Helper()
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	now := time.Now().UTC()
	p := &models.Project{
		ProjectID:      "testproj",
		Name:           "test",
		Status:         "active",
		NextTaskNumber: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := config.SaveProject(projectPath, p); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	return projectPath
}

func TestCreateTaskPersistsAgent(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "Use Codex for this",
		Prompt: "do stuff",
		Status: string(models.TaskStatusReady),
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Agent != "codex" {
		t.Fatalf("Agent on created: got %q, want %q", created.Agent, "codex")
	}

	// Reload from disk to verify YAML round-trip.
	loaded, err := config.LoadTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Agent != "codex" {
		t.Errorf("Agent after reload: got %q, want %q", loaded.Agent, "codex")
	}
}

func TestCreateTaskWithoutAgentLeavesFieldEmpty(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "Defaults to project",
		Prompt: "do stuff",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Agent != "" {
		t.Errorf("Agent should be empty, got %q", created.Agent)
	}
}

func TestUpdateTaskSetsAgent(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "t",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newAgent := "codex"
	updated, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: created.TaskNumber,
		Agent:      &newAgent,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Agent != "codex" {
		t.Errorf("Agent after update: got %q, want %q", updated.Agent, "codex")
	}
}

func TestUpdateTaskClearsAgentWithExplicitEmptyString(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "t",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Agent != "codex" {
		t.Fatalf("precondition: Agent not persisted on create")
	}

	empty := ""
	updated, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: created.TaskNumber,
		Agent:      &empty,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Agent != "" {
		t.Errorf("Agent should be cleared, got %q", updated.Agent)
	}

	loaded, err := config.LoadTask(projectPath, created.TaskNumber)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Agent != "" {
		t.Errorf("Agent should be cleared on disk, got %q", loaded.Agent)
	}
}

func TestUpdateTaskNilAgentLeavesUnchanged(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()
	created, err := m.CreateTask(projectPath, CreateOptions{
		Title:  "t",
		Prompt: "p",
		Status: string(models.TaskStatusReady),
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newTitle := "new title"
	updated, err := m.UpdateTask(projectPath, UpdateOptions{
		TaskNumber: created.TaskNumber,
		Title:      &newTitle,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Agent != "codex" {
		t.Errorf("Agent should stay codex, got %q", updated.Agent)
	}
}
