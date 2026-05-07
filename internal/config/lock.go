package config

import (
	"errors"
	"path/filepath"
)

// DaemonLockFileName is the name of the daemon singleton lock file
// inside ~/.watchfire/. The lock is an OS-level advisory flock taken
// for the lifetime of the daemon process; the file itself is never
// deleted on release because flock release happens on file-handle
// close (which the OS guarantees on process exit, including SIGKILL).
const DaemonLockFileName = "daemon.lock"

// ErrDaemonLockHeld is returned by AcquireDaemonLock when another
// process already holds the daemon singleton lock. Callers should
// log a friendly message and exit cleanly with status 0 — a
// duplicate spawn is the expected outcome of a startup race, not
// an error condition.
var ErrDaemonLockHeld = errors.New("daemon lock held by another process")

// GlobalDaemonLockFile returns the path to ~/.watchfire/daemon.lock.
func GlobalDaemonLockFile() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DaemonLockFileName), nil
}

// daemonLockPath resolves the lockfile path. It is a package-private
// indirection so tests can override it to a temporary directory
// without touching the real ~/.watchfire/daemon.lock.
var daemonLockPath = func() (string, error) {
	return GlobalDaemonLockFile()
}
