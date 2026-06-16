package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Per-project daemon log size cap. The verbose, "hardcore" operational
// trail (wildfire chain decisions, merge/auto-PR, agent lifecycle, issue
// detection) is written per project to
// ~/.watchfire/logs/<project_id>/daemon.log so the global ~/.watchfire/daemon.log
// stays small and readable. Each project log family is capped at
// projectLogCap * (1 + 1 backup).
const (
	projectLogCap     = 16 * 1024 * 1024 // 16 MiB per project log file
	projectLogBackups = 1                // one numbered backup (daemon.log.1)
)

// projectLogWriter is a tiny self-rotating appender for one project's
// daemon log. It mirrors the daemon-package rotatingFileWriter but lives
// in config so every daemon package (agent, watcher, server) can route
// project-scoped lines here without an import cycle.
type projectLogWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

var (
	projectLogsMu sync.Mutex
	projectLogs   = map[string]*projectLogWriter{}
)

// getProjectLogWriter returns the cached writer for a project, creating the
// ~/.watchfire/logs/<projectID>/ directory and opening the file on first use.
func getProjectLogWriter(projectID string) (*projectLogWriter, error) {
	projectLogsMu.Lock()
	defer projectLogsMu.Unlock()
	if w, ok := projectLogs[projectID]; ok {
		return w, nil
	}
	logsDir, err := GlobalLogsDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(logsDir, projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &projectLogWriter{path: filepath.Join(dir, "daemon.log")}
	projectLogs[projectID] = w
	return w, nil
}

func (w *projectLogWriter) write(line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		w.f = f
		if info, statErr := f.Stat(); statErr == nil {
			w.size = info.Size()
		}
	}
	if w.size+int64(len(line)) > projectLogCap {
		w.rotateLocked()
		if w.f == nil {
			return
		}
	}
	n, err := w.f.WriteString(line)
	if err == nil {
		w.size += int64(n)
	}
}

// rotateLocked promotes the active file to .1 (dropping the prior backup)
// and opens a fresh active file. Callers must hold w.mu.
func (w *projectLogWriter) rotateLocked() {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	for i := projectLogBackups + 1; i <= projectLogBackups+10; i++ {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, i))
	}
	if _, err := os.Stat(w.path); err == nil {
		_ = os.Rename(w.path, w.path+".1")
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	w.f = f
	w.size = 0
}

// ProjectLogf appends a timestamped line to
// ~/.watchfire/logs/<projectID>/daemon.log. It is best-effort and never
// panics or returns an error — logging must never break the daemon. A line
// with an empty projectID is dropped (the caller should use the global log
// for project-less events).
func ProjectLogf(projectID, format string, args ...interface{}) {
	if projectID == "" {
		return
	}
	w, err := getProjectLogWriter(projectID)
	if err != nil {
		return
	}
	ts := time.Now().Format("2006/01/02 15:04:05")
	w.write(ts + " " + fmt.Sprintf(format, args...) + "\n")
}
