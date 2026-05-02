package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// loadLA returns a fixed America/New_York Location for the DST tests so the
// test result doesn't depend on the host's local zone.
func loadLA(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("could not load test zone: %v", err)
	}
	return loc
}

func TestParseDigestSchedule(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want models.DigestSchedule
	}{
		{"MON 09:00", true, models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}},
		{"mon 09:00", true, models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}},
		{"FRI 17:00", true, models.DigestSchedule{Weekday: time.Friday, Hour: 17, Minute: 0}},
		{"DAILY 09:00", true, models.DigestSchedule{Daily: true, Hour: 9, Minute: 0}},
		{"FRIDAY 17:00", false, models.DigestSchedule{}},
		{"MON 25:00", false, models.DigestSchedule{}},
		{"", false, models.DigestSchedule{}},
		{"weekly", false, models.DigestSchedule{}},
	}
	for _, c := range cases {
		got, ok := models.ParseDigestSchedule(c.in)
		if ok != c.ok {
			t.Errorf("ParseDigestSchedule(%q) ok = %v, want %v", c.in, ok, c.ok)
		}
		if c.ok && got != c.want {
			t.Errorf("ParseDigestSchedule(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TestNextFireDSTSpringForward exercises the spring-forward DST transition
// in America/New_York: at 2026-03-08 02:00 the wall clock jumps to 03:00, so
// a "MON 09:00" schedule on 2026-03-09 should still land at the local 09:00,
// not 08:00. The schedule preserves the wall-clock time-of-day across DST.
func TestNextFireDSTSpringForward(t *testing.T) {
	loc := loadLA(t)
	schedule := models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}

	// Friday 2026-03-06 in New York, well before the DST shift on Sunday 2026-03-08.
	from := time.Date(2026, 3, 6, 12, 0, 0, 0, loc)
	got := schedule.NextFire(from)
	want := time.Date(2026, 3, 9, 9, 0, 0, 0, loc) // Monday after DST
	if !got.Equal(want) {
		t.Errorf("spring-forward: NextFire = %s, want %s", got, want)
	}
	// Confirm wall-clock time-of-day is preserved (the time.Date constructor
	// builds the right offset for that date in that zone).
	if h, m := got.Hour(), got.Minute(); h != 9 || m != 0 {
		t.Errorf("spring-forward: wall-clock = %02d:%02d, want 09:00", h, m)
	}
}

// TestNextFireDSTFallBack exercises the fall-back DST transition: at
// 2026-11-01 02:00 the wall clock falls to 01:00 in America/New_York. A
// MON 09:00 schedule on 2026-11-02 should fire at the local 09:00.
func TestNextFireDSTFallBack(t *testing.T) {
	loc := loadLA(t)
	schedule := models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}

	from := time.Date(2026, 10, 30, 12, 0, 0, 0, loc) // Friday before fall-back
	got := schedule.NextFire(from)
	want := time.Date(2026, 11, 2, 9, 0, 0, 0, loc) // Monday after fall-back
	if !got.Equal(want) {
		t.Errorf("fall-back: NextFire = %s, want %s", got, want)
	}
	if h, m := got.Hour(), got.Minute(); h != 9 || m != 0 {
		t.Errorf("fall-back: wall-clock = %02d:%02d, want 09:00", h, m)
	}
}

// TestNextFireSameDayBeforeTime — when the current time is the target weekday
// but earlier than the target time, the next fire is later that same day.
func TestNextFireSameDayBeforeTime(t *testing.T) {
	loc := loadLA(t)
	schedule := models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}

	from := time.Date(2026, 3, 9, 7, 0, 0, 0, loc) // Monday morning, before 9
	got := schedule.NextFire(from)
	want := time.Date(2026, 3, 9, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("same-day-before: NextFire = %s, want %s", got, want)
	}
}

// TestNextFireSameDayAfterTime — when the current time is the target weekday
// past the target time, the next fire is one week later.
func TestNextFireSameDayAfterTime(t *testing.T) {
	loc := loadLA(t)
	schedule := models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}

	from := time.Date(2026, 3, 9, 9, 0, 1, 0, loc) // Monday, just past 09:00
	got := schedule.NextFire(from)
	want := time.Date(2026, 3, 16, 9, 0, 0, 0, loc) // following Monday
	if !got.Equal(want) {
		t.Errorf("same-day-after: NextFire = %s, want %s", got, want)
	}
}

// TestPreviousFireWeekly — verifies the catch-up helper picks the most recent
// past fire that's within the last week, and not a fire in the future.
func TestPreviousFireWeekly(t *testing.T) {
	loc := loadLA(t)
	schedule := models.DigestSchedule{Weekday: time.Monday, Hour: 9, Minute: 0}

	at := time.Date(2026, 3, 9, 12, 0, 0, 0, loc) // Monday past 09:00
	prev := schedule.PreviousFire(at)
	want := time.Date(2026, 3, 9, 9, 0, 0, 0, loc) // earlier today
	if !prev.Equal(want) {
		t.Errorf("PreviousFire(after-fire) = %s, want %s", prev, want)
	}

	at2 := time.Date(2026, 3, 9, 7, 0, 0, 0, loc) // Monday before 09:00
	prev2 := schedule.PreviousFire(at2)
	want2 := time.Date(2026, 3, 2, 9, 0, 0, 0, loc) // previous Monday
	if !prev2.Equal(want2) {
		t.Errorf("PreviousFire(before-fire) = %s, want %s", prev2, want2)
	}
}

// TestPreviousFireDaily — verifies daily catch-up.
func TestPreviousFireDaily(t *testing.T) {
	loc := loadLA(t)
	schedule := models.DigestSchedule{Daily: true, Hour: 9, Minute: 0}

	at := time.Date(2026, 3, 9, 7, 0, 0, 0, loc)
	prev := schedule.PreviousFire(at)
	want := time.Date(2026, 3, 8, 9, 0, 0, 0, loc) // yesterday
	if !prev.Equal(want) {
		t.Errorf("daily PreviousFire(before-fire) = %s, want %s", prev, want)
	}

	at2 := time.Date(2026, 3, 9, 12, 0, 0, 0, loc)
	prev2 := schedule.PreviousFire(at2)
	want2 := time.Date(2026, 3, 9, 9, 0, 0, 0, loc) // earlier today
	if !prev2.Equal(want2) {
		t.Errorf("daily PreviousFire(after-fire) = %s, want %s", prev2, want2)
	}
}

// TestShouldNotifyWeeklyDigestGate — the gating helper must short-circuit
// when events.weekly_digest is false (default), and pass when toggled on.
func TestShouldNotifyWeeklyDigestGate(t *testing.T) {
	cfg := models.DefaultNotifications()
	now := time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)

	// Default config: weekly_digest is OFF.
	if models.ShouldNotify(models.NotificationWeeklyDigest, cfg, false, now) {
		t.Errorf("default config should suppress weekly digest")
	}

	// Toggle on — passes.
	cfg.Events.WeeklyDigest = true
	if !models.ShouldNotify(models.NotificationWeeklyDigest, cfg, false, now) {
		t.Errorf("weekly_digest enabled should pass the gate")
	}

	// Master toggle off — suppresses.
	cfg.Enabled = false
	if models.ShouldNotify(models.NotificationWeeklyDigest, cfg, false, now) {
		t.Errorf("master toggle off should suppress weekly digest")
	}

	// Reset, then quiet hours — suppresses.
	cfg.Enabled = true
	cfg.QuietHours = models.QuietHoursConfig{Enabled: true, Start: "08:00", End: "10:00"}
	if models.ShouldNotify(models.NotificationWeeklyDigest, cfg, false, now) {
		t.Errorf("quiet hours should suppress weekly digest")
	}
}

// TestNormalizeAddsDigestSchedule — partial settings without a digest_schedule
// pick up the default after Normalize().
func TestNormalizeAddsDigestSchedule(t *testing.T) {
	s := &models.Settings{
		Defaults: models.DefaultsConfig{
			Notifications: models.NotificationsConfig{
				Enabled:    true, // partial block — at least one field set
				QuietHours: models.QuietHoursConfig{Start: "10:00", End: "12:00"},
			},
		},
	}
	s.Normalize()
	if s.Defaults.Notifications.DigestSchedule != models.DefaultDigestSchedule {
		t.Errorf("Normalize should default digest_schedule, got %q", s.Defaults.Notifications.DigestSchedule)
	}
}

// TestRunnerCatchUpInsideWindow — when the daemon starts up within the
// catch-up window AND no digest exists for the missed date, the runner should
// emit immediately. Outside the window: no emit.
func TestRunnerCatchUpInsideWindow(t *testing.T) {
	withTempDigestsDir(t)
	withTempLogsDir(t)
	withSettings(t, func(s *models.Settings) {
		s.Defaults.Notifications.Events.WeeklyDigest = true
	})

	loc := time.Local
	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	// Schedule MON 09:00, simulated "now" Monday 11:00 → missed 09:00 fire 2h ago.
	r := newDigestRunnerForTest(bus)
	now := nextMondayLocal(loc, 11, 0)
	// nextMondayLocal already adds +2h, so we want the literal Monday-11:00.
	now = now.Add(-2 * time.Hour)
	r.now = func() time.Time { return now }
	r.maybeCatchUp()

	select {
	case <-ch:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Errorf("expected catch-up emit, got none")
	}

	// Second invocation must NOT re-emit (the file now exists on disk).
	r.lastFire = time.Time{} // reset the dedupe so the test gates on alreadyPersisted
	r.maybeCatchUp()
	select {
	case <-ch:
		t.Errorf("second catch-up should not re-emit (already persisted)")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestRunnerCatchUpOutsideWindow(t *testing.T) {
	withTempDigestsDir(t)

	loc := time.Local
	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	r := newDigestRunnerForTest(bus)
	// "Now" is Tuesday 11:00, the previous fire was Monday 09:00 — 26h ago,
	// outside the 24h catch-up window.
	now := nextMondayLocal(loc, 9, 0).Add(26 * time.Hour)
	r.now = func() time.Time { return now }
	r.maybeCatchUp()

	select {
	case <-ch:
		t.Errorf("catch-up outside the 24h window should NOT emit")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

// TestRunnerGatingSuppressesEmit — when the master toggle is off (default),
// the runner persists the digest but does not emit on the bus.
func TestRunnerGatingSuppressesEmit(t *testing.T) {
	dir := withTempDigestsDir(t)
	withTempLogsDir(t)

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	r := newDigestRunnerForTest(bus)
	now := time.Now()
	r.now = func() time.Time { return now }

	r.fire(now)

	select {
	case <-ch:
		t.Errorf("default config has weekly_digest=false; emit should be suppressed")
	case <-time.After(50 * time.Millisecond):
		// expected — default off
	}

	// File still persisted despite suppressed emit.
	persisted := filepath.Join(dir, now.Local().Format("2006-01-02")+".md")
	if _, err := os.Stat(persisted); err != nil {
		t.Errorf("expected digest file at %s, got %v", persisted, err)
	}
}

// TestRunnerGatingPassesWithToggleOn — flipping the gate ON via settings
// should produce an emit on next fire.
func TestRunnerGatingPassesWithToggleOn(t *testing.T) {
	withTempDigestsDir(t)
	withTempLogsDir(t)
	withSettings(t, func(s *models.Settings) {
		s.Defaults.Notifications.Events.WeeklyDigest = true
	})

	bus := notify.NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	r := newDigestRunnerForTest(bus)
	now := time.Now()
	r.now = func() time.Time { return now }
	r.fire(now)

	select {
	case n := <-ch:
		if n.Kind != notify.KindWeeklyDigest {
			t.Errorf("expected WEEKLY_DIGEST, got %s", n.Kind)
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("toggle on should produce emit")
	}
}

// === test helpers ===

// newDigestRunnerForTest returns a runner with a deterministic clock + sleep
// and a fresh stop channel.
func newDigestRunnerForTest(bus *notify.Bus) *digestRunner {
	r := newDigestRunner(bus)
	// Replace sleep with a channel-driven fake so tests don't actually wait.
	r.sleep = func(time.Duration, <-chan struct{}) bool { return true }
	return r
}

// nextMondayLocal returns a time at the next Monday at the given hour:minute
// in the given Location, computed against time.Now() so the test isn't tied
// to a specific calendar date. The returned time is always at-or-past the
// scheduled MON HH:MM mark by a couple hours so PreviousFire lands on it.
func nextMondayLocal(loc *time.Location, hour, minute int) time.Time {
	now := time.Now().In(loc)
	delta := int(time.Monday) - int(now.Weekday())
	if delta < 0 {
		delta += 7
	}
	candidate := time.Date(now.Year(), now.Month(), now.Day()+delta, hour, minute, 0, 0, loc)
	// Add 2 hours so the candidate is past the scheduled fire and represents
	// "we're 2h after the missed fire."
	return candidate.Add(2 * time.Hour)
}

// withTempDigestsDir redirects ~/.watchfire to a temp dir for the test by
// setting HOME. Returns the path of the digests/ dir inside the temp HOME.
func withTempDigestsDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".watchfire", "digests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir digests: %v", err)
	}
	return dir
}

func withTempLogsDir(t *testing.T) string {
	t.Helper()
	tmp := os.Getenv("HOME")
	if tmp == "" {
		tmp = t.TempDir()
		t.Setenv("HOME", tmp)
	}
	dir := filepath.Join(tmp, ".watchfire", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	return dir
}

func withSettings(t *testing.T, mutate func(*models.Settings)) {
	t.Helper()
	tmp := os.Getenv("HOME")
	if tmp == "" {
		t.Fatalf("withSettings requires HOME to be set first (call withTempDigestsDir)")
	}
	settingsDir := filepath.Join(tmp, ".watchfire")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}
	s := models.NewSettings()
	mutate(s)
	// Use a YAML marshaler from the config package via SaveSettings — but
	// since that depends on resolving the path through HOME, just write the
	// minimal subset by hand.
	data := []byte("version: 1\ndefaults:\n  notifications:\n    enabled: true\n    events:\n      task_failed: true\n      run_complete: true\n      weekly_digest: " + boolStr(s.Defaults.Notifications.Events.WeeklyDigest) + "\n    sounds:\n      enabled: true\n      task_failed: true\n      run_complete: true\n      volume: 0.6\n    quiet_hours:\n      enabled: false\n      start: \"22:00\"\n      end: \"08:00\"\n    digest_schedule: \"" + s.Defaults.Notifications.DigestSchedule + "\"\n")
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.yaml"), data, 0o644); err != nil {
		t.Fatalf("write settings.yaml: %v", err)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
