// Package config handles configuration loading, saving, and path management.
package config

import (
	"os"
	"path/filepath"
)

const (
	// GlobalDirName is the name of the global Watchfire directory.
	GlobalDirName = ".watchfire"

	// ProjectDirName is the name of the per-project Watchfire directory.
	ProjectDirName = ".watchfire"

	// TasksDirName is the name of the tasks directory within a project.
	TasksDirName = "tasks"

	// WorktreesDirName is the name of the worktrees directory within a project.
	WorktreesDirName = "worktrees"

	// LogsDirName is the name of the logs directory.
	LogsDirName = "logs"
)

// File names
const (
	DaemonFileName   = "daemon.yaml"
	ProjectsFileName = "projects.yaml"
	SettingsFileName = "settings.yaml"
	ProjectFileName  = "project.yaml"
)

// GlobalDir returns the path to the global Watchfire directory (~/.watchfire/).
func GlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, GlobalDirName), nil
}

// GlobalDaemonFile returns the path to the daemon.yaml file.
func GlobalDaemonFile() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DaemonFileName), nil
}

// GlobalProjectsFile returns the path to the projects.yaml file.
func GlobalProjectsFile() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ProjectsFileName), nil
}

// GlobalSettingsFile returns the path to the settings.yaml file.
func GlobalSettingsFile() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SettingsFileName), nil
}

// GlobalLogsDir returns the path to the logs directory.
func GlobalLogsDir() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, LogsDirName), nil
}

// ProjectDir returns the path to a project's .watchfire/ directory.
func ProjectDir(projectPath string) string {
	return filepath.Join(projectPath, ProjectDirName)
}

// ProjectFile returns the path to a project's project.yaml file.
func ProjectFile(projectPath string) string {
	return filepath.Join(ProjectDir(projectPath), ProjectFileName)
}

// ProjectTasksDir returns the path to a project's tasks directory.
func ProjectTasksDir(projectPath string) string {
	return filepath.Join(ProjectDir(projectPath), TasksDirName)
}

// ProjectWorktreesDir returns the path to a project's worktrees directory.
func ProjectWorktreesDir(projectPath string) string {
	return filepath.Join(ProjectDir(projectPath), WorktreesDirName)
}

// TaskFile returns the path to a specific task file.
func TaskFile(projectPath string, taskNumber int) string {
	return filepath.Join(ProjectTasksDir(projectPath), TaskFileName(taskNumber))
}

// TaskFileName returns the filename for a task number (e.g., "0001.yaml").
func TaskFileName(taskNumber int) string {
	return filepath.Join("", formatTaskNumber(taskNumber)+".yaml")
}

// formatTaskNumber formats a task number as a 4-digit string.
func formatTaskNumber(n int) string {
	return filepath.Join("", pad(n, 4))
}

// pad pads a number with leading zeros to the specified width.
func pad(n, width int) string {
	s := ""
	for i := 0; i < width; i++ {
		s = string('0'+(n%10)) + s
		n /= 10
	}
	return s
}

// EnsureGlobalDir creates the global Watchfire directory if it doesn't exist.
func EnsureGlobalDir() error {
	dir, err := GlobalDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// EnsureGlobalLogsDir creates the global logs directory if it doesn't exist.
func EnsureGlobalLogsDir() error {
	dir, err := GlobalLogsDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// EnsureProjectDir creates the project's .watchfire/ directory structure.
func EnsureProjectDir(projectPath string) error {
	// Create main .watchfire directory
	if err := os.MkdirAll(ProjectDir(projectPath), 0755); err != nil {
		return err
	}
	// Create tasks directory
	if err := os.MkdirAll(ProjectTasksDir(projectPath), 0755); err != nil {
		return err
	}
	// Create worktrees directory
	return os.MkdirAll(ProjectWorktreesDir(projectPath), 0755)
}

// ProjectExists checks if a project's .watchfire/ directory exists.
func ProjectExists(projectPath string) bool {
	_, err := os.Stat(ProjectDir(projectPath))
	return err == nil
}
