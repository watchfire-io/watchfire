package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/models"
)

// fakeCommandContextFor returns a factory whose FindProjects /
// ListTopActiveTasks are backed by the given fixtures — the echo
// router and the bridge see exactly the shape the production
// (task-0133) callbacks produce.
func fakeCommandContextFor(projects []echo.ProjectInfo, tasksByProject map[string][]*models.Task) CommandContextFactory {
	return func(chatID, userID int64) echo.CommandContext {
		return echo.CommandContext{
			UserID: fmt.Sprint(userID),
			Now:    func() time.Time { return time.Unix(1700000000, 0).UTC() },
			FindProjects: func(ctx context.Context) ([]echo.ProjectInfo, error) {
				return append([]echo.ProjectInfo(nil), projects...), nil
			},
			ListTopActiveTasks: func(ctx context.Context, projectID string, limit int) ([]*models.Task, error) {
				tasks := tasksByProject[projectID]
				if limit > 0 && len(tasks) > limit {
					tasks = tasks[:limit]
				}
				return tasks, nil
			},
		}
	}
}

// recordedDefaults swaps the bridge's persist hook for an in-memory
// recorder (most command tests don't need the real YAML round-trip).
func recordedDefaults(b *Bridge) *sync.Map {
	var m sync.Map
	b.setDefaultFn = func(chatID int64, projectID string) error {
		m.Store(chatID, projectID)
		return nil
	}
	return &m
}

func pairedChat42() []models.TelegramPairedChat {
	return []models.TelegramPairedChat{
		{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now()},
	}
}

var testProjects = []echo.ProjectInfo{
	{ID: "p-alpha", Name: "Alpha", AgentRunning: true, AgentTaskNumber: 7},
	{ID: "p-evil", Name: "Evil <script>alert(1)</script> & Co"},
	{ID: "p-beta", Name: "Beta"},
}

// TestBridgeSetMyCommandsOnStart: Run registers the live command set
// for Telegram's autocomplete before polling.
func TestBridgeSetMyCommandsOnStart(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t)
	b := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h"})
	startBridge(t, b)

	waitFor(t, "setMyCommands", func() bool { return len(fake.recordedMyCommands()) >= 1 })
	got := fake.recordedMyCommands()[0]
	for _, cmd := range []string{
		"projects", "use", "status", "tasks",
		"run", "runall", "retry", "cancel", "screen", "say", "watch", "mute", "unmute",
		"help", "pair",
	} {
		if !strings.Contains(got, `"`+cmd+`"`) {
			t.Fatalf("setMyCommands payload missing %q: %s", cmd, got)
		}
	}
}

// TestBridgeProjectsCommand: numbered list with status glyphs, escaped
// names, and an inline keyboard whose callback data carries project ids.
func TestBridgeProjectsCommand(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/projects"))
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	recordedDefaults(b)
	startBridge(t, b)

	waitFor(t, "projects reply", func() bool { return len(fake.sentMessages()) >= 1 })
	sent := fake.sentMessages()[0]
	if sent.ChatID != "42" {
		t.Fatalf("reply went to chat %s", sent.ChatID)
	}
	if !strings.Contains(sent.Text, "1. 🟢 Alpha") {
		t.Fatalf("running project missing numbered row + glyph: %q", sent.Text)
	}
	if !strings.Contains(sent.Text, "3. ⚪ Beta") {
		t.Fatalf("idle project missing numbered row + glyph: %q", sent.Text)
	}
	if strings.Contains(sent.Text, "<script>") {
		t.Fatalf("project name not HTML-escaped: %q", sent.Text)
	}
	if !strings.Contains(sent.Text, "Evil &lt;script&gt;alert(1)&lt;/script&gt; &amp; Co") {
		t.Fatalf("escaped project name missing: %q", sent.Text)
	}
	for _, data := range []string{`"use:p-alpha"`, `"use:p-evil"`, `"use:p-beta"`} {
		if !strings.Contains(sent.ReplyMarkup, data) {
			t.Fatalf("inline keyboard missing callback data %s: %s", data, sent.ReplyMarkup)
		}
	}
}

// TestBridgeUseByNameFuzzy: a case-insensitive prefix picks the
// project, the selection is persisted and mirrored in memory.
func TestBridgeUseByNameFuzzy(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/use bet"))
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	saved := recordedDefaults(b)
	startBridge(t, b)

	waitFor(t, "use confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "<b>Beta</b>") {
		t.Fatalf("confirmation should name Beta: %q", txt)
	}
	if got, _ := saved.Load(int64(42)); got != "p-beta" {
		t.Fatalf("persisted default = %v, want p-beta", got)
	}
	b.mu.Lock()
	inMem := b.paired[42].DefaultProjectID
	b.mu.Unlock()
	if inMem != "p-beta" {
		t.Fatalf("in-memory default = %q, want p-beta", inMem)
	}
}

// TestBridgeUseByNumber: "/use 3" indexes the last /projects listing.
func TestBridgeUseByNumber(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/projects"),
		updateJSON(2, 42, 42, "nuno", "/use 3"),
	)
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	saved := recordedDefaults(b)
	startBridge(t, b)

	waitFor(t, "two replies", func() bool { return len(fake.sentMessages()) >= 2 })
	if txt := fake.sentMessages()[1].Text; !strings.Contains(txt, "<b>Beta</b>") {
		t.Fatalf("/use 3 should pick Beta: %q", txt)
	}
	if got, _ := saved.Load(int64(42)); got != "p-beta" {
		t.Fatalf("persisted default = %v, want p-beta", got)
	}
}

// TestBridgeUseErrors: missing arg, no match, out-of-range number, and
// an ambiguous name each draw a distinct friendly reply.
func TestBridgeUseErrors(t *testing.T) {
	withTestEnv(t)
	ambiguous := []echo.ProjectInfo{
		{ID: "p1", Name: "Web App"},
		{ID: "p2", Name: "Web API"},
	}
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/use"),
		updateJSON(2, 42, 42, "nuno", "/use nosuch"),
		updateJSON(3, 42, 42, "nuno", "/use 9"),
		updateJSON(4, 42, 42, "nuno", "/use web"),
	)
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(ambiguous, nil),
	})
	saved := recordedDefaults(b)
	startBridge(t, b)

	waitFor(t, "four replies", func() bool { return len(fake.sentMessages()) >= 4 })
	sent := fake.sentMessages()
	if !strings.Contains(sent[0].Text, "Usage: /use") {
		t.Fatalf("missing-arg reply: %q", sent[0].Text)
	}
	if !strings.Contains(sent[1].Text, "No project matches") {
		t.Fatalf("no-match reply: %q", sent[1].Text)
	}
	if !strings.Contains(sent[2].Text, "No project number 9") {
		t.Fatalf("bad-number reply: %q", sent[2].Text)
	}
	if !strings.Contains(sent[3].Text, "Ambiguous") || !strings.Contains(sent[3].Text, "Web App") {
		t.Fatalf("ambiguous reply: %q", sent[3].Text)
	}
	if _, ok := saved.Load(int64(42)); ok {
		t.Fatal("a failed /use persisted a selection")
	}
}

// TestBridgeInlineUseCallback: an inline-button tap answers the
// callback query, persists the selection, and confirms in chat.
func TestBridgeInlineUseCallback(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, callbackJSON(1, 42, 42, "cb-1", "use:p-beta"))
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	saved := recordedDefaults(b)
	startBridge(t, b)

	waitFor(t, "callback answered", func() bool { return len(fake.answeredCallbacks()) >= 1 })
	waitFor(t, "confirmation message", func() bool { return len(fake.sentMessages()) >= 1 })
	cb := fake.answeredCallbacks()[0]
	if cb.ID != "cb-1" || !strings.Contains(cb.Text, "Beta") {
		t.Fatalf("answerCallbackQuery = %+v", cb)
	}
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "<b>Beta</b>") {
		t.Fatalf("confirmation: %q", txt)
	}
	if got, _ := saved.Load(int64(42)); got != "p-beta" {
		t.Fatalf("persisted default = %v, want p-beta", got)
	}
}

// TestBridgeCallbackFromUnpairedChatSilent: a forged callback from an
// unpaired chat is acknowledged blank (spinner stops) but leaks
// nothing and persists nothing.
func TestBridgeCallbackFromUnpairedChatSilent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, callbackJSON(1, 99, 99, "cb-x", "use:p-alpha"))
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	saved := recordedDefaults(b)
	startBridge(t, b)

	waitFor(t, "callback answered", func() bool { return len(fake.answeredCallbacks()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if cb := fake.answeredCallbacks()[0]; cb.Text != "" {
		t.Fatalf("unpaired callback answered with content: %+v", cb)
	}
	if sent := fake.sentMessages(); len(sent) != 0 {
		t.Fatalf("unpaired callback drew messages: %+v", sent)
	}
	if _, ok := saved.Load(int64(99)); ok {
		t.Fatal("unpaired callback persisted a selection")
	}
}

// TestBridgeStatusWithoutProjectPrompts: /status before any /use
// points at project selection instead of routing.
func TestBridgeStatusWithoutProjectPrompts(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/status"))
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	startBridge(t, b)

	waitFor(t, "prompt reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "/use") || !strings.Contains(txt, "/projects") {
		t.Fatalf("no-project prompt should point at /projects + /use: %q", txt)
	}
	if strings.Contains(txt, "Alpha") {
		t.Fatalf("prompt leaked project data: %q", txt)
	}
}

// TestBridgeStatusRoutesThroughEchoRoute: with a project selected,
// /status renders the shared echo status handler's response as HTML.
func TestBridgeStatusRoutesThroughEchoRoute(t *testing.T) {
	withTestEnv(t)
	started := time.Unix(1700000000, 0).Add(-5 * time.Minute).UTC()
	tasks := map[string][]*models.Task{
		"p-alpha": {
			{TaskNumber: 7, Title: "Wire the <bridge>", Status: models.TaskStatusReady, StartedAt: &started},
		},
	}
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/status"))
	chats := pairedChat42()
	chats[0].DefaultProjectID = "p-alpha"
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       chats,
		CommandContextFor: fakeCommandContextFor(testProjects, tasks),
	})
	startBridge(t, b)

	waitFor(t, "status reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "<b>Watchfire — current status</b>") {
		t.Fatalf("status header missing (echo.Route not used?): %q", txt)
	}
	if !strings.Contains(txt, "Alpha") || !strings.Contains(txt, "#0007") {
		t.Fatalf("status body missing project/task: %q", txt)
	}
	if strings.Contains(txt, "<bridge>") {
		t.Fatalf("task title not escaped: %q", txt)
	}
}

// TestBridgeTasksCommand: numbered top-active-task list with status
// glyphs; the in-flight task is marked distinctly from queued ones.
func TestBridgeTasksCommand(t *testing.T) {
	withTestEnv(t)
	tasks := map[string][]*models.Task{
		"p-alpha": {
			{TaskNumber: 7, Title: "Running task", Status: models.TaskStatusReady},
			{TaskNumber: 9, Title: "Queued <task>", Status: models.TaskStatusReady},
		},
	}
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/tasks"))
	chats := pairedChat42()
	chats[0].DefaultProjectID = "p-alpha"
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       chats,
		CommandContextFor: fakeCommandContextFor(testProjects, tasks),
	})
	startBridge(t, b)

	waitFor(t, "tasks reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "🟢 #0007 Running task") {
		t.Fatalf("in-flight task glyph wrong: %q", txt)
	}
	if !strings.Contains(txt, "🟡 #0009 Queued &lt;task&gt;") {
		t.Fatalf("queued task glyph/escaping wrong: %q", txt)
	}
	if !strings.Contains(txt, "<b>Alpha — active tasks</b>") {
		t.Fatalf("header missing: %q", txt)
	}
}

// TestBridgeHelpAndUnknown: /help lists the full 0142 verb set with no
// "(soon)" markers; unknown commands point at /help; a run-control verb
// on a bridge built without a RunController draws a clear reply; plain
// text stays silent.
func TestBridgeHelpAndUnknown(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/help"),
		updateJSON(2, 42, 42, "nuno", "/frobnicate"),
		updateJSON(3, 42, 42, "nuno", "/say hello"),
		updateJSON(4, 42, 42, "nuno", "just chatting"),
	)
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	startBridge(t, b)

	waitFor(t, "script drained", fake.scriptDrained)
	waitFor(t, "three replies", func() bool { return len(fake.sentMessages()) >= 3 })
	time.Sleep(100 * time.Millisecond)
	sent := fake.sentMessages()
	if len(sent) != 3 {
		t.Fatalf("expected 3 replies (help, unknown, no-runner) — plain text must stay silent: %+v", sent)
	}
	help := sent[0].Text
	for _, want := range []string{
		"/projects", "/use", "/status", "/tasks",
		"/run", "/runall", "/retry", "/cancel", "/screen", "/say", "/watch", "/mute", "/unmute",
		"/pair", "/help",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("/help missing %q: %q", want, help)
		}
	}
	if strings.Contains(help, "soon") {
		t.Fatalf("/help still carries a coming-soon marker: %q", help)
	}
	if !strings.Contains(sent[1].Text, "/help") || !strings.Contains(sent[1].Text, "Unknown command") {
		t.Fatalf("unknown-command reply: %q", sent[1].Text)
	}
	if !strings.Contains(sent[2].Text, "not wired up") {
		t.Fatalf("/say without a RunController should say so: %q", sent[2].Text)
	}
}

// TestBridgeCommandsFromUnpairedChatSilent: the 0136 posture is
// unchanged — every command except /start //pair draws pure silence
// from an unpaired chat.
func TestBridgeCommandsFromUnpairedChatSilent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 99, 99, "stranger", "/projects"),
		updateJSON(2, 99, 99, "stranger", "/status"),
		updateJSON(3, 99, 99, "stranger", "/tasks"),
		updateJSON(4, 99, 99, "stranger", "/help"),
		updateJSON(5, 99, 99, "stranger", "/use 1"),
	)
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	startBridge(t, b)

	waitFor(t, "script drained", fake.scriptDrained)
	waitFor(t, "one more poll", func() bool { return len(fake.recordedOffsets()) >= 6 })
	if sent := fake.sentMessages(); len(sent) != 0 {
		t.Fatalf("unpaired chat drew replies: %+v", sent)
	}
}

// TestBridgeUsePersistsAcrossRestart: /use writes DefaultProjectID
// through the real config round-trip; a fresh bridge built from the
// re-loaded config serves /status for that project without another
// /use.
func TestBridgeUsePersistsAcrossRestart(t *testing.T) {
	withTestEnv(t)

	// Seed integrations.yaml with an enabled Telegram block and the
	// paired chat, as pairing (0136) would have left it.
	seed := models.NewIntegrationsConfig()
	seed.Telegram = &models.TelegramConfig{
		Enabled:     true,
		PairedChats: pairedChat42(),
	}
	if err := config.SaveIntegrations(seed); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}

	tasks := map[string][]*models.Task{
		"p-alpha": {{TaskNumber: 7, Title: "T", Status: models.TaskStatusReady}},
	}

	// First bridge lifetime: /use alpha, persisted via the production
	// persist hook (no recordedDefaults override here).
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/use alpha"))
	b1 := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       seed.Telegram.PairedChats,
		CommandContextFor: fakeCommandContextFor(testProjects, tasks),
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() { b1.Run(ctx1); close(done1) }()
	waitFor(t, "use confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	cancel1()
	<-done1
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "<b>Alpha</b>") {
		t.Fatalf("/use confirmation: %q", txt)
	}

	// The selection reached the YAML.
	reloaded, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if got := reloaded.Telegram.PairedChats[0].DefaultProjectID; got != "p-alpha" {
		t.Fatalf("persisted DefaultProjectID = %q, want p-alpha", got)
	}

	// Simulated restart: a fresh bridge seeded from the re-loaded
	// config answers /status for Alpha without any new /use.
	fake2 := newFakeBotAPI(t, updateJSON(10, 42, 42, "nuno", "/status"))
	b2 := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       reloaded.Telegram.PairedChats,
		CommandContextFor: fakeCommandContextFor(testProjects, tasks),
	})
	startBridge(t, b2)
	waitFor(t, "status after restart", func() bool { return len(fake2.sentMessages()) >= 1 })
	txt := fake2.sentMessages()[0].Text
	if !strings.Contains(txt, "Alpha") || strings.Contains(txt, "No project selected") {
		t.Fatalf("restarted bridge lost the selection: %q", txt)
	}
}
