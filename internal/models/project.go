// Package models contains shared data structures used across the application.
package models

import (
	"math/rand/v2"
	"runtime"
	"time"
)

// ProjectColors is the fixed palette of colors assigned randomly to new projects.
var ProjectColors = []string{
	"#ef4444", // red
	"#f97316", // orange
	"#eab308", // yellow
	"#22c55e", // green
	"#14b8a6", // teal
	"#06b6d4", // cyan
	"#3b82f6", // blue
	"#8b5cf6", // violet
	"#a855f7", // purple
	"#ec4899", // pink
}

// RandomColor returns a random color from the project palette.
func RandomColor() string {
	return ProjectColors[rand.IntN(len(ProjectColors))]
}

// DefaultSandboxMode returns the platform-appropriate sandbox mode.
// macOS uses "sandbox-exec"; other platforms have no sandbox support yet.
func DefaultSandboxMode() string {
	if runtime.GOOS == "darwin" {
		return "sandbox-exec"
	}
	return "none"
}

// Project represents a Watchfire project configuration.
// This corresponds to the project.yaml file in .watchfire/ directory.
type Project struct {
	Version             int       `yaml:"version"`
	ProjectID           string    `yaml:"project_id"`
	Name                string    `yaml:"name"`
	Status              string    `yaml:"status"` // "active" | "archived"
	Color               string    `yaml:"color"`  // Hex color for GUI
	DefaultAgent        string    `yaml:"default_agent"`
	Sandbox             string    `yaml:"sandbox"`
	AutoMerge           bool      `yaml:"auto_merge"`
	AutoDeleteBranch    bool      `yaml:"auto_delete_branch"`
	AutoStartTasks      bool      `yaml:"auto_start_tasks"`
	Definition          string    `yaml:"definition"`
	SecretsInstructions string    `yaml:"-"` // Loaded from secrets/instructions.md, not stored in project.yaml
	CreatedAt           time.Time `yaml:"created_at"`
	UpdatedAt           time.Time `yaml:"updated_at"`
	NextTaskNumber      int       `yaml:"next_task_number"`
}

// ProjectEntry represents an entry in the global projects.yaml index.
type ProjectEntry struct {
	ProjectID string `yaml:"project_id"`
	Name      string `yaml:"name"`
	Path      string `yaml:"path"`
	Position  int    `yaml:"position"`
}

// ProjectsIndex represents the global projects.yaml file.
type ProjectsIndex struct {
	Version  int            `yaml:"version"`
	Projects []ProjectEntry `yaml:"projects"`
}

// NewProject creates a new project with default values.
func NewProject(id, name, path string) *Project {
	now := time.Now().UTC()
	return &Project{
		Version:          1,
		ProjectID:        id,
		Name:             name,
		Status:           "active",
		Color:            RandomColor(),
		DefaultAgent:     "claude-code",
		Sandbox:          DefaultSandboxMode(),
		AutoMerge:        true,
		AutoDeleteBranch: true,
		AutoStartTasks:   true,
		Definition:       "",
		CreatedAt:        now,
		UpdatedAt:        now,
		NextTaskNumber:   1,
	}
}

// NewProjectsIndex creates a new empty projects index.
func NewProjectsIndex() *ProjectsIndex {
	return &ProjectsIndex{
		Version:  1,
		Projects: []ProjectEntry{},
	}
}

// AddProject adds a project to the index.
func (idx *ProjectsIndex) AddProject(entry ProjectEntry) {
	// Set position to end of list
	entry.Position = len(idx.Projects) + 1
	idx.Projects = append(idx.Projects, entry)
}

// RemoveProject removes a project from the index by ID.
func (idx *ProjectsIndex) RemoveProject(projectID string) bool {
	for i, p := range idx.Projects {
		if p.ProjectID == projectID {
			idx.Projects = append(idx.Projects[:i], idx.Projects[i+1:]...)
			// Reorder positions
			for j := i; j < len(idx.Projects); j++ {
				idx.Projects[j].Position = j + 1
			}
			return true
		}
	}
	return false
}

// FindProject finds a project by ID in the index.
func (idx *ProjectsIndex) FindProject(projectID string) *ProjectEntry {
	for i := range idx.Projects {
		if idx.Projects[i].ProjectID == projectID {
			return &idx.Projects[i]
		}
	}
	return nil
}

// FindProjectByPath finds a project by path in the index.
func (idx *ProjectsIndex) FindProjectByPath(path string) *ProjectEntry {
	for i := range idx.Projects {
		if idx.Projects[i].Path == path {
			return &idx.Projects[i]
		}
	}
	return nil
}
