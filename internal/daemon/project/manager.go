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

// ProjectWithEntry pairs a loaded project with its index entry data (path, position).
type ProjectWithEntry struct {
	Project  *models.Project
	Path     string
	Position int
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

// ListProjects returns all registered projects with their index entry data.
func (m *Manager) ListProjects() ([]ProjectWithEntry, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}

	var results []ProjectWithEntry
	for _, entry := range index.Projects {
		project, err := config.LoadProject(entry.Path)
		if err != nil {
			continue // Skip projects that can't be loaded
		}
		if project != nil {
			results = append(results, ProjectWithEntry{
				Project:  project,
				Path:     entry.Path,
				Position: entry.Position,
			})
		}
	}

	return results, nil
}

// GetProject retrieves a project by ID with its index entry data.
func (m *Manager) GetProject(projectID string) (ProjectWithEntry, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return ProjectWithEntry{}, err
	}

	entry := index.FindProject(projectID)
	if entry == nil {
		return ProjectWithEntry{}, fmt.Errorf("project not found: %s", projectID)
	}

	project, err := config.LoadProject(entry.Path)
	if err != nil {
		return ProjectWithEntry{}, err
	}
	if project == nil {
		return ProjectWithEntry{}, fmt.Errorf("project file not found: %s", entry.Path)
	}

	return ProjectWithEntry{
		Project:  project,
		Path:     entry.Path,
		Position: entry.Position,
	}, nil
}

// CreateProject initializes a new project, or imports an existing one if
// the folder already contains a .watchfire/ directory.
func (m *Manager) CreateProject(opts CreateOptions) (ProjectWithEntry, error) {
	// If already a project, register and return it (import)
	if config.ProjectExists(opts.Path) {
		if err := config.EnsureProjectRegistered(opts.Path); err != nil {
			return ProjectWithEntry{}, fmt.Errorf("failed to import project: %w", err)
		}
		return m.getProjectByPath(opts.Path)
	}

	// Ensure directory exists
	if err := os.MkdirAll(opts.Path, 0o755); err != nil {
		return ProjectWithEntry{}, fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if git repo, initialize if not
	if err := ensureGitRepo(opts.Path); err != nil {
		return ProjectWithEntry{}, err
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
	p := models.NewProject(projectID, name, opts.Path)
	p.Definition = opts.Definition
	p.DefaultBranch = defaultBranch
	p.AutoMerge = opts.AutoMerge
	p.AutoDeleteBranch = opts.AutoDeleteBranch
	p.AutoStartTasks = opts.AutoStartTasks

	// Create .watchfire directory structure
	if err := config.EnsureProjectDir(opts.Path); err != nil {
		return ProjectWithEntry{}, err
	}

	// Save project file
	if err := config.SaveProject(opts.Path, p); err != nil {
		return ProjectWithEntry{}, err
	}

	// Add .watchfire to .gitignore
	if err := addToGitignore(opts.Path); err != nil {
		return ProjectWithEntry{}, err
	}

	// Commit .gitignore change (non-fatal, ignore error)
	_ = commitGitignore(opts.Path)

	// Register project in global index
	if err := config.RegisterProject(projectID, name, opts.Path); err != nil {
		return ProjectWithEntry{}, err
	}

	// Reload index to get the assigned position
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return ProjectWithEntry{}, err
	}
	entry := index.FindProject(projectID)
	position := 0
	if entry != nil {
		position = entry.Position
	}

	return ProjectWithEntry{
		Project:  p,
		Path:     opts.Path,
		Position: position,
	}, nil
}

// UpdateProject updates a project's settings.
func (m *Manager) UpdateProject(opts UpdateOptions) (ProjectWithEntry, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return ProjectWithEntry{}, err
	}

	entry := index.FindProject(opts.ProjectID)
	if entry == nil {
		return ProjectWithEntry{}, fmt.Errorf("project not found: %s", opts.ProjectID)
	}

	p, err := config.LoadProject(entry.Path)
	if err != nil {
		return ProjectWithEntry{}, err
	}
	if p == nil {
		return ProjectWithEntry{}, fmt.Errorf("project file not found: %s", entry.Path)
	}

	// Apply updates
	if opts.Name != nil {
		p.Name = *opts.Name
		entry.Name = *opts.Name
	}
	if opts.Color != nil {
		p.Color = *opts.Color
	}
	if opts.DefaultBranch != nil {
		p.DefaultBranch = *opts.DefaultBranch
	}
	if opts.DefaultAgent != nil {
		p.DefaultAgent = *opts.DefaultAgent
	}
	if opts.AutoMerge != nil {
		p.AutoMerge = *opts.AutoMerge
	}
	if opts.AutoDeleteBranch != nil {
		p.AutoDeleteBranch = *opts.AutoDeleteBranch
	}
	if opts.AutoStartTasks != nil {
		p.AutoStartTasks = *opts.AutoStartTasks
	}
	if opts.Definition != nil {
		p.Definition = *opts.Definition
	}

	p.UpdatedAt = time.Now().UTC()

	// Save project file
	if err := config.SaveProject(entry.Path, p); err != nil {
		return ProjectWithEntry{}, err
	}

	// Update global index if name changed
	if opts.Name != nil {
		if err := config.SaveProjectsIndex(index); err != nil {
			return ProjectWithEntry{}, err
		}
	}

	return ProjectWithEntry{
		Project:  p,
		Path:     entry.Path,
		Position: entry.Position,
	}, nil
}

// getProjectByPath loads a project and its index entry by filesystem path.
func (m *Manager) getProjectByPath(projectPath string) (ProjectWithEntry, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return ProjectWithEntry{}, err
	}

	entry := index.FindProjectByPath(projectPath)
	if entry == nil {
		return ProjectWithEntry{}, fmt.Errorf("project not found at path: %s", projectPath)
	}

	p, err := config.LoadProject(entry.Path)
	if err != nil {
		return ProjectWithEntry{}, err
	}
	if p == nil {
		return ProjectWithEntry{}, fmt.Errorf("project file not found: %s", entry.Path)
	}

	return ProjectWithEntry{
		Project:  p,
		Path:     entry.Path,
		Position: entry.Position,
	}, nil
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
