package tray

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// notificationLogRecord matches one JSON line in
// `~/.watchfire/logs/<project_id>/notifications.log` (the format produced by
// task 0049's headless fallback path).
type notificationLogRecord struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	TaskNumber int32     `json:"task_number"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	EmittedAt  time.Time `json:"emitted_at"`
}

// LoadRecentNotifications reads notifications.log files for every project ID
// in projectIDs (looking each up under logsDir/<project_id>/notifications.log),
// keeps only entries whose EmittedAt falls within the local-time current day,
// and returns the most recent entries sorted newest-first. The result is
// capped at MaxNotifications. The unclamped today-only count is returned
// separately so the menu header can display "Notifications (N today) ▸".
//
// projectNames maps project ID → display name and is used to resolve the
// ProjectName field on each NotificationLogEntry. Missing entries fall back
// to the raw project ID.
//
// I/O errors on any individual project's log are non-fatal and silently
// skipped — the tray must keep working even if a single log is malformed
// or missing.
func LoadRecentNotifications(logsDir string, projectIDs []string, projectNames map[string]string, now time.Time) (entries []NotificationLogEntry, todayCount int) {
	year, month, day := now.Date()
	dayStart := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	dayEnd := dayStart.Add(24 * time.Hour)

	all := make([]notificationLogRecord, 0, 64)
	for _, pid := range projectIDs {
		path := filepath.Join(logsDir, pid, "notifications.log")
		recs, err := readNotificationsFile(path)
		if err != nil {
			continue
		}
		for _, r := range recs {
			emitted := r.EmittedAt.Local()
			if emitted.Before(dayStart) || !emitted.Before(dayEnd) {
				continue
			}
			if r.ProjectID == "" {
				r.ProjectID = pid
			}
			all = append(all, r)
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].EmittedAt.After(all[j].EmittedAt)
	})

	todayCount = len(all)
	if len(all) > MaxNotifications {
		all = all[:MaxNotifications]
	}

	entries = make([]NotificationLogEntry, 0, len(all))
	for _, r := range all {
		name := projectNames[r.ProjectID]
		if name == "" {
			name = r.ProjectID
		}
		entries = append(entries, NotificationLogEntry{
			ID:          r.ID,
			ProjectID:   r.ProjectID,
			ProjectName: name,
			TaskNumber:  r.TaskNumber,
			Kind:        r.Kind,
			Title:       r.Title,
			Body:        r.Body,
			AgeText:     formatAge(now.Sub(r.EmittedAt.Local())),
		})
	}
	return entries, todayCount
}

// readNotificationsFile parses a JSONL file. Malformed lines are skipped.
// Missing files return an empty slice with no error so the caller can treat
// "no notifications yet" as the steady state.
func readNotificationsFile(path string) ([]notificationLogRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []notificationLogRecord
	sc := bufio.NewScanner(f)
	// The default scanner buffer (64KB) is plenty for a JSON record but bump
	// it once so a pathological line doesn't kill the scan.
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec notificationLogRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// formatAge renders a duration as a short relative string suitable for the
// menu subtitle (e.g. "2m ago", "1h ago"). Floors to the largest unit that
// fits, matching the GUI's relative-time helper.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}
