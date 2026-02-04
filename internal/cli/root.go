// Package cli implements the watchfire CLI commands.
package cli

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "watchfire",
	Short: "Orchestrate coding agent sessions based on specs",
	Long: `Watchfire orchestrates coding agent sessions (starting with Claude Code)
based on task files. It manages multiple projects in parallel, with one
active task per project.`,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(taskCmd)
}
