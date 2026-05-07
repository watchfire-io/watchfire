package config

import (
	"fmt"
	"os"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// LoadProjectsIndex loads the projects index from ~/.watchfire/projects.yaml.
// If the file doesn't exist, returns an empty index.
func LoadProjectsIndex() (*models.ProjectsIndex, error) {
	path, err := GlobalProjectsFile()
	if err != nil {
		return nil, err
	}
	return LoadYAMLOrDefault(path, models.NewProjectsIndex)
}

// SaveProjectsIndex saves the projects index to ~/.watchfire/projects.yaml.
func SaveProjectsIndex(index *models.ProjectsIndex) error {
	if err := EnsureGlobalDir(); err != nil {
		return err
	}

	path, err := GlobalProjectsFile()
	if err != nil {
		return err
	}
	return SaveYAML(path, index)
}

// LoadProject loads a project from its .watchfire/project.yaml file.
//
// A project.yaml that decodes to a zero/near-zero struct (Version == 0 or
// empty ProjectID) is treated as corrupt: yaml.Unmarshal silently succeeds
// on empty content, and we refuse to roll forward with that — callers must
// surface the error rather than overwrite good metadata with zeros.
func LoadProject(projectPath string) (*models.Project, error) {
	path := ProjectFile(projectPath)

	if !FileExists(path) {
		return nil, nil
	}

	var project models.Project
	if err := LoadYAML(path, &project); err != nil {
		return nil, err
	}

	if project.Version == 0 || project.ProjectID == "" {
		return nil, fmt.Errorf("corrupt project.yaml at %s: version=%d project_id=%q", path, project.Version, project.ProjectID)
	}

	// Load secrets instructions from secrets/instructions.md
	secretsPath := ProjectSecretsInstructionsFile(projectPath)
	if data, err := os.ReadFile(secretsPath); err == nil {
		project.SecretsInstructions = string(data)
	}

	return &project, nil
}

// SaveProject saves a project to its .watchfire/project.yaml file.
func SaveProject(projectPath string, project *models.Project) error {
	if err := EnsureProjectDir(projectPath); err != nil {
		return err
	}
	if err := SaveYAML(ProjectFile(projectPath), project); err != nil {
		return err
	}

	// Write secrets instructions to secrets/instructions.md if set
	if project.SecretsInstructions != "" {
		secretsPath := ProjectSecretsInstructionsFile(projectPath)
		if err := os.WriteFile(secretsPath, []byte(project.SecretsInstructions), 0o644); err != nil {
			return fmt.Errorf("failed to write secrets instructions: %w", err)
		}
	}

	return nil
}

// RegisterProject adds a project to the global index.
func RegisterProject(projectID, name, path string) error {
	index, err := LoadProjectsIndex()
	if err != nil {
		return err
	}

	// Check if already registered by ID
	existing := index.FindProject(projectID)
	if existing != nil {
		existing.Path = path
		existing.Name = name
		return SaveProjectsIndex(index)
	}

	// Remove stale entry for the same path (different project ID)
	if stale := index.FindProjectByPath(path); stale != nil {
		index.RemoveProject(stale.ProjectID)
	}

	// Add new entry
	index.AddProject(models.ProjectEntry{
		ProjectID: projectID,
		Name:      name,
		Path:      path,
	})

	return SaveProjectsIndex(index)
}

// EnsureProjectRegistered loads the local project.yaml and ensures the project
// is registered in the global index. If the project was archived, it reactivates it.
// This enables self-healing: if the global index is deleted or the project is moved,
// running any project-scoped CLI command will re-register it automatically.
func EnsureProjectRegistered(projectPath string) error {
	project, err := LoadProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("corrupt project: no project.yaml found in %s", projectPath)
	}

	// Reactivate archived projects on contact
	if project.Status == "archived" {
		project.Status = "active"
		project.UpdatedAt = time.Now().UTC()
		if err := SaveProject(projectPath, project); err != nil {
			return fmt.Errorf("failed to reactivate project: %w", err)
		}
	}

	// Register (or update) in global index
	return RegisterProject(project.ProjectID, project.Name, projectPath)
}

// UnregisterProject removes a project from the global index.
func UnregisterProject(projectID string) error {
	index, err := LoadProjectsIndex()
	if err != nil {
		return err
	}

	if !index.RemoveProject(projectID) {
		return nil // Not found, nothing to do
	}

	return SaveProjectsIndex(index)
}
