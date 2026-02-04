package config

import (
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
func LoadProject(projectPath string) (*models.Project, error) {
	path := ProjectFile(projectPath)

	if !FileExists(path) {
		return nil, nil
	}

	var project models.Project
	if err := LoadYAML(path, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// SaveProject saves a project to its .watchfire/project.yaml file.
func SaveProject(projectPath string, project *models.Project) error {
	if err := EnsureProjectDir(projectPath); err != nil {
		return err
	}
	return SaveYAML(ProjectFile(projectPath), project)
}

// RegisterProject adds a project to the global index.
func RegisterProject(projectID, name, path string) error {
	index, err := LoadProjectsIndex()
	if err != nil {
		return err
	}

	// Check if already registered
	existing := index.FindProject(projectID)
	if existing != nil {
		// Update path if changed
		existing.Path = path
		existing.Name = name
		return SaveProjectsIndex(index)
	}

	// Add new entry
	index.AddProject(models.ProjectEntry{
		ProjectID: projectID,
		Name:      name,
		Path:      path,
	})

	return SaveProjectsIndex(index)
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
