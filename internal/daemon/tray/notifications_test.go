package tray

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRecentNotificationsTodayOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.Local)
	yesterday := now.Add(-25 * time.Hour)
	thisMorning := now.Add(-3 * time.Hour)

	writeNotificationsFile(t, filepath.Join(dir, "p1", "notifications.log"),
		`{"id":"old","project_id":"p1","task_number":1,"kind":"TASK_FAILED","title":"old","emitted_at":"`+yesterday.UTC().Format(time.RFC3339Nano)+`"}`,
		`{"id":"recent","project_id":"p1","task_number":2,"kind":"TASK_FAILED","title":"recent","emitted_at":"`+thisMorning.UTC().Format(time.RFC3339Nano)+`"}`,
	)

	got, total := LoadRecentNotifications(dir, []string{"p1"}, map[string]string{"p1": "alpha"}, now)
	if total != 1 {
		t.Fatalf("today count = %d, want 1", total)
	}
	if len(got) != 1 || got[0].ID != "recent" {
		t.Fatalf("entries = %+v", got)
	}
	if got[0].ProjectName != "alpha" {
		t.Fatalf("ProjectName = %q, want alpha", got[0].ProjectName)
	}
}

func TestLoadRecentNotificationsCappedAt10(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.Local)

	lines := make([]string, 0, 15)
	for i := 0; i < 15; i++ {
		emitted := now.Add(-time.Duration(i) * time.Minute)
		lines = append(lines,
			`{"id":"`+itoa(i)+`","project_id":"p1","kind":"TASK_FAILED","title":"x","emitted_at":"`+emitted.UTC().Format(time.RFC3339Nano)+`"}`)
	}
	writeNotificationsFile(t, filepath.Join(dir, "p1", "notifications.log"), lines...)

	got, total := LoadRecentNotifications(dir, []string{"p1"}, nil, now)
	if total != 15 {
		t.Fatalf("total today = %d, want 15", total)
	}
	if len(got) != MaxNotifications {
		t.Fatalf("returned entries = %d, want %d", len(got), MaxNotifications)
	}
	if got[0].ID != "0" {
		t.Fatalf("first entry id = %q, want '0' (newest first)", got[0].ID)
	}
}

func TestLoadRecentNotificationsMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, total := LoadRecentNotifications(dir, []string{"p1", "p2"}, nil, time.Now())
	if total != 0 || len(got) != 0 {
		t.Fatalf("expected empty, got total=%d entries=%d", total, len(got))
	}
}

func writeNotificationsFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
