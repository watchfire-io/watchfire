// Package cli implements the watchfire CLI commands.
package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/tui"
	pb "github.com/watchfire-io/watchfire/proto"
)

var rootCmd = &cobra.Command{
	Use:     "watchfire",
	Short:   "Orchestrate coding agent sessions based on specs",
	Version: buildinfo.Version,
	Long: `Watchfire orchestrates coding agent sessions based on task files.
It manages multiple projects in parallel, with one active task per project.`,
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Skip update hint for commands that already handle updates
		name := cmd.Name()
		if name == "update" || name == "version" {
			return
		}
		checkAndWarnUpdate()
	},
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

// checkAndWarnUpdate queries the daemon for update info and prints a warning.
func checkAndWarnUpdate() {
	running, info, err := config.IsDaemonRunning()
	if err != nil || !running || info == nil {
		return
	}

	addr := fmt.Sprintf("%s:%d", info.Host, info.Port)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := pb.NewDaemonServiceClient(conn)
	status, err := client.GetStatus(ctx, &emptypb.Empty{})
	if err != nil {
		return
	}

	if status.UpdateAvailable {
		fmt.Printf("\n%s\n", styleUpdate.Render(
			fmt.Sprintf("⚡ Update available: v%s — run 'watchfire update' to upgrade", status.UpdateVersion),
		))
	}
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Project lifecycle
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(defineCmd)
	rootCmd.AddCommand(configureCmd)

	// Task management
	rootCmd.AddCommand(taskCmd)

	// Execution verbs
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(generateCmd)
	rootCmd.AddCommand(wildfireCmd)

	// Daemon management
	rootCmd.AddCommand(daemonCmd)

	// Utility
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(versionCmd)
}
