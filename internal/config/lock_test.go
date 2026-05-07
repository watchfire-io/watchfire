//go:build !windows

package config

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// withTempLockPath redirects daemonLockPath to a path inside t.TempDir
// for the duration of a test. The original is restored on cleanup.
func withTempLockPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.lock")

	orig := daemonLockPath
	daemonLockPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { daemonLockPath = orig })

	return path
}

// TestAcquireDaemonLock_SecondAcquireBlocks verifies that while the
// first holder owns the lock, a concurrent AcquireDaemonLock call
// returns ErrDaemonLockHeld — and that releasing the first lock
// makes the second call succeed.
func TestAcquireDaemonLock_SecondAcquireBlocks(t *testing.T) {
	withTempLockPath(t)

	first, err := AcquireDaemonLock()
	if err != nil {
		t.Fatalf("first AcquireDaemonLock: unexpected error: %v", err)
	}
	if first == nil {
		t.Fatal("first AcquireDaemonLock: returned nil file")
	}

	// Second concurrent attempt must fail with ErrDaemonLockHeld.
	type result struct {
		f   interface{ Close() error }
		err error
	}
	contendCh := make(chan result, 1)
	go func() {
		f, err := AcquireDaemonLock()
		contendCh <- result{f: f, err: err}
	}()

	select {
	case r := <-contendCh:
		if !errors.Is(r.err, ErrDaemonLockHeld) {
			t.Fatalf("second AcquireDaemonLock: want ErrDaemonLockHeld, got err=%v file=%v", r.err, r.f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second AcquireDaemonLock: timed out (LOCK_NB should return immediately)")
	}

	// Release the first holder; second attempt should now succeed.
	if err := first.Close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}

	second, err := AcquireDaemonLock()
	if err != nil {
		t.Fatalf("AcquireDaemonLock after release: unexpected error: %v", err)
	}
	if second == nil {
		t.Fatal("AcquireDaemonLock after release: returned nil file")
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second lock: %v", err)
	}
}

// TestAcquireDaemonLock_FileNotDeletedOnRelease verifies that closing
// the lock file does NOT delete it — flock semantics rely on the
// file handle, not the file's existence on disk, so deletion would
// open a race window.
func TestAcquireDaemonLock_FileNotDeletedOnRelease(t *testing.T) {
	path := withTempLockPath(t)

	f, err := AcquireDaemonLock()
	if err != nil {
		t.Fatalf("AcquireDaemonLock: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}

	// Lockfile must still exist after release.
	if !FileExists(path) {
		t.Fatalf("lockfile %q removed on release; flock semantics require it stay on disk", path)
	}
}
