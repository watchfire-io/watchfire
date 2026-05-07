//go:build !windows

package config

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// AcquireDaemonLock takes an exclusive non-blocking advisory flock on
// ~/.watchfire/daemon.lock. On success it returns the open *os.File —
// the caller MUST keep this handle alive for the lifetime of the
// daemon process. Closing the file (or process exit) releases the
// lock. The lockfile itself is never removed on release; flock
// semantics handle stale files correctly across crashes.
//
// Returns ErrDaemonLockHeld if another process already holds the
// lock. Other errors (open / fcntl failures) are wrapped and
// returned as-is.
func AcquireDaemonLock() (*os.File, error) {
	path, err := daemonLockPath()
	if err != nil {
		return nil, fmt.Errorf("resolve daemon lock path: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrDaemonLockHeld
		}
		return nil, fmt.Errorf("flock daemon lock file: %w", err)
	}

	return f, nil
}
