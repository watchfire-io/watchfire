package cli

import (
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:     "generate",
	Aliases: []string{"gen"},
	Short:   "Generate project definition",
	Long:    `Generate a project definition using an agent to analyze the codebase.`,
	RunE:    runGenerate,
}

func runGenerate(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	return runAgentAttach(projectPath, "generate-definition", 0)
}
