// Package models contains shared data structures used across the application.
package models

import (
	"math/rand/v2"
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

// ProjectNotifications holds per-project notification preferences.
//
// `Muted` is the master per-project mute (shipped in v4.0 Beacon). When true,
// the project never emits notifications regardless of any other field.
//
// The remaining fields, added in v6.0 (#0091), let a project override the
// global per-event toggles, per-event sounds, and quiet-hours window. They
// only take effect when `OverrideEvents` (events) or
// `QuietHoursOverride != nil` (quiet hours) is set, so a project.yaml that
// pre-dates v6 and only carries `muted` continues to inherit globals as
// before. All new fields are `omitempty` so the on-disk shape stays compact.
type ProjectNotifications struct {
	Muted              bool                        `yaml:"muted"`
	OverrideEvents     bool                        `yaml:"override_events,omitempty"`
	Events             map[string]ProjectEventPref `yaml:"events,omitempty"`
	QuietHoursOverride *QuietHoursConfig           `yaml:"quiet_hours_override,omitempty"`
}

// ProjectEventPref is a single per-event override row. Sound is the empty
// string when the project wants to inherit the global sound choice.
type ProjectEventPref struct {
	Enabled bool   `yaml:"enabled"`
	Sound   string `yaml:"sound,omitempty"`
}

// ProjectIntegrations holds per-project bindings that override the global
// integrations defaults. Each field is the empty string / false when the
// project inherits.
//
// `SlackChannel` and `DiscordGuildID` are persisted under the project's
// `integrations:` block in `project.yaml`. The GitHub auto-PR projection
// lives in the global `integrations.yaml` (`github.project_scopes`); the
// model here does not store it — the server fans the projection into the
// proto Project at marshal time so the UI sees a single coherent picture.
type ProjectIntegrations struct {
	SlackChannel   string `yaml:"slack_channel,omitempty"`
	DiscordGuildID string `yaml:"discord_guild_id,omitempty"`
}

// Project represents a Watchfire project configuration.
// This corresponds to the project.yaml file in .watchfire/ directory.
type Project struct {
	Version             int                  `yaml:"version"`
	ProjectID           string               `yaml:"project_id"`
	Name                string               `yaml:"name"`
	Status              string               `yaml:"status"` // "active" | "archived"
	Color               string               `yaml:"color"`  // Hex color for GUI
	DefaultAgent        string               `yaml:"default_agent"`
	Sandbox             string               `yaml:"sandbox"`
	AutoMerge           bool                 `yaml:"auto_merge"`
	AutoDeleteBranch    bool                 `yaml:"auto_delete_branch"`
	AutoStartTasks      bool                 `yaml:"auto_start_tasks"`
	Notifications       ProjectNotifications `yaml:"notifications"`
	Integrations        ProjectIntegrations  `yaml:"integrations,omitempty"`
	Definition          string               `yaml:"definition"`
	SecretsInstructions string               `yaml:"-"` // Loaded from secrets/instructions.md, not stored in project.yaml
	CreatedAt           time.Time            `yaml:"created_at"`
	UpdatedAt           time.Time            `yaml:"updated_at"`
	NextTaskNumber      int                  `yaml:"next_task_number"`
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
		Sandbox:          "auto",
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
