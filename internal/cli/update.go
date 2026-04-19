package cli

import (
	"fmt"
	"os"
	"path/filepath"

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
			if stopErr := stopDaemonForUpdate(daemonInfo.PID); stopErr != nil {
				fmt.Printf("Warning: failed to stop daemon: %v\n", stopErr)
			}
		}

		// Resolve install paths up front so the downloads can be staged
		// inside their target install directories — keeps the final rename
		// same-filesystem and sidesteps the EXDEV error (#25) that happens
		// when os.TempDir() and the install dir live on different
		// filesystems (e.g. tmpfs /tmp vs ext4 ~/.local/bin on Fedora/Ubuntu).
		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find self: %w", err)
		}
		selfPath, err = filepath.EvalSymlinks(selfPath)
		if err != nil {
			return fmt.Errorf("failed to resolve self: %w", err)
		}
		cliInstallDir := filepath.Dir(selfPath)

		daemonBinPath, err := findDaemonBinary()
		if err != nil {
			return fmt.Errorf("failed to find daemon binary: %w", err)
		}
		if resolved, symErr := filepath.EvalSymlinks(daemonBinPath); symErr == nil {
			daemonBinPath = resolved
		}
		daemonInstallDir := filepath.Dir(daemonBinPath)

		// Download new binaries
		fmt.Printf("Downloading CLI (%s)...\n", cliAsset.Name)
		cliTmpPath, err := updater.DownloadAsset(cliAsset, cliInstallDir)
		if err != nil {
			return fmt.Errorf("failed to download CLI: %w", err)
		}
		defer func() { _ = os.Remove(cliTmpPath) }()

		fmt.Printf("Downloading daemon (%s)...\n", daemonAsset.Name)
		daemonTmpPath, err := updater.DownloadAsset(daemonAsset, daemonInstallDir)
		if err != nil {
			return fmt.Errorf("failed to download daemon: %w", err)
		}
		defer func() { _ = os.Remove(daemonTmpPath) }()

		fmt.Println("Installing CLI...")
		if replaceErr := updater.ReplaceBinary(selfPath, cliTmpPath); replaceErr != nil {
			return fmt.Errorf("failed to update CLI: %w", replaceErr)
		}

		fmt.Println("Installing daemon...")
		if replaceErr := updater.ReplaceBinary(daemonBinPath, daemonTmpPath); replaceErr != nil {
			return fmt.Errorf("failed to update daemon: %w", replaceErr)
		}

		// Restart daemon if it was running
		if daemonWasRunning {
			fmt.Println("Restarting daemon...")
			if startErr := startDaemon(); startErr != nil {
				fmt.Printf("Warning: failed to restart daemon: %v\n", startErr)
			}
		}

		fmt.Printf("Updated to v%s.\n", result.LatestVersion)
		return nil
	},
}

// stopDaemonForUpdate stops the daemon process.
// Platform-specific implementation in update_unix.go (SIGTERM) and update_windows.go (Kill).
