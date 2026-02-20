package cli

import (
	"github.com/spf13/cobra"
)

var wildfireCmd = &cobra.Command{
	Use:     "wildfire",
	Aliases: []string{"fire"},
	Short:   "Run all ready tasks in wildfire mode",
	Long:    `Run all ready tasks in wildfire mode with analysis and refinement phases.`,
	RunE:    runWildfire,
}

func runWildfire(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	return runAgentAttach(projectPath, "wildfire", 0)
}
