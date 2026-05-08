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
	NotificationWeeklyDigest
)

// ShouldNotify combines all the gates a notification has to pass before it
// reaches the OS layer: master toggle, per-event toggle, per-project mute,
// and quiet hours. now is wall-clock local time (the caller passes
// time.Now().Local() in production; tests pass a fixed time).
//
// Pre-v6 callers can pass an empty ProjectNotifications{} — the function
// then behaves identically to the v4.0 Beacon two-argument signature. New
// callers can pass a fully-populated struct and the function will consult
// per-project overrides for events and quiet hours before falling back to
// the global config.
func ShouldNotify(kind NotificationKind, cfg NotificationsConfig, project ProjectNotifications, now time.Time) bool {
	if project.Muted {
		return false
	}
	if !cfg.Enabled {
		return false
	}
	if !eventEnabled(kind, cfg, project) {
		return false
	}
	qh := cfg.QuietHours
	if project.QuietHoursOverride != nil {
		qh = *project.QuietHoursOverride
	}
	if IsQuietHours(now, qh) {
		return false
	}
	return true
}

// eventEnabled reports whether the per-event gate lets `kind` through.
// Project overrides win when OverrideEvents is set AND a row for the event
// is present in the map; otherwise the global cfg.Events block decides.
// A `nil` Events map with OverrideEvents=true is treated as "all events
// inherit globally" — defensive, so a partial override never silently
// disables every notification.
func eventEnabled(kind NotificationKind, cfg NotificationsConfig, project ProjectNotifications) bool {
	if project.OverrideEvents && project.Events != nil {
		if pref, ok := project.Events[eventKey(kind)]; ok {
			return pref.Enabled
		}
	}
	switch kind {
	case NotificationTaskFailed:
		return cfg.Events.TaskFailed
	case NotificationRunComplete:
		return cfg.Events.RunComplete
	case NotificationWeeklyDigest:
		return cfg.Events.WeeklyDigest
	}
	return true
}

// eventKey is the YAML / map key for a NotificationKind. Stays internal so
// callers don't reach into the map directly; the tests round-trip through
// ShouldNotify rather than asserting on raw keys.
func eventKey(kind NotificationKind) string {
	switch kind {
	case NotificationTaskFailed:
		return "task_failed"
	case NotificationRunComplete:
		return "run_complete"
	case NotificationWeeklyDigest:
		return "weekly_digest"
	}
	return ""
}

// ResolveSound returns the sound name to play for a given notification
// kind, honouring the per-project override when present and falling back
// to the global default. Empty return means "no sound configured" — the
// caller should not play one.
func ResolveSound(kind NotificationKind, cfg NotificationsConfig, project ProjectNotifications, defaults map[NotificationKind]string) string {
	if project.OverrideEvents && project.Events != nil {
		if pref, ok := project.Events[eventKey(kind)]; ok && pref.Sound != "" {
			return pref.Sound
		}
	}
	if defaults != nil {
		return defaults[kind]
	}
	return ""
}
