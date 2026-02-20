package cli

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
)

var configureCmd = &cobra.Command{
	Use:     "configure",
	Aliases: []string{"config"},
	Short:   "Configure project settings",
	Long: `Configure project settings interactively.

This allows you to modify:
  - Project name
  - Project color (hex)
  - Default branch
  - Automation settings (auto-merge, auto-delete, auto-start)

Press Enter to keep the current value for any setting.`,
	RunE: runConfigure,
}

func runConfigure(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	project, err := config.LoadProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("failed to load project configuration")
	}

	reader := bufio.NewReader(os.Stdin)
	changed := false
	originalName := project.Name

	// Project name
	fmt.Printf("Project name [%s]: ", project.Name)
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)
	if name != "" && name != project.Name {
		project.Name = name
		changed = true
	}

	// Project color
	fmt.Printf("Project color (hex) [%s]: ", project.Color)
	color, _ := reader.ReadString('\n')
	color = strings.TrimSpace(color)
	if color != "" {
		if !isValidHexColor(color) {
			return fmt.Errorf("invalid hex color: %s (expected format: #RRGGBB or #RGB)", color)
		}
		if color != project.Color {
			project.Color = color
			changed = true
		}
	}

	// Default branch
	fmt.Printf("Default branch [%s]: ", project.DefaultBranch)
	branch, _ := reader.ReadString('\n')
	branch = strings.TrimSpace(branch)
	if branch != "" && branch != project.DefaultBranch {
		project.DefaultBranch = branch
		changed = true
	}

	// Automation settings
	fmt.Println("\nAutomation settings:")

	newAutoMerge := promptYesNoWithCurrent(reader, "Auto-merge after task completion?", project.AutoMerge)
	if newAutoMerge != project.AutoMerge {
		project.AutoMerge = newAutoMerge
		changed = true
	}

	newAutoDelete := promptYesNoWithCurrent(reader, "Auto-delete worktrees after merge?", project.AutoDeleteBranch)
	if newAutoDelete != project.AutoDeleteBranch {
		project.AutoDeleteBranch = newAutoDelete
		changed = true
	}

	newAutoStart := promptYesNoWithCurrent(reader, "Auto-start agent when task set to ready?", project.AutoStartTasks)
	if newAutoStart != project.AutoStartTasks {
		project.AutoStartTasks = newAutoStart
		changed = true
	}

	if !changed {
		fmt.Println("\nNo changes made.")
		return nil
	}

	// Save project
	project.UpdatedAt = time.Now().UTC()
	if err := config.SaveProject(projectPath, project); err != nil {
		return fmt.Errorf("failed to save project: %w", err)
	}

	// Update global index if name changed
	if project.Name != originalName {
		if err := config.RegisterProject(project.ProjectID, project.Name, projectPath); err != nil {
			return fmt.Errorf("failed to update project index: %w", err)
		}
	}

	fmt.Println("\nProject settings updated.")
	return nil
}

// promptYesNoWithCurrent prompts for a yes/no value showing the current value.
func promptYesNoWithCurrent(reader *bufio.Reader, prompt string, current bool) bool {
	currentStr := "no"
	if current {
		currentStr = "yes"
	}

	fmt.Printf("  %s [%s]: ", prompt, currentStr)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "" {
		return current
	}
	return response == "y" || response == "yes"
}

// isValidHexColor validates a hex color string.
func isValidHexColor(color string) bool {
	// Match #RGB or #RRGGBB format
	match, _ := regexp.MatchString(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`, color)
	return match
}
