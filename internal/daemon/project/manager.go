// Package project handles project management for the daemon.
package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// Manager handles project operations.
type Manager struct{}

// NewManager creates a new project manager.
func NewManager() *Manager {
	return &Manager{}
}

// CreateOptions contains options for creating a project.
type CreateOptions struct {
	Path             string
	Name             string
	Definition       string
	DefaultBranch    string
	AutoMerge        bool
	AutoDeleteBranch bool
	AutoStartTasks   bool
}

// UpdateOptions contains options for updating a project.
type UpdateOptions struct {
	ProjectID        string
	Name             *string
	Color            *string
	DefaultBranch    *string
	DefaultAgent     *string
	AutoMerge        *bool
	AutoDeleteBranch *bool
	AutoStartTasks   *bool
	Definition       *string
}

// ListProjects returns all registered projects.
func (m *Manager) ListProjects() ([]*models.Project, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}

	var projects []*models.Project
	for _, entry := range index.Projects {
		project, err := config.LoadProject(entry.Path)
		if err != nil {
			continue // Skip projects that can't be loaded
		}
		if project != nil {
			projects = append(projects, project)
		}
	}

	return projects, nil
}

// GetProject retrieves a project by ID.
func (m *Manager) GetProject(projectID string) (*models.Project, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}

	entry := index.FindProject(projectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", projectID)
	}

	project, err := config.LoadProject(entry.Path)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project file not found: %s", entry.Path)
	}

	return project, nil
}

// CreateProject initializes a new project.
func (m *Manager) CreateProject(opts CreateOptions) (*models.Project, error) {
	// Check if already a project
	if config.ProjectExists(opts.Path) {
		return nil, fmt.Errorf("already a Watchfire project: %s", opts.Path)
	}

	// Ensure directory exists
	if err := os.MkdirAll(opts.Path, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if git repo, initialize if not
	if err := ensureGitRepo(opts.Path); err != nil {
		return nil, err
	}

	// Generate project ID
	projectID := uuid.New().String()

	// Use folder name if name not provided
	name := opts.Name
	if name == "" {
		name = filepath.Base(opts.Path)
	}

	// Set defaults
	defaultBranch := opts.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	// Create project
	project := models.NewProject(projectID, name, opts.Path)
	project.Definition = opts.Definition
	project.DefaultBranch = defaultBranch
	project.AutoMerge = opts.AutoMerge
	project.AutoDeleteBranch = opts.AutoDeleteBranch
	project.AutoStartTasks = opts.AutoStartTasks

	// Create .watchfire directory structure
	if err := config.EnsureProjectDir(opts.Path); err != nil {
		return nil, err
	}

	// Save project file
	if err := config.SaveProject(opts.Path, project); err != nil {
		return nil, err
	}

	// Add .watchfire to .gitignore
	if err := addToGitignore(opts.Path); err != nil {
		return nil, err
	}

	// Commit .gitignore change (non-fatal, ignore error)
	_ = commitGitignore(opts.Path)

	// Register project in global index
	if err := config.RegisterProject(projectID, name, opts.Path); err != nil {
		return nil, err
	}

	return project, nil
}

// UpdateProject updates a project's settings.
func (m *Manager) UpdateProject(opts UpdateOptions) (*models.Project, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}

	entry := index.FindProject(opts.ProjectID)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", opts.ProjectID)
	}

	project, err := config.LoadProject(entry.Path)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project file not found: %s", entry.Path)
	}

	// Apply updates
	if opts.Name != nil {
		project.Name = *opts.Name
		entry.Name = *opts.Name
	}
	if opts.Color != nil {
		project.Color = *opts.Color
	}
	if opts.DefaultBranch != nil {
		project.DefaultBranch = *opts.DefaultBranch
	}
	if opts.DefaultAgent != nil {
		project.DefaultAgent = *opts.DefaultAgent
	}
	if opts.AutoMerge != nil {
		project.AutoMerge = *opts.AutoMerge
	}
	if opts.AutoDeleteBranch != nil {
		project.AutoDeleteBranch = *opts.AutoDeleteBranch
	}
	if opts.AutoStartTasks != nil {
		project.AutoStartTasks = *opts.AutoStartTasks
	}
	if opts.Definition != nil {
		project.Definition = *opts.Definition
	}

	project.UpdatedAt = time.Now().UTC()

	// Save project file
	if err := config.SaveProject(entry.Path, project); err != nil {
		return nil, err
	}

	// Update global index if name changed
	if opts.Name != nil {
		if err := config.SaveProjectsIndex(index); err != nil {
			return nil, err
		}
	}

	return project, nil
}

// DeleteProject removes a project from the registry.
// Note: This does NOT delete the .watchfire directory, only unregisters the project.
func (m *Manager) DeleteProject(projectID string) error {
	return config.UnregisterProject(projectID)
}

// ensureGitRepo checks if the directory is a git repo, initializes if not.
func ensureGitRepo(path string) error {
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil // Already a git repo
	}

	cmd := exec.CommandContext(context.TODO(), "git", "init")
	cmd.Dir = path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to initialize git: %w", err)
	}
	return nil
}

// addToGitignore adds .watchfire/ to the project's .gitignore.
func addToGitignore(projectPath string) error {
	gitignorePath := filepath.Join(projectPath, ".gitignore")

	// Read existing content
	var content []byte
	if _, err := os.Stat(gitignorePath); err == nil {
		content, err = os.ReadFile(gitignorePath)
		if err != nil {
			return fmt.Errorf("failed to read .gitignore: %w", err)
		}
	}

	// Check if already present
	if contains(string(content), ".watchfire/") {
		return nil
	}

	// Append entry
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Add newline if file doesn't end with one
	if len(content) > 0 && content[len(content)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	if _, err := f.WriteString(".watchfire/\n"); err != nil {
		return fmt.Errorf("failed to write to .gitignore: %w", err)
	}

	return nil
}

// commitGitignore commits the .gitignore change.
func commitGitignore(projectPath string) error {
	// Add .gitignore
	cmd := exec.CommandContext(context.TODO(), "git", "add", ".gitignore")
	cmd.Dir = projectPath
	if err := cmd.Run(); err != nil {
		return err
	}

	// Commit
	cmd = exec.CommandContext(context.TODO(), "git", "commit", "-m", "chore: add .watchfire to gitignore")
	cmd.Dir = projectPath
	return cmd.Run()
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || s != "" && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
