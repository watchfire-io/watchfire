package main

import (
	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/cli"
	"github.com/watchfire-io/watchfire/internal/mcpserver"
)

var mcpReadOnly bool

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Expose Watchfire to MCP clients",
	Long: `Expose Watchfire as an MCP (Model Context Protocol) server, so external
coding agents can create tasks, launch sandboxed agent runs, and inspect
the results.`,
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the Watchfire MCP server over stdio",
	Long: `Serve the Watchfire MCP server over stdio until the client closes the pipe.

Intended to be spawned by an MCP client (Claude Code, Codex, Gemini CLI, …),
not run interactively. Auto-starts the Watchfire daemon if it is not running.
stdout carries the MCP transport; all logs go to stderr.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpserver.Serve(cmd.Context(), mcpserver.Options{
			ReadOnly: mcpReadOnly,
			Version:  buildinfo.Version,
		})
	},
}

func init() {
	mcpServeCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false,
		"Serve only observation tools (no task creation or agent control)")
	mcpCmd.AddCommand(mcpServeCmd)
	cli.AddCommand(mcpCmd)
}
