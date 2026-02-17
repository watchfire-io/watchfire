package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/updater"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update watchfire to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Checking for updates...")

		result, err := updater.CheckForUpdate()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}

		if !result.Available {
			fmt.Printf("Already up to date (v%s).\n", result.CurrentVersion)
			return nil
		}

		fmt.Printf("Update available: v%s → v%s\n", result.CurrentVersion, result.LatestVersion)
		fmt.Printf("Release: %s\n", result.ReleaseURL)

		// Find CLI and daemon assets
		cliAsset := updater.FindAsset(result.Release, updater.CLIAssetName())
		daemonAsset := updater.FindAsset(result.Release, updater.DaemonAssetName())

		if cliAsset == nil {
			return fmt.Errorf("CLI binary not found in release (expected %s)", updater.CLIAssetName())
		}
		if daemonAsset == nil {
			return fmt.Errorf("daemon binary not found in release (expected %s)", updater.DaemonAssetName())
		}

		// Check if daemon is running
		daemonWasRunning, daemonInfo, _ := config.IsDaemonRunning()

		// Stop daemon if running
		if daemonWasRunning && daemonInfo != nil {
			fmt.Println("Stopping daemon...")
			if err := stopDaemonForUpdate(daemonInfo.PID); err != nil {
				fmt.Printf("Warning: failed to stop daemon: %v\n", err)
			}
		}

		// Download new binaries
		fmt.Printf("Downloading CLI (%s)...\n", cliAsset.Name)
		cliTmpPath, err := updater.DownloadAsset(cliAsset)
		if err != nil {
			return fmt.Errorf("failed to download CLI: %w", err)
		}
		defer os.Remove(cliTmpPath)

		fmt.Printf("Downloading daemon (%s)...\n", daemonAsset.Name)
		daemonTmpPath, err := updater.DownloadAsset(daemonAsset)
		if err != nil {
			return fmt.Errorf("failed to download daemon: %w", err)
		}
		defer os.Remove(daemonTmpPath)

		// Replace CLI binary (self)
		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find self: %w", err)
		}
		selfPath, err = filepath.EvalSymlinks(selfPath)
		if err != nil {
			return fmt.Errorf("failed to resolve self: %w", err)
		}

		fmt.Println("Installing CLI...")
		if err := updater.ReplaceBinary(selfPath, cliTmpPath); err != nil {
			return fmt.Errorf("failed to update CLI: %w", err)
		}

		// Replace daemon binary
		daemonBinPath, err := findDaemonBinary()
		if err != nil {
			return fmt.Errorf("failed to find daemon binary: %w", err)
		}

		fmt.Println("Installing daemon...")
		if err := updater.ReplaceBinary(daemonBinPath, daemonTmpPath); err != nil {
			return fmt.Errorf("failed to update daemon: %w", err)
		}

		// Restart daemon if it was running
		if daemonWasRunning {
			fmt.Println("Restarting daemon...")
			if err := startDaemon(); err != nil {
				fmt.Printf("Warning: failed to restart daemon: %v\n", err)
			}
		}

		fmt.Printf("Updated to v%s.\n", result.LatestVersion)
		return nil
	},
}

// stopDaemonForUpdate stops the daemon via SIGTERM.
func stopDaemonForUpdate(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send stop signal: %w", err)
	}

	// Wait for daemon to stop
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		running, _, _ := config.IsDaemonRunning()
		if !running {
			return nil
		}
	}
	return fmt.Errorf("daemon did not stop in time")
}
