// Package cli implements the watchfire CLI commands.
package cli

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "watchfire",
	Short: "Orchestrate coding agent sessions based on specs",
	Long: `Watchfire orchestrates coding agent sessions based on task files.
It manages multiple projects in parallel, with one active task per project.`,
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
	rootCmd.AddCommand(versionCmd)
}
