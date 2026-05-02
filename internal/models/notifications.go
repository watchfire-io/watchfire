package models

import "time"

// IsQuietHours reports whether the given local-time clock falls inside the
// configured quiet-hours window. The window is inclusive on Start and
// exclusive on End, so [22:00, 08:00) means 22:00 mutes and 08:00 unmutes.
//
// Wrap-around windows where Start > End (e.g. 22:00 → 08:00) cover the
// interval (start..24:00) ∪ [00:00..end). Non-wrapping windows where
// Start <= End (e.g. 12:00 → 14:00) cover [start..end).
//
// When Enabled is false or either time-of-day string is malformed the gate
// returns false (i.e. notifications are NOT muted) — defensive: a broken
// quiet-hours config should never silently swallow notifications.
func IsQuietHours(now time.Time, qh QuietHoursConfig) bool {
	if !qh.Enabled {
		return false
	}
	startMin, ok := parseTimeOfDay(qh.Start)
	if !ok {
		return false
	}
	endMin, ok := parseTimeOfDay(qh.End)
	if !ok {
		return false
	}
	if startMin == endMin {
		return false
	}
	nowMin := now.Hour()*60 + now.Minute()
	if startMin < endMin {
		return nowMin >= startMin && nowMin < endMin
	}
	// Wrap around midnight.
	return nowMin >= startMin || nowMin < endMin
}

// parseTimeOfDay parses a HH:MM string into minutes since midnight.
// Returns false on any malformed input.
func parseTimeOfDay(s string) (int, bool) {
	if !IsValidTimeOfDay(s) {
		return 0, false
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	return h*60 + m, true
}

// NotificationKind enumerates the high-level reasons we'd emit a notification.
// Kept here (instead of in a daemon-only package) so the gate function can
// branch on it without importing daemon code.
type NotificationKind int

const (
	NotificationTaskFailed NotificationKind = iota
	NotificationRunComplete
)

// ShouldNotify combines all the gates a notification has to pass before it
// reaches the OS layer: master toggle, per-event toggle, per-project mute,
// and quiet hours. now is wall-clock local time (the caller passes
// time.Now().Local() in production; tests pass a fixed time).
func ShouldNotify(kind NotificationKind, cfg NotificationsConfig, projectMuted bool, now time.Time) bool {
	if projectMuted {
		return false
	}
	if !cfg.Enabled {
		return false
	}
	switch kind {
	case NotificationTaskFailed:
		if !cfg.Events.TaskFailed {
			return false
		}
	case NotificationRunComplete:
		if !cfg.Events.RunComplete {
			return false
		}
	}
	if IsQuietHours(now, cfg.QuietHours) {
		return false
	}
	return true
}
