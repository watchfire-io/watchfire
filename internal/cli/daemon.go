package cli

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
)

// EnsureDaemon makes sure the daemon is running, starting it if necessary.
func EnsureDaemon() error {
	running, info, err := config.IsDaemonRunning()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if running {
		return nil
	}

	// Clean up stale daemon info if it exists
	if info != nil {
		_ = config.RemoveDaemonInfo()
	}

	// Start daemon in background
	return startDaemon()
}

// startDaemon starts the daemon process in the background.
func startDaemon() error {
	// Find the daemon binary
	daemonPath, err := findDaemonBinary()
	if err != nil {
		return err
	}

	// Start daemon in background
	cmd := exec.Command(daemonPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for daemon to be ready (max 5 seconds)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		running, _, err := config.IsDaemonRunning()
		if err == nil && running {
			return nil
		}
	}

	return fmt.Errorf("daemon failed to start within timeout")
}

// findDaemonBinary locates the watchfired binary.
func findDaemonBinary() (string, error) {
	// Try PATH first
	path, err := exec.LookPath("watchfired")
	if err == nil {
		return path, nil
	}

	// Try relative to current executable
	execPath, err := os.Executable()
	if err == nil {
		// Try same directory
		daemonPath := execPath[:len(execPath)-len("watchfire")] + "watchfired"
		if _, err := os.Stat(daemonPath); err == nil {
			return daemonPath, nil
		}
	}

	// Try build directory
	if _, err := os.Stat("./build/watchfired"); err == nil {
		return "./build/watchfired", nil
	}

	return "", fmt.Errorf("watchfired not found. Install or build it first")
}

// GetDaemonStatus returns the daemon status.
func GetDaemonStatus() (bool, *DaemonStatusInfo, error) {
	running, info, err := config.IsDaemonRunning()
	if err != nil {
		return false, nil, err
	}

	if !running || info == nil {
		return false, nil, nil
	}

	return true, &DaemonStatusInfo{
		Host:      info.Host,
		Port:      info.Port,
		PID:       info.PID,
		StartedAt: info.StartedAt,
	}, nil
}

// DaemonStatusInfo contains daemon status information.
type DaemonStatusInfo struct {
	Host      string
	Port      int
	PID       int
	StartedAt time.Time
}
