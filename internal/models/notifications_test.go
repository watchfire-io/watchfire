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
	if !ShouldNotify(NotificationTaskFailed, cfg, false, now) {
		t.Fatalf("baseline: expected notification to fire")
	}

	// Project mute overrides global enabled (true mute).
	if ShouldNotify(NotificationTaskFailed, cfg, true, now) {
		t.Errorf("per-project mute MUST override global enabled (true mute)")
	}
}

func TestShouldNotifyProjectUnmutedDoesNotOverrideGlobalDisabled(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.Enabled = false
	now := atTime(t, "12:00")

	// Project unmuted should NOT override global disabled.
	if ShouldNotify(NotificationTaskFailed, cfg, false, now) {
		t.Errorf("project unmuted MUST NOT override global disabled")
	}
}

func TestShouldNotifyPerEventToggle(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.Events.RunComplete = false
	now := atTime(t, "12:00")

	if !ShouldNotify(NotificationTaskFailed, cfg, false, now) {
		t.Errorf("task_failed enabled should still fire")
	}
	if ShouldNotify(NotificationRunComplete, cfg, false, now) {
		t.Errorf("run_complete disabled should not fire")
	}
}

func TestShouldNotifyDuringQuietHours(t *testing.T) {
	cfg := DefaultNotifications()
	cfg.QuietHours.Enabled = true
	cfg.QuietHours.Start = "22:00"
	cfg.QuietHours.End = "08:00"

	if ShouldNotify(NotificationTaskFailed, cfg, false, atTime(t, "23:00")) {
		t.Errorf("inside quiet hours should not fire")
	}
	if !ShouldNotify(NotificationTaskFailed, cfg, false, atTime(t, "12:00")) {
		t.Errorf("outside quiet hours should fire")
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
