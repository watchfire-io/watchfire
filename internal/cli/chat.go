package cli

import (
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with project context",
	Long: `Start an interactive chat session with the current project's context.

The agent runs in chat mode with full access to the project definition
and system prompt, allowing you to ask questions and iterate on tasks.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath, err := getProjectPath()
		if err != nil {
			return err
		}
		return runAgentAttach(projectPath, "chat", 0)
	},
}
