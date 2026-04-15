package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
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

	// Prompt for agent
	defaultAgent, err := promptAgent(reader)
	if err != nil {
		return err
	}

	// Prompt for settings
	fmt.Println("\nProject settings:")

	autoMerge := promptYesNo(reader, "Auto-merge after task completion?", true)
	autoDeleteBranch := promptYesNo(reader, "Auto-delete worktrees after merge?", true)
	autoStartTasks := promptYesNo(reader, "Auto-start agent when task set to ready?", true)

	// Create project using the project manager
	mgr := project.NewManager()
	pwe, err := mgr.CreateProject(project.CreateOptions{
		Path:             cwd,
		Name:             name,
		Definition:       definition,
		DefaultAgent:     defaultAgent,
		AutoMerge:        autoMerge,
		AutoDeleteBranch: autoDeleteBranch,
		AutoStartTasks:   autoStartTasks,
	})
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	fmt.Println()
	fmt.Println(styleSuccess.Render(fmt.Sprintf("Project '%s' initialized successfully!", pwe.Project.Name)))
	fmt.Printf("  %s %s\n", styleLabel.Render(fmt.Sprintf("%-5s", "ID")), styleValue.Render(pwe.Project.ProjectID))
	fmt.Printf("  %s %s\n", styleLabel.Render(fmt.Sprintf("%-5s", "Path")), styleValue.Render(cwd))
	fmt.Println("\nNext steps:")
	fmt.Printf("  - Run %s to open the TUI\n", styleCommand.Render("watchfire"))
	fmt.Printf("  - Run %s to create your first task\n", styleCommand.Render("watchfire task add"))
	fmt.Printf("  - Run %s to see all tasks\n", styleCommand.Render("watchfire task list"))

	return nil
}

// promptAgent asks the user which agent should run tasks for this project.
// The default selection follows this rule:
//   - If the global Defaults.DefaultAgent is a registered backend, pre-select it.
//   - If it is empty ("Ask per project"), force an explicit choice.
//   - Otherwise fall back to claude-code (or the first registered backend).
func promptAgent(reader *bufio.Reader) (string, error) {
	backends := backend.List()
	if len(backends) == 0 {
		// No registered backends — fall back silently.
		return "claude-code", nil
	}

	// Resolve the default index.
	defaultIdx := -1
	settings, _ := config.LoadSettings()
	globalDefault := ""
	if settings != nil {
		globalDefault = settings.Defaults.DefaultAgent
	}
	if globalDefault != "" {
		for i, b := range backends {
			if b.Name() == globalDefault {
				defaultIdx = i
				break
			}
		}
	}
	if defaultIdx == -1 && globalDefault != "" {
		// Global default set but not registered: fall back.
		for i, b := range backends {
			if b.Name() == "claude-code" {
				defaultIdx = i
				break
			}
		}
	}
	if defaultIdx == -1 && globalDefault == "" {
		// "Ask per project" — no default, force explicit choice.
	} else if defaultIdx == -1 {
		defaultIdx = 0
	}

	fmt.Println("\nWhich agent will run tasks for this project?")
	for i, b := range backends {
		marker := " "
		if i == defaultIdx {
			marker = "*"
		}
		fmt.Printf("  %s %d) %s\n", marker, i+1, b.DisplayName())
	}

	for {
		if defaultIdx >= 0 {
			fmt.Printf("Select agent [%d]: ", defaultIdx+1)
		} else {
			fmt.Print("Select agent: ")
		}
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		if response == "" {
			if defaultIdx >= 0 {
				return backends[defaultIdx].Name(), nil
			}
			fmt.Println("  Please choose an agent.")
			continue
		}

		// Accept either the index or the backend name.
		for i, b := range backends {
			if response == b.Name() || response == fmt.Sprintf("%d", i+1) {
				return b.Name(), nil
			}
		}
		fmt.Println("  Invalid selection. Try again.")
	}
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
