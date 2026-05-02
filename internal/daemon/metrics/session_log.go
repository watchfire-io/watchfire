package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/watchfire-io/watchfire/internal/config"
)

// LocateSessionLog returns the absolute path to the most-recent session
// log for (projectID, taskNumber). Returns "" when no log exists; the
// caller can pass that to a Parser, which will return all-nil for the
// missing-file case.
//
// Logs land at `~/.watchfire/logs/<projectID>/<taskNumber>-<n>-<ts>.log`
// (see config.WriteLog). Filenames sort lexicographically by timestamp,
// so picking the lexically-largest match yields the most recent log.
func LocateSessionLog(projectID string, taskNumber int) string {
	logsDir, err := config.GlobalLogsDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(logsDir, projectID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	prefix := fmt.Sprintf("%04d-", taskNumber)
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		matches = append(matches, name)
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return filepath.Join(dir, matches[0])
}
