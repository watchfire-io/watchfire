package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// digestBodyPreview is the maximum number of bytes from the rendered Markdown
// digest copied into the Notification.Body. Short enough to render cleanly in
// macOS Notification Center, long enough to give the user a meaningful preview.
const digestBodyPreview = 280

// digestCatchupWindow is the maximum lookback when the daemon starts up after
// a missed fire. Anything older than 24h is skipped — a long sleep shouldn't
// queue up a stale digest the user will ignore.
const digestCatchupWindow = 24 * time.Hour

// digestRunner schedules and emits WEEKLY_DIGEST notifications. The runner
// owns a single re-armable timer that fires at the configured local-time
// cadence; on each fire it renders a Markdown summary of the past 7 days,
// persists it to ~/.watchfire/digests/<YYYY-MM-DD>.md, and emits via the
// notify.Bus through the same gating as TASK_FAILED / RUN_COMPLETE.
//
// Schedule is recomputed each tick (rather than ticking on a fixed interval)
// so DST transitions don't drift: if the user picks "MON 09:00" and DST
// shifts the wall clock, the next fire still lands at 09:00 on Monday in
// the local zone.
type digestRunner struct {
	bus *notify.Bus

	// now / sleep are factored out so tests can drive the scheduler with a
	// mock clock without races. In production now=time.Now and sleep is a
	// time.Timer-backed wait honouring stop signals.
	now   func() time.Time
	sleep func(d time.Duration, stop <-chan struct{}) bool

	// Last fire timestamp, used to suppress duplicate emits inside the
	// catch-up window.
	mu       sync.Mutex
	lastFire time.Time

	stopOnce sync.Once
	stopCh   chan struct{}
}

// newDigestRunner constructs a digestRunner. The runner is dormant until
// Start() is called.
func newDigestRunner(bus *notify.Bus) *digestRunner {
	return &digestRunner{
		bus:    bus,
		now:    time.Now,
		sleep:  defaultSleep,
		stopCh: make(chan struct{}),
	}
}

func defaultSleep(d time.Duration, stop <-chan struct{}) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}

// Start runs the scheduler in a goroutine. Stop() returns the goroutine.
func (r *digestRunner) Start() {
	go r.run()
}

// Stop cancels the next scheduled fire. Safe to call multiple times.
func (r *digestRunner) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}

func (r *digestRunner) run() {
	// On startup, check whether we missed a fire inside the past 24h and
	// catch up if so. A missed fire older than 24h is dropped (catch-up
	// window) — the next scheduled slot fires fresh.
	r.maybeCatchUp()

	for {
		schedule, ok := r.currentSchedule()
		if !ok {
			// Schedule string was malformed; ParseDigestSchedule returns the
			// fallback default but flags it. Log once and use the fallback.
			log.Printf("[digest] malformed schedule, falling back to %s", models.DefaultDigestSchedule)
		}
		next := schedule.NextFire(r.now())
		wait := next.Sub(r.now())
		if !r.sleep(wait, r.stopCh) {
			return
		}
		r.fire(r.now())
	}
}

// maybeCatchUp checks whether the most recent scheduled fire happened in the
// past 24 hours but the daemon was offline at that moment. If so, fire once
// now to catch up. Skipped if a digest for that calendar date already exists
// on disk so a daemon restart inside the same day doesn't double-fire.
func (r *digestRunner) maybeCatchUp() {
	schedule, _ := r.currentSchedule()
	now := r.now()
	prev := schedule.PreviousFire(now)
	age := now.Sub(prev)
	if age <= 0 || age > digestCatchupWindow {
		return
	}
	if r.alreadyPersisted(prev) {
		return
	}
	log.Printf("[digest] catching up missed fire from %s (%.0fh ago)", prev.Format(time.RFC3339), age.Hours())
	r.fire(now)
}

// fire renders + persists + emits a single digest. now is the wall-clock
// stamp used for the persisted filename and the notification timestamp.
func (r *digestRunner) fire(now time.Time) {
	r.mu.Lock()
	if !r.lastFire.IsZero() && now.Sub(r.lastFire) < time.Minute {
		// Defensive: a tick arriving twice in the same minute (e.g. catch-up
		// + scheduled fire racing) should collapse rather than double-emit.
		r.mu.Unlock()
		return
	}
	r.lastFire = now
	r.mu.Unlock()

	settings, _ := config.LoadSettings()
	cfg := models.DefaultNotifications()
	if settings != nil {
		cfg = settings.Defaults.Notifications
	}

	// Always render + persist the digest, even when gates suppress the
	// notification — the user can open it from the GUI's notification center
	// or the saved file under ~/.watchfire/digests/.
	windowEnd := now
	windowStart := windowEnd.AddDate(0, 0, -7)
	body, summary := renderDigestMarkdown(windowStart, windowEnd)

	dateKey := now.Local().Format("2006-01-02")
	path, persistErr := persistDigest(dateKey, body)
	if persistErr != nil {
		log.Printf("[digest] failed to persist digest %s: %v", dateKey, persistErr)
	}

	if !models.ShouldNotify(models.NotificationWeeklyDigest, cfg, false, now.Local()) {
		return
	}

	emittedAt := now.UTC()
	bodyPreview := digestBodyPreviewText(summary, path)
	n := notify.Notification{
		ID:        notify.MakeID(notify.KindWeeklyDigest, "_global", 0, emittedAt),
		Kind:      notify.KindWeeklyDigest,
		ProjectID: "",
		Title:     "Watchfire — your week",
		Body:      bodyPreview,
		EmittedAt: emittedAt,
	}
	if r.bus != nil {
		r.bus.Emit(n)
	}
	if err := notify.AppendGlobalLogLine(n); err != nil {
		log.Printf("[digest] failed to append digests.log: %v", err)
	}
}

// digestBodyPreviewText assembles the notification body: a short preview of
// the rendered digest with the persisted file path appended on its own line
// so the GUI / tray can resolve the full Markdown.
func digestBodyPreviewText(summary, path string) string {
	if len(summary) > digestBodyPreview {
		summary = strings.TrimSpace(summary[:digestBodyPreview]) + "…"
	}
	if path != "" {
		return summary + "\n\n" + path
	}
	return summary
}

// alreadyPersisted reports whether a digest for the calendar date has already
// been written. Used by the catch-up path to avoid double-firing when the
// daemon restarts inside the same day.
func (r *digestRunner) alreadyPersisted(when time.Time) bool {
	dir, err := config.GlobalDigestsDir()
	if err != nil {
		return false
	}
	path := filepath.Join(dir, when.Local().Format("2006-01-02")+".md")
	_, err = os.Stat(path)
	return err == nil
}

// currentSchedule resolves the configured digest schedule from settings,
// falling back to the default on malformed input.
func (r *digestRunner) currentSchedule() (models.DigestSchedule, bool) {
	settings, _ := config.LoadSettings()
	raw := models.DefaultDigestSchedule
	if settings != nil && settings.Defaults.Notifications.DigestSchedule != "" {
		raw = settings.Defaults.Notifications.DigestSchedule
	}
	return models.ParseDigestSchedule(raw)
}

// persistDigest writes the rendered Markdown body to
// ~/.watchfire/digests/<dateKey>.md and returns the absolute path. The file
// is overwritten if a digest for the same date already exists (catch-up path
// won't reach this, but a manual re-trigger should).
func persistDigest(dateKey, body string) (string, error) {
	if err := config.EnsureGlobalDigestsDir(); err != nil {
		return "", err
	}
	dir, err := config.GlobalDigestsDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, dateKey+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// renderDigestMarkdown builds a Markdown digest of activity across every
// registered project in the [windowStart, windowEnd] window. Returns the full
// Markdown body and a short headline summary suitable for the notification
// preview. This is a self-contained renderer; v6.0 task 0058's insights
// helper + task 0059's templates are not yet merged.
func renderDigestMarkdown(windowStart, windowEnd time.Time) (body, summary string) {
	index, _ := config.LoadProjectsIndex()

	type projectStats struct {
		Name      string
		Done      int
		Failed    int
		InFlight  int
		Created   int
		FailedTitles []string
	}

	var projects []projectStats
	totalDone := 0
	totalFailed := 0
	totalCreated := 0
	totalInFlight := 0

	if index != nil {
		for _, entry := range index.Projects {
			tasks, err := config.LoadAllTasks(entry.Path)
			if err != nil {
				continue
			}
			ps := projectStats{Name: entry.Name}
			for _, t := range tasks {
				if t == nil || t.IsDeleted() {
					continue
				}
				if t.CreatedAt.After(windowStart) && !t.CreatedAt.After(windowEnd) {
					ps.Created++
				}
				if t.Status == models.TaskStatusDone && t.CompletedAt != nil &&
					t.CompletedAt.After(windowStart) && !t.CompletedAt.After(windowEnd) {
					if t.Success != nil && *t.Success {
						ps.Done++
					} else {
						ps.Failed++
						if len(ps.FailedTitles) < 3 {
							ps.FailedTitles = append(ps.FailedTitles, t.Title)
						}
					}
				}
				if t.Status == models.TaskStatusReady || t.Status == models.TaskStatusDraft {
					ps.InFlight++
				}
			}
			totalDone += ps.Done
			totalFailed += ps.Failed
			totalCreated += ps.Created
			totalInFlight += ps.InFlight
			projects = append(projects, ps)
		}
	}

	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})

	var b strings.Builder
	fmt.Fprintf(&b, "# Watchfire — your week\n\n")
	fmt.Fprintf(&b, "_Window: %s → %s_\n\n",
		windowStart.Local().Format("Mon, Jan 2"),
		windowEnd.Local().Format("Mon, Jan 2 2006"))

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- **%d** task%s completed\n", totalDone, plural(totalDone))
	fmt.Fprintf(&b, "- **%d** task%s failed\n", totalFailed, plural(totalFailed))
	fmt.Fprintf(&b, "- **%d** task%s created\n", totalCreated, plural(totalCreated))
	fmt.Fprintf(&b, "- **%d** task%s currently in flight\n", totalInFlight, plural(totalInFlight))
	fmt.Fprintf(&b, "- Across **%d** project%s\n\n", len(projects), plural(len(projects)))

	if len(projects) > 0 {
		fmt.Fprintf(&b, "## By project\n\n")
		for _, p := range projects {
			fmt.Fprintf(&b, "### %s\n\n", p.Name)
			fmt.Fprintf(&b, "- %d done · %d failed · %d new · %d in flight\n",
				p.Done, p.Failed, p.Created, p.InFlight)
			if len(p.FailedTitles) > 0 {
				fmt.Fprintf(&b, "- Failed:\n")
				for _, title := range p.FailedTitles {
					fmt.Fprintf(&b, "  - %s\n", title)
				}
			}
			b.WriteString("\n")
		}
	} else {
		fmt.Fprintf(&b, "_No registered projects yet._\n\n")
	}

	summary = fmt.Sprintf("%d done · %d failed · %d new across %d project%s",
		totalDone, totalFailed, totalCreated, len(projects), plural(len(projects)))

	return b.String(), summary
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
