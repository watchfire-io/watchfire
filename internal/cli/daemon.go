package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	cmd := exec.CommandContext(context.TODO(), daemonPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for daemon to be ready (max 5 seconds)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		running, info, err := config.IsDaemonRunning()
		if err == nil && running && info != nil {
			// Verify the port is actually accepting connections
			if waitForPort(info.Port, 2*time.Second) == nil {
				return nil
			}
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

	// Determine platform-appropriate executable extension
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	// Try relative to current executable
	execPath, err := os.Executable()
	if err == nil {
		base := filepath.Base(execPath)
		baseExt := filepath.Ext(base)
		name := strings.TrimSuffix(base, baseExt)
		daemonName := strings.Replace(name, "watchfire", "watchfired", 1) + baseExt
		daemonPath := filepath.Join(filepath.Dir(execPath), daemonName)
		if _, err := os.Stat(daemonPath); err == nil {
			return daemonPath, nil
		}
	}

	// Try build directory
	buildDaemon := "./build/watchfired" + ext
	if _, err := os.Stat(buildDaemon); err == nil {
		return buildDaemon, nil
	}

	return "", fmt.Errorf("watchfired not found. Install or build it first")
}

// waitForPort polls until a TCP connection to the given port succeeds or the timeout expires.
func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("localhost:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("port %d not ready after %s", port, timeout)
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
