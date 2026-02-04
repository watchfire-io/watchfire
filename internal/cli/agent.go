package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/daemon/task"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage coding agents",
	Long:  `Manage coding agent sessions for the current project.`,
}

var agentStartCmd = &cobra.Command{
	Use:   "start [task-number]",
	Short: "Start an agent session",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runAgentStart,
}

var agentGenerateCmd = &cobra.Command{
	Use:     "generate",
	Aliases: []string{"gen"},
	Short:   "Generate project artifacts",
	Long:    `Generate project definition or tasks using an agent.`,
}

var agentGenerateDefCmd = &cobra.Command{
	Use:     "definition",
	Aliases: []string{"def"},
	Short:   "Generate project definition",
	RunE:    runAgentGenerateDef,
}

var agentGenerateTasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Generate tasks from project definition",
	RunE:  runAgentGenerateTasks,
}

var agentWildfireCmd = &cobra.Command{
	Use:   "wildfire",
	Short: "Run all ready tasks in sequence",
	RunE:  runAgentWildfire,
}

func init() {
	agentGenerateCmd.AddCommand(agentGenerateDefCmd)
	agentGenerateCmd.AddCommand(agentGenerateTasksCmd)

	agentCmd.AddCommand(agentGenerateCmd)
	agentCmd.AddCommand(agentStartCmd)
	agentCmd.AddCommand(agentWildfireCmd)
}

func runAgentStart(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	if err := EnsureDaemon(); err != nil {
		return err
	}

	// Validate task exists if task number given
	if len(args) > 0 {
		taskNum, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid task number: %s", args[0])
		}

		mgr := task.NewManager()
		_, err = mgr.GetTask(projectPath, taskNum)
		if err != nil {
			return err
		}

		fmt.Printf("Task #%04d validated.\n", taskNum)
	}

	fmt.Println("Agent spawning not yet implemented.")
	return nil
}

func runAgentGenerateDef(cmd *cobra.Command, args []string) error {
	if _, err := getProjectPath(); err != nil {
		return err
	}

	if err := EnsureDaemon(); err != nil {
		return err
	}

	fmt.Println("Definition generation not yet implemented.")
	return nil
}

func runAgentGenerateTasks(cmd *cobra.Command, args []string) error {
	if _, err := getProjectPath(); err != nil {
		return err
	}

	if err := EnsureDaemon(); err != nil {
		return err
	}

	fmt.Println("Task generation not yet implemented.")
	return nil
}

func runAgentWildfire(cmd *cobra.Command, args []string) error {
	if _, err := getProjectPath(); err != nil {
		return err
	}

	if err := EnsureDaemon(); err != nil {
		return err
	}

	fmt.Println("Wildfire mode not yet implemented.")
	return nil
}
