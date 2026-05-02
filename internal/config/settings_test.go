package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/watchfire-io/watchfire/internal/models"
)

// TestSettingsRoundTripAllFieldsSet writes a fully-populated settings.yaml,
// reads it back, and asserts every notification subfield round-trips cleanly.
func TestSettingsRoundTripAllFieldsSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	want := models.NewSettings()
	want.Defaults.Notifications = models.NotificationsConfig{
		Enabled: true,
		Events: models.NotificationsEvents{
			TaskFailed:   false,
			RunComplete:  true,
			WeeklyDigest: true,
		},
		Sounds: models.NotificationsSounds{
			Enabled:     false,
			TaskFailed:  true,
			RunComplete: false,
			Volume:      0.42,
		},
		QuietHours: models.QuietHoursConfig{
			Enabled: true,
			Start:   "22:00",
			End:     "08:00",
		},
		DigestSchedule: "FRI 17:00",
	}

	if err := SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	if got.Defaults.Notifications != want.Defaults.Notifications {
		t.Errorf("Notifications round-trip mismatch:\n got=%+v\nwant=%+v",
			got.Defaults.Notifications, want.Defaults.Notifications)
	}
}

// TestSettingsRoundTripEmptyFile guarantees that a settings.yaml with no
// `notifications:` block at all reads back as fully-defaulted notifications.
// This is what hand-edited / pre-existing files look like before the user
// opens the new preferences UI.
func TestSettingsRoundTripEmptyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Write a minimal settings.yaml that omits the notifications block.
	settingsPath := filepath.Join(dir, ".watchfire", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("version: 1\n")
	if err := os.WriteFile(settingsPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	want := models.DefaultNotifications()
	if got.Defaults.Notifications != want {
		t.Errorf("empty settings.yaml should yield default notifications:\n got=%+v\nwant=%+v",
			got.Defaults.Notifications, want)
	}
}
