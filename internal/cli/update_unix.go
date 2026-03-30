//go:build !windows

package cli

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
)

// stopDaemonForUpdate stops the daemon via SIGTERM, waiting up to 5 seconds.
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
