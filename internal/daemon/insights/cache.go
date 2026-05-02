// Package insights — v6.0 Ember rollup cache.
//
// The dashboard rollup card and TUI fleet overlay both call
// `LoadGlobalInsights` on every refresh; serialising every call into a
// fan-out across every project's task YAMLs is wasteful when nothing has
// changed. The cache absorbs that cost by persisting one entry per
// (window_start, window_end) tuple under
// `~/.watchfire/insights-cache/_global.json`.
//
// Invalidation cascade: any per-project metrics change drops both the
// per-project cache (introduced in 0057) and the fleet `_global.json`
// cache. The cascade keeps "what counts" in one place — the watcher
// hooks just call `InvalidateProjectCache(projectID)`.
package insights

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
)

// CacheDirName is the on-disk directory under ~/.watchfire/.
const CacheDirName = "insights-cache"

// GlobalCacheFile is the file name for the fleet-wide rollup cache.
const GlobalCacheFile = "_global.json"

// cacheMu serialises read-modify-write on the JSON file. The cache is
// process-local — the daemon is a singleton so we don't need cross-process
// locking; this just serialises concurrent gRPC handlers.
var cacheMu sync.Mutex

// globalCacheFileShape is the on-disk JSON shape. A `map[key]entry`
// schema lets us cache multiple windows side-by-side (the GUI sometimes
// flips between 30d and 90d in quick succession).
type globalCacheFileShape struct {
	Entries map[string]*GlobalInsights `json:"entries"`
}

func cacheKey(start, end time.Time) string {
	// Window bounds round-tripped through time.RFC3339Nano so the on-disk
	// key is human-readable and stable across daemon restarts. A zero
	// time.Time encodes as "0001-01-01T00:00:00Z", which is fine.
	return start.UTC().Format(time.RFC3339Nano) + "|" + end.UTC().Format(time.RFC3339Nano)
}

func globalCachePath() (string, error) {
	dir, err := config.GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CacheDirName, GlobalCacheFile), nil
}

func ensureCacheDir() (string, error) {
	dir, err := config.GlobalDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(dir, CacheDirName)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	return cacheDir, nil
}

// readGlobalCache returns the cached rollup for (start, end) if present.
func readGlobalCache(start, end time.Time) (*GlobalInsights, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	path, err := globalCachePath()
	if err != nil {
		return nil, false
	}
	bytes, err := os.ReadFile(path) //nolint:gosec // path is daemon-controlled
	if err != nil {
		return nil, false
	}
	var shape globalCacheFileShape
	if err := json.Unmarshal(bytes, &shape); err != nil {
		return nil, false
	}
	entry, ok := shape.Entries[cacheKey(start, end)]
	if !ok || entry == nil {
		return nil, false
	}
	return entry, true
}

// writeGlobalCache merges `out` into the cache file under its window key.
func writeGlobalCache(out *GlobalInsights) {
	if out == nil {
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if _, err := ensureCacheDir(); err != nil {
		return
	}
	path, err := globalCachePath()
	if err != nil {
		return
	}
	shape := globalCacheFileShape{Entries: map[string]*GlobalInsights{}}
	if bytes, err := os.ReadFile(path); err == nil { //nolint:gosec // path is daemon-controlled
		_ = json.Unmarshal(bytes, &shape) // best-effort merge — corrupt file just gets overwritten
	}
	if shape.Entries == nil {
		shape.Entries = map[string]*GlobalInsights{}
	}
	shape.Entries[cacheKey(out.WindowStart, out.WindowEnd)] = out
	encoded, err := json.MarshalIndent(shape, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, encoded, 0o644) //nolint:gosec // process-private cache
}

// InvalidateGlobalCache drops the fleet-wide rollup cache file. Safe to
// call when the file doesn't exist.
func InvalidateGlobalCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	path, err := globalCachePath()
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Best-effort: a permission error here just means the next read
		// returns stale data, which the next write will overwrite anyway.
		return
	}
}

// InvalidateProjectCache drops both the per-project rollup cache (set up
// in 0057) and the fleet `_global.json` cache. The cascade is the
// contract: any caller that knows a project's metrics changed should
// just call this and let it fan out.
//
// `projectID` is currently unused by the public Go side (we only have the
// global cache today), but the parameter pins the contract so 0057's
// per-project cache layer can land additively.
func InvalidateProjectCache(projectID string) {
	_ = projectID // reserved for the per-project cache file added in 0057
	InvalidateGlobalCache()
}
