//go:build windows

package config

import (
	"fmt"
	"os"
)

// AcquireDaemonLock is a no-op stub on Windows. v6.0 ships the
// singleton-daemon hardening (flock-based) on Unix only — the
// production bug was observed on macOS, and Linux + macOS share
// the syscall.Flock path. On Windows the daemon falls back to the
// pre-existing daemon.yaml / IsDaemonRunning best-effort check.
//
// The returned *os.File is a handle to the lockfile so callers can
// uniformly defer Close() without platform-specific branches; no
// OS-level lock is taken.
func AcquireDaemonLock() (*os.File, error) {
	path, err := daemonLockPath()
	if err != nil {
		return nil, fmt.Errorf("resolve daemon lock path: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock file: %w", err)
	}

	return f, nil
}
