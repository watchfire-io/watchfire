package telegram

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// fakeSessions is a scripted SessionSource.
type fakeSessions struct {
	mu      sync.Mutex
	sess    map[string]*WatchedSession
	out     map[int]TaskOutcome
	created []TaskSummary // returned by TasksCreatedSince
}

func (f *fakeSessions) TasksCreatedSince(string, time.Time) []TaskSummary {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TaskSummary(nil), f.created...)
}

func (f *fakeSessions) ActiveSession(projectID string) (*WatchedSession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sess[projectID]
	return s, ok
}

func (f *fakeSessions) ActiveProjects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sess))
	for pid := range f.sess {
		out = append(out, pid)
	}
	return out
}

func (f *fakeSessions) TaskOutcome(_ string, taskNumber int) (TaskOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	oc, ok := f.out[taskNumber]
	if !ok {
		return TaskOutcome{}, fmt.Errorf("no outcome for task %d", taskNumber)
	}
	return oc, nil
}

// speedUp shrinks every watch-mode interval so the relay runs at test
// speed while exercising the production code paths.
func speedUp(b *Bridge) {
	b.watchPoll = 5 * time.Millisecond
	b.flushEvery = 2 * time.Millisecond
	b.coalesceEvery = 5 * time.Millisecond
	b.screenEvery = 5 * time.Millisecond
	b.tailPoll = 2 * time.Millisecond
	b.outcomeRetry = time.Millisecond
	b.sayEnterDelay = time.Millisecond
}

func watchingChat42(projectID string) []models.TelegramPairedChat {
	return []models.TelegramPairedChat{{
		ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now(),
		DefaultProjectID: projectID,
	}}
}

// sentContaining returns whether any sent message contains needle.
func sentContaining(f *fakeBotAPI, needle string) bool {
	for _, m := range f.sentMessages() {
		if strings.Contains(m.Text, needle) {
			return true
		}
	}
	return false
}

// sentOrderIndex returns the index of the first sent message containing
// needle, or -1.
func sentOrderIndex(f *fakeBotAPI, needle string) int {
	for i, m := range f.sentMessages() {
		if strings.Contains(m.Text, needle) {
			return i
		}
	}
	return -1
}

// TestWatchRelayTranscriptEndToEnd: a Claude Code session relays the
// start marker, assistant text, tool one-liners, and the outcome marker
// to a watching chat, in transcript order.
func TestWatchRelayTranscriptEndToEnd(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	appendFile(t, path, `{"type":"custom-title","customTitle":"proj:0007"}`+"\n")

	done := make(chan struct{})
	sess := &WatchedSession{
		ProjectID: "p1", ProjectPath: "/tmp/proj", Mode: "task",
		TaskNumber: 7, TaskTitle: "Fix the flux capacitor", BackendName: "claude-code",
		StartedAt: time.Now(), Done: done,
	}
	fs := &fakeSessions{
		sess: map[string]*WatchedSession{"p1": sess},
		out:  map[int]TaskOutcome{7: {Done: true, Success: true, Merged: true}},
	}
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: watchingChat42("p1"),
		Sessions:    fs,
	})
	speedUp(b)
	b.tailerForFn = func(*WatchedSession) (TailableTranscript, bool) {
		return &claudeTranscript{locateFn: func() (string, error) { return path, nil }}, true
	}
	startBridge(t, b)

	waitFor(t, "start marker", func() bool { return sentContaining(fake, "▶ task 0007 — Fix the flux capacitor") })

	appendFile(t, path, assistantTextLine("Let me look at the code.")+"\n")
	waitFor(t, "assistant text", func() bool { return sentContaining(fake, "Let me look at the code.") })

	appendFile(t, path, toolUseLine("Edit", map[string]any{"file_path": "internal/tui/model.go"})+"\n")
	appendFile(t, path, toolUseLine("Bash", map[string]any{"command": "make test"})+"\n")
	waitFor(t, "tool one-liners", func() bool {
		return sentContaining(fake, "⚒ Edit internal/tui/model.go") && sentContaining(fake, "⚒ Bash: make test")
	})

	close(done)
	waitFor(t, "outcome marker", func() bool { return sentContaining(fake, "✔ task 0007 merged") })

	// Ordering: marker → text → tools → outcome, by message position.
	idx := []int{
		sentOrderIndex(fake, "▶ task 0007"),
		sentOrderIndex(fake, "Let me look at the code."),
		sentOrderIndex(fake, "⚒ Edit internal/tui/model.go"),
		sentOrderIndex(fake, "✔ task 0007 merged"),
	}
	for i := 1; i < len(idx); i++ {
		if idx[i-1] < 0 || idx[i] < 0 || idx[i-1] > idx[i] {
			t.Fatalf("relay out of order: indices %v in %+v", idx, fake.sentMessages())
		}
	}
	// Everything went to the watching chat.
	for _, m := range fake.sentMessages() {
		if m.ChatID != "42" {
			t.Fatalf("relay leaked to chat %s: %q", m.ChatID, m.Text)
		}
	}
}

// TestWatchRelayFailureMarker: a failed task relays the failure reason.
func TestWatchRelayFailureMarker(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	done := make(chan struct{})
	sess := &WatchedSession{
		ProjectID: "p1", ProjectPath: "/tmp/proj", Mode: "task",
		TaskNumber: 9, TaskTitle: "Doomed", BackendName: "claude-code",
		StartedAt: time.Now(), Done: done,
	}
	fs := &fakeSessions{
		sess: map[string]*WatchedSession{"p1": sess},
		out:  map[int]TaskOutcome{9: {Done: true, Success: false, FailureReason: "tests kept failing"}},
	}
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: watchingChat42("p1"), Sessions: fs,
	})
	speedUp(b)
	b.tailerForFn = func(*WatchedSession) (TailableTranscript, bool) {
		return &claudeTranscript{locateFn: func() (string, error) { return path, nil }}, true
	}
	startBridge(t, b)

	waitFor(t, "start marker", func() bool { return sentContaining(fake, "▶ task 0009 — Doomed") })
	close(done)
	waitFor(t, "failure marker", func() bool { return sentContaining(fake, "✖ task 0009 failed: tests kept failing") })
}

// TestWatchRelayScreenDeltaFallback: a backend without a tailable
// transcript falls back to debounced <pre> screen deltas — sent only on
// change — and a non-task session draws no task markers.
func TestWatchRelayScreenDeltaFallback(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t)

	done := make(chan struct{})
	var mu sync.Mutex
	lines := []string{"$ starting up", ""}
	sess := &WatchedSession{
		ProjectID: "p1", ProjectPath: "/tmp/proj", Mode: "chat",
		BackendName: "codex", StartedAt: time.Now(), Done: done,
		Snapshot: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), lines...)
		},
	}
	fs := &fakeSessions{sess: map[string]*WatchedSession{"p1": sess}}
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: watchingChat42("p1"), Sessions: fs,
	})
	speedUp(b)
	// Default tailerForFn: codex has no tailer → tier 2.
	startBridge(t, b)

	waitFor(t, "first screen delta", func() bool { return sentContaining(fake, "$ starting up") })
	first := fake.sentMessages()
	if len(first) != 1 || !strings.HasPrefix(first[0].Text, "<pre>") || !strings.HasSuffix(first[0].Text, "</pre>") {
		t.Fatalf("screen delta should be one <pre> block: %+v", first)
	}

	// Unchanged content across many polls — nothing resent.
	time.Sleep(60 * time.Millisecond)
	if n := len(fake.sentMessages()); n != 1 {
		t.Fatalf("unchanged screen resent: %d messages", n)
	}

	mu.Lock()
	lines = []string{"$ starting up", "$ compiling…", ""}
	mu.Unlock()
	waitFor(t, "changed screen delta", func() bool { return sentContaining(fake, "compiling") })

	close(done)
	time.Sleep(30 * time.Millisecond)
	for _, m := range fake.sentMessages() {
		if strings.Contains(m.Text, "▶ task") || strings.Contains(m.Text, "session ended") {
			t.Fatalf("chat-mode session must not draw task markers: %q", m.Text)
		}
	}
}

// TestWatchRelayStopsWhenWatchTurnsOff: /watch off cancels the running
// relay; new transcript lines stop flowing to the chat.
func TestWatchRelayStopsWhenWatchTurnsOff(t *testing.T) {
	withTestEnv(t)

	// Seed a persisted paired chat so /watch off has a record to update.
	seed := &models.IntegrationsConfig{Telegram: &models.TelegramConfig{
		Enabled:     true,
		PairedChats: watchingChat42("p1"),
	}}
	if err := config.SaveIntegrations(seed); err != nil {
		t.Fatalf("seed integrations: %v", err)
	}

	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/watch off"))
	path := filepath.Join(t.TempDir(), "session.jsonl")
	done := make(chan struct{})
	defer close(done)
	sess := &WatchedSession{
		ProjectID: "p1", ProjectPath: "/tmp/proj", Mode: "task",
		TaskNumber: 3, TaskTitle: "Long haul", BackendName: "claude-code",
		StartedAt: time.Now(), Done: done,
	}
	fs := &fakeSessions{sess: map[string]*WatchedSession{"p1": sess}}
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: seed.Telegram.PairedChats, Sessions: fs,
	})
	speedUp(b)
	b.tailerForFn = func(*WatchedSession) (TailableTranscript, bool) {
		return &claudeTranscript{locateFn: func() (string, error) { return path, nil }}, true
	}
	startBridge(t, b)

	waitFor(t, "start marker", func() bool { return sentContaining(fake, "▶ task 0003") })
	waitFor(t, "watch-off ack", func() bool { return sentContaining(fake, "Watch is <b>off</b>") })
	waitFor(t, "relay wound down", func() bool {
		b.watchMu.Lock()
		defer b.watchMu.Unlock()
		return len(b.relays) == 0
	})

	// Lines appended after the toggle never reach the chat.
	before := len(fake.sentMessages())
	appendFile(t, path, assistantTextLine("You should not see this.")+"\n")
	time.Sleep(40 * time.Millisecond)
	if sentContaining(fake, "You should not see this.") {
		t.Fatal("relay kept streaming after /watch off")
	}
	if n := len(fake.sentMessages()); n != before {
		t.Fatalf("unexpected extra messages after /watch off: %d → %d", before, n)
	}
	// And the toggle persisted.
	cfg, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if cfg.Telegram.PairedChats[0].Watching() {
		t.Fatal("/watch off did not persist")
	}
}

// TestWatchCommandTogglePersists: /watch on persists on the paired-chat
// record and survives a bridge restart (a fresh bridge seeded from the
// reloaded config carries the flag).
func TestWatchCommandTogglePersists(t *testing.T) {
	withTestEnv(t)
	seed := &models.IntegrationsConfig{Telegram: &models.TelegramConfig{
		Enabled: true,
		PairedChats: []models.TelegramPairedChat{
			{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now(), DefaultProjectID: "p1"},
		},
	}}
	if err := config.SaveIntegrations(seed); err != nil {
		t.Fatalf("seed integrations: %v", err)
	}

	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/watch on"))
	b := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h", PairedChats: seed.Telegram.PairedChats})
	startBridge(t, b)

	waitFor(t, "watch-on ack", func() bool { return sentContaining(fake, "Watch is <b>on</b>") })
	cfg, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if len(cfg.Telegram.PairedChats) != 1 || !cfg.Telegram.PairedChats[0].Watching() {
		t.Fatalf("watch flag not persisted: %+v", cfg.Telegram.PairedChats)
	}

	// "Daemon restart": a fresh bridge built from the persisted config
	// starts with the chat already watching.
	b2 := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h", PairedChats: cfg.Telegram.PairedChats})
	b2.mu.Lock()
	restored := b2.paired[42].Watching()
	b2.mu.Unlock()
	if !restored {
		t.Fatal("restarted bridge lost the watch flag")
	}
}

// TestWatchCommandStateAndUsage: bare /watch reports the current state
// — ON by default (WatchOff zero value) — and junk arguments draw usage.
func TestWatchCommandStateAndUsage(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/watch"),
		updateJSON(2, 42, 42, "nuno", "/watch maybe"),
	)
	b := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: []models.TelegramPairedChat{{ChatID: 42, UserID: 42, PairedAt: time.Now()}}})
	startBridge(t, b)

	waitFor(t, "two replies", func() bool { return len(fake.sentMessages()) >= 2 })
	sent := fake.sentMessages()
	if !strings.Contains(sent[0].Text, "Watch is <b>on</b>") || !strings.Contains(sent[0].Text, "/watch on|off") {
		t.Fatalf("bare /watch should report state + usage: %q", sent[0].Text)
	}
	if !strings.Contains(sent[1].Text, "Usage: /watch on|off") {
		t.Fatalf("junk argument should draw usage: %q", sent[1].Text)
	}
}

// TestStartMarker: wildfire sessions get phase-specific milestone
// markers; task sessions name the task; plain chat opens silently.
func TestStartMarker(t *testing.T) {
	cases := []struct {
		sess WatchedSession
		want string
	}{
		{WatchedSession{Mode: "wildfire", Phase: "generate"}, "🔥 wildfire — generating new tasks…"},
		{WatchedSession{Mode: "wildfire", Phase: "refine"}, "🔥 wildfire — reviewing the plan and refining the backlog…"},
		{WatchedSession{Mode: "wildfire", Phase: "execute", TaskNumber: 7, TaskTitle: "Ship"}, "🔥 wildfire — implementing task 0007 — Ship"},
		{WatchedSession{Mode: "task", TaskNumber: 9, TaskTitle: "Fix"}, "▶ task 0009 — Fix"},
		{WatchedSession{Mode: "chat"}, ""},
	}
	for _, c := range cases {
		if got := startMarker(&c.sess); got != c.want {
			t.Errorf("startMarker(%+v) = %q, want %q", c.sess, got, c.want)
		}
	}
}

// TestWildfireMilestoneRelay: a wildfire planning-phase session relays
// milestones only — no raw screen stream — closing with the tasks it
// generated; and the typing indicator fires while the relay is active.
func TestWildfireMilestoneRelay(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t)

	done := make(chan struct{})
	sess := &WatchedSession{
		ProjectID: "p1", ProjectPath: "/tmp/proj", Mode: "wildfire", Phase: "generate",
		BackendName: "claude-code", StartedAt: time.Now(), Done: done,
		Snapshot: func() []string { return []string{"raw screen noise"} },
	}
	fs := &fakeSessions{
		sess:    map[string]*WatchedSession{"p1": sess},
		created: []TaskSummary{{Number: 151, Title: "Do X"}, {Number: 152, Title: "Do Y"}},
	}
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: watchingChat42("p1"), Sessions: fs,
	})
	speedUp(b)
	b.typingEvery = 3 * time.Millisecond
	startBridge(t, b)

	waitFor(t, "generate start marker", func() bool { return sentContaining(fake, "generating new tasks") })
	waitFor(t, "typing action", func() bool { return fake.chatActionCount() >= 1 })
	close(done)
	waitFor(t, "generated task markers", func() bool {
		return sentContaining(fake, "✚ generated task 0151 — Do X") && sentContaining(fake, "✚ generated task 0152 — Do Y")
	})
	for _, m := range fake.sentMessages() {
		if strings.Contains(m.Text, "raw screen noise") || strings.HasPrefix(m.Text, "<pre>") {
			t.Fatalf("wildfire planning phases must not stream the raw screen: %q", m.Text)
		}
	}
}
