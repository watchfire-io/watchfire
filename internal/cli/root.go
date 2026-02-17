// Package cli implements the watchfire CLI commands.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/tui"
)

var rootCmd = &cobra.Command{
	Use:   "watchfire",
	Short: "Orchestrate coding agent sessions based on specs",
	Long: `Watchfire orchestrates coding agent sessions based on task files.
It manages multiple projects in parallel, with one active task per project.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// No subcommand → launch TUI
		projectPath, err := getProjectPath()
		if err != nil {
			return err
		}

		// Ensure daemon is running
		if err := EnsureDaemon(); err != nil {
			return err
		}

		return tui.Run(projectPath)
	},
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add subcommands (alphabetical)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(definitionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(settingsCmd)
	rootCmd.AddCommand(taskCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(versionCmd)
}
