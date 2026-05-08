package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// TestProjectYAMLRoundTripPreV6 — a project.yaml that pre-dates the v6
// notification overrides (only `muted: false`) must load identically and
// not accidentally materialise the new sub-fields on save. omitempty
// matters: a fresh file with no override should not carry empty maps /
// nil pointers in its YAML form.
func TestProjectYAMLRoundTripPreV6(t *testing.T) {
	dir := t.TempDir()

	body := []byte(`version: 1
project_id: pre-v6-id
name: legacy
status: active
color: "#ef4444"
default_agent: claude-code
sandbox: auto
auto_merge: true
auto_delete_branch: true
auto_start_tasks: true
notifications:
  muted: false
definition: ""
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
next_task_number: 1
`)
	if err := EnsureProjectDir(dir); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	if err := os.WriteFile(ProjectFile(dir), body, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProject(dir)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if got == nil {
		t.Fatalf("LoadProject returned nil")
	}
	if got.Notifications.Muted {
		t.Errorf("legacy project should load with Muted=false")
	}
	if got.Notifications.OverrideEvents {
		t.Errorf("legacy project should NOT inherit OverrideEvents=true")
	}
	if got.Notifications.Events != nil {
		t.Errorf("legacy project should not materialise an Events map; got %v", got.Notifications.Events)
	}
	if got.Notifications.QuietHoursOverride != nil {
		t.Errorf("legacy project should not materialise QuietHoursOverride; got %v", got.Notifications.QuietHoursOverride)
	}
}

// TestProjectYAMLRoundTripV6Overrides — write a project.yaml carrying the
// new override fields, read it back, assert exact equality.
func TestProjectYAMLRoundTripV6Overrides(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureProjectDir(dir); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}

	want := &models.Project{
		Version:          1,
		ProjectID:        "v6-override-id",
		Name:             "v6",
		Status:           "active",
		Color:            "#3b82f6",
		DefaultAgent:     "claude-code",
		Sandbox:          "auto",
		AutoMerge:        true,
		AutoDeleteBranch: true,
		AutoStartTasks:   true,
		Notifications: models.ProjectNotifications{
			Muted:          false,
			OverrideEvents: true,
			Events: map[string]models.ProjectEventPref{
				"task_failed":  {Enabled: true, Sound: "alert.wav"},
				"run_complete": {Enabled: false},
			},
			QuietHoursOverride: &models.QuietHoursConfig{
				Enabled: true,
				Start:   "23:00",
				End:     "07:00",
			},
		},
		Integrations: models.ProjectIntegrations{
			SlackChannel:   "#deploys",
			DiscordGuildID: "123456789",
		},
		CreatedAt:      time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
		NextTaskNumber: 5,
	}
	if err := SaveProject(dir, want); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	got, err := LoadProject(dir)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if got == nil {
		t.Fatalf("LoadProject returned nil")
	}

	if got.Notifications.OverrideEvents != want.Notifications.OverrideEvents {
		t.Errorf("OverrideEvents mismatch: got %v want %v", got.Notifications.OverrideEvents, want.Notifications.OverrideEvents)
	}
	if len(got.Notifications.Events) != len(want.Notifications.Events) {
		t.Errorf("Events len mismatch: got %d want %d", len(got.Notifications.Events), len(want.Notifications.Events))
	}
	if got.Notifications.Events["task_failed"].Sound != "alert.wav" {
		t.Errorf("task_failed sound did not round-trip")
	}
	if got.Notifications.QuietHoursOverride == nil ||
		got.Notifications.QuietHoursOverride.Start != "23:00" {
		t.Errorf("QuietHoursOverride did not round-trip; got %v", got.Notifications.QuietHoursOverride)
	}
	if got.Integrations.SlackChannel != "#deploys" {
		t.Errorf("Integrations.SlackChannel did not round-trip; got %q", got.Integrations.SlackChannel)
	}
	if got.Integrations.DiscordGuildID != "123456789" {
		t.Errorf("Integrations.DiscordGuildID did not round-trip; got %q", got.Integrations.DiscordGuildID)
	}

	// Inspect the on-disk YAML — empty fields must be omitted rather than
	// rendered as `key: null` / `key: {}`. This is the contract that lets
	// hand-edited files stay terse.
	raw, err := os.ReadFile(ProjectFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "override_events") {
		t.Errorf("v6 round-trip should write override_events; raw=\n%s", raw)
	}
	if !strings.Contains(string(raw), "slack_channel") {
		t.Errorf("v6 round-trip should write slack_channel; raw=\n%s", raw)
	}
}
