package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/watchfire-io/watchfire/internal/config"
)

// Daemon-log size cap. The total disk budget for the daemon log family
// (the active file plus its numbered backups) is roughly
// daemonLogFileCap * (1 + daemonLogBackups).
//
// v7.3.0 caps the family at ~1 GiB after a user's daemon.log grew to
// 300 GB under the v7.2.1 "rotate manually if it grows" deferral.
const (
	daemonLogFileCap = 500 * 1024 * 1024 // 500 MiB per file
	daemonLogBackups = 1                 // one numbered backup (daemon.log.1)
)

// openDaemonLog appends future log output to ~/.watchfire/daemon.log in
// addition to the existing stderr destination. Best-effort: if the file
// can't be opened, the daemon continues with stderr-only logging.
//
// Append (not truncate) so a restart preserves the immediately-prior
// run's log lines — those are usually the most diagnostic when the
// last process died unexpectedly. The destination is a size-capped
// rotatingFileWriter so an unattended daemon can't silently fill the
// disk.
func openDaemonLog() {
	dir, err := config.GlobalDir()
	if err != nil {
		log.Printf("[daemon-log] could not resolve global dir: %v — continuing with stderr only", err)
		return
	}
	path := filepath.Join(dir, "daemon.log")
	w, err := newRotatingFileWriter(path, daemonLogFileCap, daemonLogBackups)
	if err != nil {
		log.Printf("[daemon-log] could not open %s: %v — continuing with stderr only", path, err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, w))
	log.Printf("[daemon-log] writing logs to %s (cap %d MiB, %d backup)", path, daemonLogFileCap>>20, daemonLogBackups)
}

// rotatingFileWriter is a tiny self-rotating io.Writer for the daemon
// log. It enforces a per-file size cap (fileCap) and keeps up to
// `backups` numbered backups (daemon.log.1, daemon.log.2, …). When the
// active file would exceed fileCap on the next write, the writer
// rotates: active → .1, .1 → .2, etc., and the oldest backup beyond
// `backups` is dropped.
//
// Concurrency: the stdlib log package serialises calls through its own
// mutex, so Write is not called concurrently in practice. The writer
// still locks defensively — log.SetOutput could be swapped, callers
// could write directly. Cost is one uncontended lock per log line.
type rotatingFileWriter struct {
	mu      sync.Mutex
	path    string
	fileCap int64
	backups int
	f       *os.File
	size    int64
}

// newRotatingFileWriter opens the active log at path. If an existing
// file is already at or over the cap (the upgrade-from-oversized case),
// it is rotated immediately so the next write starts in a fresh file
// under cap.
func newRotatingFileWriter(path string, fileCap int64, backups int) (*rotatingFileWriter, error) {
	w := &rotatingFileWriter{path: path, fileCap: fileCap, backups: backups}
	info, err := os.Stat(path)
	if err == nil && info.Size() >= fileCap {
		if err := w.rotate(); err != nil {
			return nil, err
		}
		return w, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w.f = f
	if info != nil {
		w.size = info.Size()
	}
	return w, nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, fmt.Errorf("daemon log writer is closed")
	}
	if w.size+int64(len(p)) > w.fileCap {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the active file, shifts numbered backups down (dropping
// any beyond w.backups), promotes the active file to .1, and opens a
// fresh active file. Callers must hold w.mu.
func (w *rotatingFileWriter) rotate() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	// Defensive cleanup: drop any stale backups beyond w.backups. A
	// previous build might have used a higher backup count; without
	// this cleanup those files would linger forever.
	for i := w.backups + 1; i <= w.backups+10; i++ {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, i))
	}
	// Shift backups down: .<n-1> → .<n>, .<n-2> → .<n-1>, …, .1 → .2.
	// With backups=1 the loop runs zero times — active goes straight
	// to .1 below, overwriting any prior .1.
	for i := w.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	// Promote active → .1 (only if active exists; on first-call
	// startup-rotation the active file does exist).
	if _, err := os.Stat(w.path); err == nil {
		_ = os.Rename(w.path, w.path+".1")
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}
