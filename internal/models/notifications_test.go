package models

import (
	"testing"
	"time"
)

func atTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("15:04", s, time.Local)
	if err != nil {
		t.Fatalf("invalid test time %q: %v", s, err)
	}
	return parsed
}

func TestIsQuietHoursWrappingMidnight(t *testing.T) {
	qh := QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"}
	cases := []struct {
		name string
		at   string
		want bool
	}{
		{"21:59 just before window", "21:59", false},
		{"22:00 boundary on", "22:00", true},
		{"22:01 inside", "22:01", true},
		{"00:00 midnight inside", "00:00", true},
		{"07:59 still inside", "07:59", true},
		{"08:00 boundary off (exclusive)", "08:00", false},
		{"08:01 after", "08:01", false},
		{"12:00 noon clearly off", "12:00", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsQuietHours(atTime(t, c.at), qh)
			if got != c.want {
				t.Errorf("IsQuietHours(%s) = %v, want %v", c.at, got, c.want)
			}
		})
	}
}

func TestIsQuietHoursNonWrapping(t *testing.T) {
	qh := QuietHoursConfig{Enabled: true, Start: "12:00", End: "14:00"}
	cases := []struct {
		at   string
		want bool
	}{
		{"11:59", false},
		{"12:00", true},
		{"12:01", true},
		{"13:59", true},
		{"14:00", false},
		{"14:01", false},
	}
	for _, c := range cases {
		got := IsQuietHours(atTime(t, c.at), qh)
		if got != c.want {
			t.Errorf("IsQuietHours(%s) = %v, want %v", c.at, got, c.want)
		}
	}
}

func TestIsQuietHoursDisabledIgnored(t *testing.T) {
	qh := QuietHoursConfig{Enabled: false, Start: "22:00", End: "08:00"}
	if IsQuietHours(atTime(t, "23:00"), qh) {
		t.Errorf("disabled quiet-hours should never gate")
	}
}

func TestIsQuietHoursMalformedFallsOpen(t *testing.T) {
	// A malformed config must never accidentally swallow notifications.
	qh := QuietHoursConfig{Enabled: true, Start: "garbage", End: "08:00"}
	if IsQuietHours(atTime(t, "23:00"), qh) {
		t.Errorf("malformed start should fall open, not silently mute")
	}
}

func TestShouldNotifyProjectMuteOverridesGlobalEnabled(t *testing.T) {
	cfg := DefaultNotifications()
	now := atTime(t, "12:00")

	// Sanity: globally enabled, no project mute → we notify.
	if !ShouldNotify(NotificationTaskFailed, cfg, ProjectNotifications{}, now) {
		t.Fatalf("baseline: expected notification to fire")
	}

	// Project mute overrides global enabled (true mute).
	if ShouldNotify(NotificationTaskFailed, cfg, ProjectNotifications{Muted: true}, now) {
		t.Errorf("per-project mute MUST override global enabled (true mute)")
	}
}

func TestShouldNotifyProjectUnmutedDoesNotOverrideGlobalDisabled(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.Enabled = false
	now := atTime(t, "12:00")

	// Project unmuted should NOT override global disabled.
	if ShouldNotify(NotificationTaskFailed, cfg, ProjectNotifications{}, now) {
		t.Errorf("project unmuted MUST NOT override global disabled")
	}
}

func TestShouldNotifyPerEventToggle(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.Events.RunComplete = false
	now := atTime(t, "12:00")

	if !ShouldNotify(NotificationTaskFailed, cfg, ProjectNotifications{}, now) {
		t.Errorf("task_failed enabled should still fire")
	}
	if ShouldNotify(NotificationRunComplete, cfg, ProjectNotifications{}, now) {
		t.Errorf("run_complete disabled should not fire")
	}
}

func TestShouldNotifyDuringQuietHours(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.QuietHours.Enabled = true
	cfg.QuietHours.Start = "22:00"
	cfg.QuietHours.End = "08:00"

	if ShouldNotify(NotificationTaskFailed, cfg, ProjectNotifications{}, atTime(t, "23:00")) {
		t.Errorf("inside quiet hours should not fire")
	}
	if !ShouldNotify(NotificationTaskFailed, cfg, ProjectNotifications{}, atTime(t, "12:00")) {
		t.Errorf("outside quiet hours should fire")
	}
}

// TestShouldNotifyProjectEventOverrideEnables exercises the v6 #0091 override
// path: a project that flips a globally-disabled event to enabled.
func TestShouldNotifyProjectEventOverrideEnables(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.Events.RunComplete = false
	now := atTime(t, "12:00")

	// Without override: global disabled wins.
	if ShouldNotify(NotificationRunComplete, cfg, ProjectNotifications{}, now) {
		t.Fatalf("baseline: global-disabled run_complete should not fire")
	}

	// With override: per-project Enabled=true wins.
	pn := ProjectNotifications{
		OverrideEvents: true,
		Events: map[string]ProjectEventPref{
			"run_complete": {Enabled: true},
		},
	}
	if !ShouldNotify(NotificationRunComplete, cfg, pn, now) {
		t.Errorf("project override Enabled=true must override global disabled")
	}
}

// TestShouldNotifyProjectEventOverrideDisables verifies the inverse: a
// project that mutes a globally-enabled event.
func TestShouldNotifyProjectEventOverrideDisables(t *testing.T) {
	cfg := DefaultNotifications()
	now := atTime(t, "12:00")

	pn := ProjectNotifications{
		OverrideEvents: true,
		Events: map[string]ProjectEventPref{
			"task_failed": {Enabled: false},
		},
	}
	if ShouldNotify(NotificationTaskFailed, cfg, pn, now) {
		t.Errorf("project override Enabled=false must override global enabled")
	}
	// Other events without an override row inherit globally — run_complete
	// is globally true, so it still fires.
	if !ShouldNotify(NotificationRunComplete, cfg, pn, now) {
		t.Errorf("event without override row must inherit global")
	}
}

// TestShouldNotifyOverrideWithoutMapInherits guards against the
// OverrideEvents=true / Events=nil partial-config case silently muting
// every event.
func TestShouldNotifyOverrideWithoutMapInherits(t *testing.T) {
	cfg := DefaultNotifications()
	now := atTime(t, "12:00")

	pn := ProjectNotifications{OverrideEvents: true, Events: nil}
	if !ShouldNotify(NotificationTaskFailed, cfg, pn, now) {
		t.Errorf("OverrideEvents=true with nil map must inherit global, not mute")
	}
}

// TestShouldNotifyQuietHoursOverride covers the per-project quiet-hours
// override path: project window mutes despite globals being off, and vice
// versa.
func TestShouldNotifyQuietHoursOverride(t *testing.T) {
	cfg := DefaultNotifications() // globals: quiet hours off

	// Project window 22:00 → 08:00 mutes despite globals being off.
	pn := ProjectNotifications{
		QuietHoursOverride: &QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"},
	}
	if ShouldNotify(NotificationTaskFailed, cfg, pn, atTime(t, "23:00")) {
		t.Errorf("project quiet-hours override must mute when globals are off")
	}

	// Globally muting window — but project override flips the window to a
	// non-overlapping one, so the notification fires.
	cfg.QuietHours = QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"}
	pn = ProjectNotifications{
		QuietHoursOverride: &QuietHoursConfig{Enabled: true, Start: "12:00", End: "13:00"},
	}
	if !ShouldNotify(NotificationTaskFailed, cfg, pn, atTime(t, "23:00")) {
		t.Errorf("project quiet-hours override must replace global window, not union it")
	}
}

// TestResolveSoundOverridePath round-trips per-event sound overrides.
func TestResolveSoundOverridePath(t *testing.T) {
	cfg := DefaultNotifications()
	defaults := map[NotificationKind]string{
		NotificationTaskFailed:  "default-failed.wav",
		NotificationRunComplete: "default-complete.wav",
	}

	// No override: fall back to default.
	if got := ResolveSound(NotificationTaskFailed, cfg, ProjectNotifications{}, defaults); got != "default-failed.wav" {
		t.Errorf("expected default sound, got %q", got)
	}

	// Override with empty Sound: fall back to default (inherit).
	pn := ProjectNotifications{
		OverrideEvents: true,
		Events:         map[string]ProjectEventPref{"task_failed": {Enabled: true, Sound: ""}},
	}
	if got := ResolveSound(NotificationTaskFailed, cfg, pn, defaults); got != "default-failed.wav" {
		t.Errorf("empty Sound should inherit default; got %q", got)
	}

	// Override with explicit Sound: use override.
	pn.Events["task_failed"] = ProjectEventPref{Enabled: true, Sound: "custom.wav"}
	if got := ResolveSound(NotificationTaskFailed, cfg, pn, defaults); got != "custom.wav" {
		t.Errorf("expected custom override sound, got %q", got)
	}
}

func TestIsValidTimeOfDay(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"00:00", true},
		{"08:00", true},
		{"13:45", true},
		{"23:59", true},
		{"24:00", false},
		{"7:00", false},
		{"08:60", false},
		{"08", false},
		{"", false},
		{"08:00:00", false},
		{"abc", false},
	}
	for _, c := range cases {
		if got := IsValidTimeOfDay(c.s); got != c.want {
			t.Errorf("IsValidTimeOfDay(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
