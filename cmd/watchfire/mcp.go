package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/cli"
	"github.com/watchfire-io/watchfire/internal/mcpserver"
	"github.com/watchfire-io/watchfire/internal/mcpserver/install"
)

var (
	mcpReadOnly     bool
	mcpInstallPrint bool
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Expose Watchfire to MCP clients",
	Long: `Expose Watchfire as an MCP (Model Context Protocol) server, so external
coding agents can create tasks, launch sandboxed agent runs, and inspect
the results.

The server is local-only by construction: its only transport is stdio,
spawned by an MCP client on this machine. It never opens a TCP socket and
is not reachable from outside the host.`,
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

var mcpInstallCmd = &cobra.Command{
	Use:   "install [client]",
	Short: "Register the Watchfire MCP server with a coding-agent client",
	Long: `Register the Watchfire MCP server ("watchfire mcp serve") with a coding-agent
client, so that agent can use Watchfire as a task factory.

Known clients: ` + strings.Join(installClientIDs(), ", ") + `.
With no argument, an interactive picker lists them plus a Custom entry.
"custom" (or --print) prints the generic JSON snippet for any other MCP
client instead of writing anything.

Installation is best-effort and idempotent: re-running updates or no-ops,
existing config files are merged (never overwritten), and when a client is
missing or its config cannot be parsed, the manual snippet is printed
instead.

The server is local-only: stdio transport spawned on this machine, no TCP
socket, not reachable from outside the host.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMcpInstall,
}

func installClientIDs() []string {
	clients := install.Clients()
	ids := make([]string, 0, len(clients))
	for _, c := range clients {
		ids = append(ids, c.ID)
	}
	return ids
}

func runMcpInstall(cmd *cobra.Command, args []string) error {
	if mcpInstallPrint || (len(args) == 1 && args[0] == "custom") {
		printCustomSnippet()
		return nil
	}

	var client install.Client
	if len(args) == 1 {
		c, ok := install.Get(args[0])
		if !ok {
			return fmt.Errorf("unknown client %q (known: %s, custom)", args[0], strings.Join(installClientIDs(), ", "))
		}
		client = c
	} else {
		c, custom, err := pickInstallClient()
		if err != nil {
			return err
		}
		if custom {
			printCustomSnippet()
			return nil
		}
		client = c
	}

	printInstallResult(client, client.Install())
	return nil
}

// pickInstallClient shows the interactive picker: the five known clients
// (annotated with their detection state) plus a Custom entry.
func pickInstallClient() (install.Client, bool, error) {
	clients := install.Clients()

	fmt.Println("Which client should connect to Watchfire's MCP server?")
	for i, c := range clients {
		note := ""
		if st := c.Status(); st.Configured {
			note = "  (already configured)"
		} else if st.Detected {
			note = "  (detected)"
		}
		if note != "" {
			fmt.Printf("  %d) %-20s%s\n", i+1, c.DisplayName, note)
		} else {
			fmt.Printf("  %d) %s\n", i+1, c.DisplayName)
		}
	}
	fmt.Printf("  %d) Custom (any MCP client)\n", len(clients)+1)

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Select client: ")
		response, err := reader.ReadString('\n')
		if err != nil {
			return install.Client{}, false, fmt.Errorf("no client selected: %w", err)
		}
		response = strings.TrimSpace(response)

		if n, err := strconv.Atoi(response); err == nil {
			if n >= 1 && n <= len(clients) {
				return clients[n-1], false, nil
			}
			if n == len(clients)+1 {
				return install.Client{}, true, nil
			}
		}
		if response == "custom" {
			return install.Client{}, true, nil
		}
		for _, c := range clients {
			if response == c.ID {
				return c, false, nil
			}
		}
		fmt.Println("  Invalid selection. Try again.")
	}
}

func printCustomSnippet() {
	fmt.Println("Add this server entry to your MCP client's configuration:")
	fmt.Println()
	fmt.Println(install.CustomSnippet())
	fmt.Println()
	fmt.Println("The server speaks MCP over stdio: the client spawns `watchfire mcp serve`")
	fmt.Println("on this machine. It never opens a TCP socket and is not reachable from")
	fmt.Println("outside the host.")
}

func printInstallResult(client install.Client, res install.Result) {
	switch res.Action {
	case install.ActionInstalled:
		fmt.Printf("Registered the Watchfire MCP server with %s.\n", client.DisplayName)
		fmt.Printf("  Config: %s\n", res.ConfigPath)
		fmt.Printf("Restart %s to pick it up.\n", client.DisplayName)
	case install.ActionUpdated:
		fmt.Printf("Updated the existing watchfire entry for %s.\n", client.DisplayName)
		fmt.Printf("  Config: %s\n", res.ConfigPath)
		fmt.Printf("Restart %s to pick it up.\n", client.DisplayName)
	case install.ActionUnchanged:
		fmt.Printf("%s is already configured — nothing to do.\n", client.DisplayName)
		fmt.Printf("  Config: %s\n", res.ConfigPath)
	case install.ActionManual:
		fmt.Printf("Could not register automatically: %s\n", res.Reason)
		fmt.Println()
		fmt.Println(res.Snippet)
	}
}

func init() {
	mcpServeCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false,
		"Serve only observation tools (no task creation or agent control)")
	mcpInstallCmd.Flags().BoolVar(&mcpInstallPrint, "print", false,
		"Print the generic JSON snippet for any MCP client instead of installing")
	mcpCmd.AddCommand(mcpServeCmd)
	mcpCmd.AddCommand(mcpInstallCmd)
	cli.AddCommand(mcpCmd)
}
