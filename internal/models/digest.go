package models

import (
	"strings"
	"time"
)

// DigestSchedule is a parsed weekly-digest cadence: a target weekday (or
// every-day for DAILY) at a specific local hour:minute. Cron-ish source
// strings are accepted by ParseDigestSchedule:
//
//	"MON HH:MM" / "TUE HH:MM" / ... / "SUN HH:MM" — fire on that weekday
//	"DAILY HH:MM"                                  — fire every day
//
// The default cadence is DefaultDigestSchedule ("MON 09:00").
type DigestSchedule struct {
	// Daily, when true, makes the schedule fire every day at HH:MM.
	Daily bool
	// Weekday is the target weekday for non-daily schedules.
	Weekday time.Weekday
	// Hour is the 24-hour local-time hour, 0-23.
	Hour int
	// Minute is the 0-59 minute.
	Minute int
}

// ParseDigestSchedule parses a cron-ish digest schedule string. Returns the
// parsed schedule and ok=true on success. On any malformed input, returns
// the default schedule (MON 09:00) and ok=false so the daemon falls back to
// a known-good cadence rather than silently never firing.
func ParseDigestSchedule(s string) (DigestSchedule, bool) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}, false
	}
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}, false
	}
	dayPart, timePart := parts[0], parts[1]

	if !IsValidTimeOfDay(timePart) {
		return DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}, false
	}
	hour := int(timePart[0]-'0')*10 + int(timePart[1]-'0')
	minute := int(timePart[3]-'0')*10 + int(timePart[4]-'0')

	if dayPart == "DAILY" {
		return DigestSchedule{Daily: true, Hour: hour, Minute: minute}, true
	}
	wd, ok := parseWeekday(dayPart)
	if !ok {
		return DigestSchedule{Weekday: time.Monday, Hour: hour, Minute: minute}, false
	}
	return DigestSchedule{Weekday: wd, Hour: hour, Minute: minute}, true
}

func parseWeekday(s string) (time.Weekday, bool) {
	switch s {
	case "SUN":
		return time.Sunday, true
	case "MON":
		return time.Monday, true
	case "TUE":
		return time.Tuesday, true
	case "WED":
		return time.Wednesday, true
	case "THU":
		return time.Thursday, true
	case "FRI":
		return time.Friday, true
	case "SAT":
		return time.Saturday, true
	}
	return time.Sunday, false
}

// NextFire returns the next time this schedule should fire after `after`. The
// returned time is always strictly later than `after` and is computed in
// `after`'s local Location so DST transitions advance correctly (the wall
// clock target is preserved across spring-forward / fall-back: e.g. 09:00 on
// the day of a DST shift always fires at the wall-clock 09:00 of the local
// zone).
func (d DigestSchedule) NextFire(after time.Time) time.Time {
	loc := after.Location()
	year, month, day := after.Date()
	candidate := time.Date(year, month, day, d.Hour, d.Minute, 0, 0, loc)

	if d.Daily {
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate
	}

	// Weekly: advance to the target weekday.
	delta := int(d.Weekday) - int(candidate.Weekday())
	if delta < 0 {
		delta += 7
	}
	candidate = candidate.AddDate(0, 0, delta)
	if !candidate.After(after) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate
}

// PreviousFire returns the most recent past fire time at-or-before `at`. Used
// by the catch-up path: on daemon startup we look for a missed fire within
// the last 24h.
func (d DigestSchedule) PreviousFire(at time.Time) time.Time {
	loc := at.Location()
	year, month, day := at.Date()
	candidate := time.Date(year, month, day, d.Hour, d.Minute, 0, 0, loc)

	if d.Daily {
		if candidate.After(at) {
			candidate = candidate.AddDate(0, 0, -1)
		}
		return candidate
	}

	delta := int(candidate.Weekday()) - int(d.Weekday)
	if delta < 0 {
		delta += 7
	}
	candidate = candidate.AddDate(0, 0, -delta)
	if candidate.After(at) {
		candidate = candidate.AddDate(0, 0, -7)
	}
	return candidate
}
