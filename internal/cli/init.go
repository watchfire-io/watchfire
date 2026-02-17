package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Watchfire project in the current directory",
	Long: `Initialize a new Watchfire project in the current directory.

This will:
  1. Check for git (initialize if needed)
  2. Create .watchfire/ directory structure
  3. Add .watchfire/ to .gitignore
  4. Create initial project.yaml
  5. Register project globally`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Check if already a project
	if config.ProjectExists(cwd) {
		return fmt.Errorf("already a Watchfire project")
	}

	reader := bufio.NewReader(os.Stdin)

	// Prompt for project name
	defaultName := filepath.Base(cwd)
	fmt.Printf("Project name [%s]: ", defaultName)
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultName
	}

	// Prompt for definition (optional)
	fmt.Print("Project definition (optional, press Enter to skip): ")
	definition, _ := reader.ReadString('\n')
	definition = strings.TrimSpace(definition)

	// Prompt for settings
	fmt.Println("\nProject settings:")

	autoMerge := promptYesNo(reader, "Auto-merge after task completion?", true)
	autoDeleteBranch := promptYesNo(reader, "Auto-delete worktrees after merge?", true)
	autoStartTasks := promptYesNo(reader, "Auto-start agent when task set to ready?", true)

	fmt.Print("Default branch [main]: ")
	defaultBranch, _ := reader.ReadString('\n')
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	// Create project using the project manager
	mgr := project.NewManager()
	pwe, err := mgr.CreateProject(project.CreateOptions{
		Path:             cwd,
		Name:             name,
		Definition:       definition,
		DefaultBranch:    defaultBranch,
		AutoMerge:        autoMerge,
		AutoDeleteBranch: autoDeleteBranch,
		AutoStartTasks:   autoStartTasks,
	})
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	fmt.Printf("\nProject '%s' initialized successfully!\n", pwe.Project.Name)
	fmt.Printf("  ID: %s\n", pwe.Project.ProjectID)
	fmt.Printf("  Path: %s\n", cwd)
	fmt.Println("\nNext steps:")
	fmt.Println("  - Run 'watchfire task add' to create your first task")
	fmt.Println("  - Run 'watchfire task list' to see all tasks")

	return nil
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultVal bool) bool {
	defaultStr := "Y/n"
	if !defaultVal {
		defaultStr = "y/N"
	}

	fmt.Printf("  %s [%s]: ", prompt, defaultStr)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "" {
		return defaultVal
	}
	return response == "y" || response == "yes"
}
