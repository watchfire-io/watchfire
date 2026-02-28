package server

import (
	"fmt"

	"github.com/watchfire-io/watchfire/internal/config"
)

// getProjectPath resolves a project ID to its filesystem path and index entry.
func getProjectPath(projectID string) (string, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return "", err
	}
	entry := index.FindProject(projectID)
	if entry == nil {
		return "", fmt.Errorf("project not found: %s", projectID)
	}
	return entry.Path, nil
}
