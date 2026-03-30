//go:build windows

package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
)

// stopDaemonForUpdate stops the daemon by killing the process.
// Windows has no SIGTERM equivalent, so we kill directly.
func stopDaemonForUpdate(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process: %w", err)
	}

	if err := process.Kill(); err != nil {
		return fmt.Errorf("kill daemon process: %w", err)
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
