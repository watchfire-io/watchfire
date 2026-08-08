package agent

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// cleanupSessionHome removes the per-session agent home directory
// (~/.watchfire/<agent>-home/<session>) once the session has ended and its
// transcript has been exported to the session log (#47). The directory is
// pure scratch — the composed system prompt, config symlinks, and the agent
// CLI's own session state — all recreated by InstallSystemPrompt on the
// next session with the same name.
func cleanupSessionHome(projectID, backendName, sessionName string) {
	if sessionName == "" {
		return
	}
	be, ok := backend.Get(backendName)
	if !ok {
		return
	}
	provider, ok := be.(backend.SessionHomeProvider)
	if !ok {
		return // e.g. Claude — delivers the prompt via CLI flag, no session home
	}
	dir, err := provider.SessionHome(sessionName)
	if err != nil {
		return
	}
	root, err := provider.SessionHomeRoot()
	if err != nil {
		return
	}
	if !isDirectChildOf(root, dir) {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		config.ProjectLogf(projectID, "[session-home] Failed to remove %s: %v", dir, err)
		return
	}
	config.ProjectLogf(projectID, "[session-home] removed %s", dir)
}

// CleanupProjectSessionHomes removes every per-session agent home directory
// belonging to projectName across all registered backends (#47). Session
// names begin with the project's name slug followed by ':', so the sweep is
// a prefix match on directory names under each backend's home root.
// Directories belonging to currently running sessions are skipped — they
// are removed by their own end-of-session cleanup — which also defuses the
// edge case of another project sharing the same 30-char slug while one of
// its sessions is mid-flight.
func (m *Manager) CleanupProjectSessionHomes(projectName string) {
	prefix := slugify(projectName, 30) + ":"

	active := map[string]bool{}
	m.mu.RLock()
	for _, ag := range m.agents {
		be, ok := backend.Get(ag.BackendName)
		if !ok {
			continue
		}
		provider, ok := be.(backend.SessionHomeProvider)
		if !ok {
			continue
		}
		if dir, err := provider.SessionHome(ag.SessionName); err == nil {
			active[dir] = true
		}
	}
	m.mu.RUnlock()

	removed := 0
	for _, be := range backend.List() {
		provider, ok := be.(backend.SessionHomeProvider)
		if !ok {
			continue
		}
		root, err := provider.SessionHomeRoot()
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // root absent — this backend never ran
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if active[dir] || !isDirectChildOf(root, dir) {
				continue
			}
			if rmErr := os.RemoveAll(dir); rmErr == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		log.Printf("[session-home] project %q deleted — removed %d leftover session home dir(s)", projectName, removed)
	}
}

// isDirectChildOf reports whether dir is a direct child of root — the guard
// that keeps a degenerate session name from resolving RemoveAll at the home
// root itself or anywhere outside it.
func isDirectChildOf(root, dir string) bool {
	if root == "" || dir == "" || dir == root {
		return false
	}
	if filepath.Dir(dir) != root {
		return false
	}
	base := filepath.Base(dir)
	return base != "" && base != "." && base != ".." && base != string(filepath.Separator)
}
