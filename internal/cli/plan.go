package cli

import (
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Generate tasks from project definition",
	Long:  `Analyze the project definition and generate tasks using an agent.`,
	RunE:  runPlan,
}

func runPlan(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	return runAgentAttach(projectPath, "generate-tasks", 0)
}
