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
	// The menu carries the full canonical verb set; only the hidden
	// aliases (/runall, /unmute) stay out.
	for _, cmd := range []string{
		"projects", "use", "status", "tasks", "agent",
		"run", "new", "wildfire", "stop", "generate", "plan", "retry", "cancel",
		"watch", "screen", "say", "mute", "pair", "help",
	} {
		if !strings.Contains(got, `"`+cmd+`"`) {
			t.Fatalf("setMyCommands payload missing %q: %s", cmd, got)
		}
	}
	for _, hidden := range []string{"runall", "unmute"} {
		if strings.Contains(got, `"`+hidden+`"`) {
			t.Fatalf("menu should not list the hidden alias %q: %s", hidden, got)
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
// text forwards to the agent session (and, without a runner wired,
// draws the same clear reply instead of silence).
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
	waitFor(t, "four replies", func() bool { return len(fake.sentMessages()) >= 4 })
	time.Sleep(100 * time.Millisecond)
	sent := fake.sentMessages()
	if len(sent) != 4 {
		t.Fatalf("expected 4 replies (help, unknown, no-runner /say, no-runner plain text): %+v", sent)
	}
	help := sent[0].Text
	for _, want := range []string{
		"/projects", "/use", "/status", "/tasks", "/agent",
		"/run", "/new", "/wildfire", "/stop", "/generate", "/plan",
		"/retry", "/cancel", "/screen", "/say", "/watch", "/mute",
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
	if !strings.Contains(sent[3].Text, "not wired up") {
		t.Fatalf("plain text without a RunController should say so: %q", sent[3].Text)
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

// TestGenerateWithoutRunner: /generate on a bridge without a
// RunController draws the clear reply instead of panicking (the method
// selection is deferred behind the requireRunner guard).
func TestGenerateWithoutRunner(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/generate"))
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       pairedChat42(),
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
	})
	startBridge(t, b)

	waitFor(t, "no-runner reply", func() bool { return sentContaining(fake, "not wired up") })
}

// stubAgents is a scripted AgentSelector.
type stubAgents struct {
	mu      sync.Mutex
	current string
	sets    []string
	choices []AgentChoice
}

func (a *stubAgents) ListAgents(context.Context) ([]AgentChoice, error) {
	return append([]AgentChoice(nil), a.choices...), nil
}
func (a *stubAgents) ProjectAgent(context.Context, string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current, nil
}
func (a *stubAgents) SetProjectAgent(_ context.Context, _ string, agent string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sets = append(a.sets, agent)
	a.current = agent
	return nil
}

// TestAgentCommand: bare /agent lists backends with the current mark
// and install state; /agent <prefix> switches by unique match; an
// uninstalled backend is refused; junk gets a pointer to the list.
func TestAgentCommand(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/agent"),
		updateJSON(2, 42, 42, "nuno", "/agent cod"),
		updateJSON(3, 42, 42, "nuno", "/agent gemini"),
		updateJSON(4, 42, 42, "nuno", "/agent nosuch"),
	)
	agents := &stubAgents{
		current: "claude-code",
		choices: []AgentChoice{
			{Name: "claude-code", DisplayName: "Claude Code", Available: true},
			{Name: "codex", DisplayName: "Codex", Available: true},
			{Name: "gemini", DisplayName: "Gemini CLI", Available: false},
		},
	}
	chats := pairedChat42()
	chats[0].DefaultProjectID = "p1"
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       chats,
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
		Agents:            agents,
	})
	startBridge(t, b)

	waitFor(t, "four replies", func() bool { return len(fake.sentMessages()) >= 4 })
	sent := fake.sentMessages()
	if !strings.Contains(sent[0].Text, "Claude Code (claude-code) ✓") {
		t.Fatalf("list should mark the current agent: %q", sent[0].Text)
	}
	if !strings.Contains(sent[0].Text, "Gemini CLI (gemini) — not installed") {
		t.Fatalf("list should mark uninstalled backends: %q", sent[0].Text)
	}
	if !strings.Contains(sent[1].Text, "Default agent set to <b>Codex</b>") {
		t.Fatalf("prefix switch reply: %q", sent[1].Text)
	}
	if !strings.Contains(sent[2].Text, "not installed on this machine") {
		t.Fatalf("uninstalled backend must be refused: %q", sent[2].Text)
	}
	if !strings.Contains(sent[3].Text, "No agent matches") {
		t.Fatalf("unknown agent reply: %q", sent[3].Text)
	}
	agents.mu.Lock()
	sets := append([]string(nil), agents.sets...)
	agents.mu.Unlock()
	if len(sets) != 1 || sets[0] != "codex" {
		t.Fatalf("SetProjectAgent calls = %v, want [codex]", sets)
	}
}

// TestRunAllViaRunAll: "/run all" folds the old /runall in, and the
// bare /runall alias keeps working (both via the same seam call).
func TestRunAllViaRunAll(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/run all"),
		updateJSON(2, 42, 42, "nuno", "/runall"),
	)
	runner := &stubRunner{start: RunStart{TaskNumber: 3, TaskTitle: "First"}}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "two confirmations", func() bool { return len(fake.sentMessages()) >= 2 })
	runner.mu.Lock()
	n := len(runner.runAllStarts)
	runner.mu.Unlock()
	if n != 2 {
		t.Fatalf("StartRunAll calls = %d, want 2 (/run all + /runall alias)", n)
	}
}

// TestStatusAll: "/status all" renders one line per project with live
// session state from the SessionSource and a working count; the active
// project carries the ✓ marker.
func TestStatusAll(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/status all"))
	sessions := &fakeSessions{sess: map[string]*WatchedSession{
		"p-alpha": {ProjectID: "p-alpha", Mode: "wildfire", Phase: "generate"},
		"p-beta":  {ProjectID: "p-beta", Mode: "chat"},
	}}
	chats := pairedChat42()
	chats[0].DefaultProjectID = "p-beta"
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       chats,
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
		Sessions:          sessions,
	})
	startBridge(t, b)

	waitFor(t, "fleet reply", func() bool { return sentContaining(fake, "Fleet") })
	txt := fake.sentMessages()[0].Text
	for _, want := range []string{
		"3 projects, 2 working",
		"🟢 <b>Alpha</b> — wildfire (generate)",
		"🟢 <b>Beta</b> ✓ — chat session",
		"⚪ <b>Evil &lt;script&gt;alert(1)&lt;/script&gt; &amp; Co</b> — idle",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("fleet view missing %q:\n%s", want, txt)
		}
	}
}
