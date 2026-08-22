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

// stubRunner is a recording RunController seam.
type stubRunner struct {
	mu       sync.Mutex
	start    RunStart // returned by both start calls (zero → echo the request)
	startErr error
	inputErr error

	taskStarts     []int              // task numbers passed to StartTask
	taskProjects   []string           // project ids passed to StartTask
	runAllStarts   []string           // project ids passed to StartRunAll
	chatStarts     []string           // project ids passed to StartChat
	wildfireStarts []string           // project ids passed to StartWildfire
	generateStarts []string           // project ids passed to StartGenerate
	planStarts     []string           // project ids passed to StartPlan
	chatRestarts   []string           // project ids passed to RestartChat
	stops          []string           // project ids passed to StopAgent
	inputs         []string           // raw byte payloads passed to SendInput
	inputProj      []string           // project ids passed to SendInput
	onInput        func(chunk string) // optional hook, called per SendInput chunk
}

func (r *stubRunner) StartTask(_ context.Context, projectID string, taskNumber int) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taskProjects = append(r.taskProjects, projectID)
	r.taskStarts = append(r.taskStarts, taskNumber)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	if r.start == (RunStart{}) {
		return RunStart{TaskNumber: taskNumber, TaskTitle: "T"}, nil
	}
	return r.start, nil
}

func (r *stubRunner) StartRunAll(_ context.Context, projectID string) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runAllStarts = append(r.runAllStarts, projectID)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	return r.start, nil
}

func (r *stubRunner) StartChat(_ context.Context, projectID string) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chatStarts = append(r.chatStarts, projectID)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	return r.start, nil
}

func (r *stubRunner) StartWildfire(_ context.Context, projectID string) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wildfireStarts = append(r.wildfireStarts, projectID)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	return r.start, nil
}

func (r *stubRunner) StartGenerate(_ context.Context, projectID string) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generateStarts = append(r.generateStarts, projectID)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	return r.start, nil
}

func (r *stubRunner) StartPlan(_ context.Context, projectID string) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.planStarts = append(r.planStarts, projectID)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	return r.start, nil
}

func (r *stubRunner) RestartChat(_ context.Context, projectID string) (RunStart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chatRestarts = append(r.chatRestarts, projectID)
	if r.startErr != nil {
		return RunStart{}, r.startErr
	}
	return r.start, nil
}

func (r *stubRunner) StopAgent(projectID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, projectID)
	return nil
}

func (r *stubRunner) SendInput(projectID string, data []byte) error {
	r.mu.Lock()
	r.inputProj = append(r.inputProj, projectID)
	r.inputs = append(r.inputs, string(data))
	hook, err := r.onInput, r.inputErr
	r.mu.Unlock()
	if hook != nil {
		hook(string(data))
	}
	return err
}

func (r *stubRunner) snapshot() (taskStarts []int, runAllStarts, inputs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.taskStarts...),
		append([]string(nil), r.runAllStarts...),
		append([]string(nil), r.inputs...)
}

// chatOnProject is pairedChat42 with the active project pre-selected.
func chatOnProject(projectID string) []models.TelegramPairedChat {
	chats := pairedChat42()
	chats[0].DefaultProjectID = projectID
	return chats
}

// busySession is a live task-mode session for the refusal tests.
func busySession() *fakeSessions {
	return &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "task", TaskNumber: 7, TaskTitle: "Busy <task>"},
	}}
}

// idleSessions has no live session for any project.
func idleSessions() *fakeSessions {
	return &fakeSessions{sess: map[string]*WatchedSession{}}
}

func runControlBridge(runner RunController, sessions SessionSource, chats []models.TelegramPairedChat) *Bridge {
	return New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       chats,
		CommandContextFor: fakeCommandContextFor(testProjects, nil),
		Sessions:          sessions,
		Runner:            runner,
	})
}

// TestRunReplacesRunningAgent: /run against a project with a live
// session REPLACES it — the same mode-switch semantics as the GUI/TUI
// (v10.0.2) — and the confirmation names what was replaced, escaped.
func TestRunReplacesRunningAgent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/run 9"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "start confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "▶ Started task #0009") {
		t.Fatalf("should confirm the start: %q", txt)
	}
	if !strings.Contains(txt, "Replaced the running task #0007 — Busy &lt;task&gt;") {
		t.Fatalf("confirmation should name the replaced session, escaped: %q", txt)
	}
	starts, _, _ := runner.snapshot()
	if len(starts) != 1 || starts[0] != 9 {
		t.Fatalf("StartTask calls = %v, want [9] (replace, not refuse)", starts)
	}
}

// TestRunStartsWhenIdle: with no agent running, /run starts the task
// through the seam and confirms with task number + title.
func TestRunStartsWhenIdle(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/run 9"))
	runner := &stubRunner{start: RunStart{TaskNumber: 9, TaskTitle: "Ship <it>"}}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "start confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "▶ Started task #0009") || !strings.Contains(txt, "Ship &lt;it&gt;") {
		t.Fatalf("start confirmation should carry number + escaped title: %q", txt)
	}
	starts, _, _ := runner.snapshot()
	if len(starts) != 1 || starts[0] != 9 {
		t.Fatalf("StartTask calls = %v, want [9]", starts)
	}
	runner.mu.Lock()
	proj := runner.taskProjects[0]
	runner.mu.Unlock()
	if proj != "p1" {
		t.Fatalf("StartTask project = %q, want p1", proj)
	}
}

// TestRunUsageAndStartError: a missing/garbled task number draws usage;
// a seam error (e.g. the server-side race backstop) is surfaced.
func TestRunUsageAndStartError(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/run"),
		updateJSON(2, 42, 42, "nuno", "/run abc"),
		updateJSON(3, 42, 42, "nuno", "/run 5"),
	)
	runner := &stubRunner{startErr: fmt.Errorf("boom <err>")}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "three replies", func() bool { return len(fake.sentMessages()) >= 3 })
	sent := fake.sentMessages()
	for i := 0; i < 2; i++ {
		if !strings.Contains(sent[i].Text, "Usage: /run") {
			t.Fatalf("reply %d should be usage: %q", i, sent[i].Text)
		}
	}
	if !strings.Contains(sent[2].Text, "Failed to start task #0005") || !strings.Contains(sent[2].Text, "boom &lt;err&gt;") {
		t.Fatalf("seam error should be surfaced escaped: %q", sent[2].Text)
	}
}

// TestRunAllReplacesRunningAgent: /runall replaces a running agent
// like /run, naming what it replaced.
func TestRunAllReplacesRunningAgent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/runall"))
	runner := &stubRunner{start: RunStart{TaskNumber: 3, TaskTitle: "First"}}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "start confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "▶ Run-all started on task #0003") || !strings.Contains(txt, "Replaced the running task #0007") {
		t.Fatalf("run-all should replace and say so: %q", txt)
	}
	if _, runAlls, _ := runner.snapshot(); len(runAlls) != 1 {
		t.Fatalf("StartRunAll calls = %v, want 1 (replace, not refuse)", runAlls)
	}
}

// TestRunAllStartsWhenIdle: run-all starts through the seam and the
// confirmation names the first task.
func TestRunAllStartsWhenIdle(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/runall"))
	runner := &stubRunner{start: RunStart{TaskNumber: 3, TaskTitle: "First"}}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "start confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "Run-all started on task #0003") || !strings.Contains(txt, "First") {
		t.Fatalf("run-all confirmation: %q", txt)
	}
	if _, runAlls, _ := runner.snapshot(); len(runAlls) != 1 || runAlls[0] != "p1" {
		t.Fatalf("StartRunAll calls = %v, want [p1]", runAlls)
	}
}

// TestSayInjectsExactBytes: /say writes the user's text VERBATIM
// (internal whitespace preserved, HTML not escaped), then exactly one
// \r as its own write (a single text+\r chunk trips the CLI's paste
// detection and the Enter never submits), and acks with "→ sent".
func TestSayInjectsExactBytes(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/say echo  hi <there>"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "say ack", func() bool { return len(fake.sentMessages()) >= 1 })
	if txt := fake.sentMessages()[0].Text; txt != "→ sent" {
		t.Fatalf("ack = %q, want %q", txt, "→ sent")
	}
	_, _, inputs := runner.snapshot()
	if len(inputs) != 2 {
		t.Fatalf("SendInput calls = %d, want 2 (text, then Enter)", len(inputs))
	}
	if inputs[0] != "echo  hi <there>" || inputs[1] != "\r" {
		t.Fatalf("injected chunks = %q, want text then a lone \\r", inputs)
	}
	runner.mu.Lock()
	proj := runner.inputProj[0]
	runner.mu.Unlock()
	if proj != "p1" {
		t.Fatalf("SendInput project = %q, want p1", proj)
	}
}

// TestSayOnlyWhenRunning: /say against an idle project is refused and
// nothing reaches the PTY seam; an empty /say draws usage.
func TestSayOnlyWhenRunning(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/say hello"),
		updateJSON(2, 42, 42, "nuno", "/say"),
	)
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "two replies", func() bool { return len(fake.sentMessages()) >= 2 })
	sent := fake.sentMessages()
	if !strings.Contains(sent[0].Text, "No agent is running") {
		t.Fatalf("idle /say should refuse: %q", sent[0].Text)
	}
	if !strings.Contains(sent[1].Text, "Usage: /say") {
		t.Fatalf("empty /say should draw usage: %q", sent[1].Text)
	}
	if _, _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatalf("nothing may reach SendInput: %v", inputs)
	}
}

// TestScreenLive: /screen replies with a <pre> block holding the last
// 40 tier-2-normalized lines — ANSI stripped, older lines dropped.
func TestScreenLive(t *testing.T) {
	withTestEnv(t)
	lines := make([]string, 0, 52)
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("\x1b[31mline%02d\x1b[0m", i))
	}
	lines = append(lines, "", "") // blank bottom edge → trimmed
	sessions := &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "task", TaskNumber: 7, TaskTitle: "T",
			Snapshot: func() []string { return append([]string(nil), lines...) }},
	}}
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/screen"))
	b := runControlBridge(&stubRunner{}, sessions, chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "screen reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.HasPrefix(txt, "<pre>") || !strings.HasSuffix(txt, "</pre>") {
		t.Fatalf("screen tail should be a <pre> block: %q", txt)
	}
	if !strings.Contains(txt, "line11") || !strings.Contains(txt, "line50") {
		t.Fatalf("tail should keep the last 40 lines: %q", txt)
	}
	if strings.Contains(txt, "line10") {
		t.Fatalf("lines beyond the 40-line tail must be dropped: %q", txt)
	}
	if strings.Contains(txt, "\x1b") {
		t.Fatalf("ANSI escapes must be stripped: %q", txt)
	}
}

// TestScreenIdle: /screen with no live session says so.
func TestScreenIdle(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/screen"))
	b := runControlBridge(&stubRunner{}, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "idle reply", func() bool { return len(fake.sentMessages()) >= 1 })
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "No agent is running") {
		t.Fatalf("idle /screen reply: %q", txt)
	}
}

// TestRetryAndCancelRouteThroughEcho: /retry and /cancel are pure
// dispatch into echo.Route — the production router callbacks fire and
// the router's response is rendered back as Telegram HTML.
func TestRetryAndCancelRouteThroughEcho(t *testing.T) {
	withTestEnv(t)
	var mu sync.Mutex
	var retried, cancelled []int
	ccFor := func(chatID, userID int64) echo.CommandContext {
		return echo.CommandContext{
			Now: time.Now,
			LookupTask: func(ctx context.Context, ref string) (*models.Task, echo.ProjectInfo, error) {
				n, _, ok := echo.ParseTaskRef(ref)
				if !ok {
					return nil, echo.ProjectInfo{}, echo.ErrTaskNotFound
				}
				return &models.Task{TaskNumber: n, Title: "Fix it"}, echo.ProjectInfo{ID: "p1", Name: "Alpha"}, nil
			},
			Retry: func(ctx context.Context, projectID string, n int) error {
				mu.Lock()
				retried = append(retried, n)
				mu.Unlock()
				return nil
			},
			Cancel: func(ctx context.Context, projectID string, n int, reason string) error {
				mu.Lock()
				cancelled = append(cancelled, n)
				mu.Unlock()
				return nil
			},
		}
	}
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/retry 7"),
		updateJSON(2, 42, 42, "nuno", "/cancel 8"),
		updateJSON(3, 42, 42, "nuno", "/retry"),
	)
	b := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats:       chatOnProject("p1"),
		CommandContextFor: ccFor,
	})
	startBridge(t, b)

	waitFor(t, "three replies", func() bool { return len(fake.sentMessages()) >= 3 })
	sent := fake.sentMessages()
	if !strings.Contains(sent[0].Text, "Retrying task #0007") {
		t.Fatalf("/retry reply should come from the router: %q", sent[0].Text)
	}
	if !strings.Contains(sent[1].Text, "Cancelled task #0008") {
		t.Fatalf("/cancel reply should come from the router: %q", sent[1].Text)
	}
	if !strings.Contains(sent[2].Text, "Usage: /retry") {
		t.Fatalf("empty /retry should draw usage: %q", sent[2].Text)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(retried) != 1 || retried[0] != 7 {
		t.Fatalf("Retry callback calls = %v, want [7]", retried)
	}
	if len(cancelled) != 1 || cancelled[0] != 8 {
		t.Fatalf("Cancel callback calls = %v, want [8]", cancelled)
	}
}

// TestMuteRoundTrip: /mute persists Muted=true through the production
// config path; a bridge restarted from the re-loaded config still
// knows it, and /unmute flips it back.
func TestMuteRoundTrip(t *testing.T) {
	withTestEnv(t)
	seed := models.NewIntegrationsConfig()
	seed.Telegram = &models.TelegramConfig{Enabled: true, PairedChats: pairedChat42()}
	if err := config.SaveIntegrations(seed); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}

	// First bridge lifetime: /mute via the production persist hook.
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/mute"))
	b1 := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: seed.Telegram.PairedChats,
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() { b1.Run(ctx1); close(done1) }()
	waitFor(t, "mute confirmation", func() bool { return len(fake.sentMessages()) >= 1 })
	cancel1()
	<-done1
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "Muted") {
		t.Fatalf("/mute confirmation: %q", txt)
	}
	b1.mu.Lock()
	inMem := b1.paired[42].Muted
	b1.mu.Unlock()
	if !inMem {
		t.Fatal("in-memory Muted not mirrored after /mute")
	}

	reloaded, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if !reloaded.Telegram.PairedChats[0].Muted {
		t.Fatal("persisted Muted = false after /mute, want true")
	}

	// Simulated restart: /unmute flips it back through the same path.
	fake2 := newFakeBotAPI(t, updateJSON(10, 42, 42, "nuno", "/unmute"))
	b2 := New(Config{
		Token: testToken, Pairing: NewPairing(), Hostname: "h",
		PairedChats: reloaded.Telegram.PairedChats,
	})
	startBridge(t, b2)
	waitFor(t, "unmute confirmation", func() bool { return len(fake2.sentMessages()) >= 1 })
	if txt := fake2.sentMessages()[0].Text; !strings.Contains(txt, "Unmuted") {
		t.Fatalf("/unmute confirmation: %q", txt)
	}
	reloaded2, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if reloaded2.Telegram.PairedChats[0].Muted {
		t.Fatal("persisted Muted = true after /unmute, want false")
	}
}

// TestSayVerbatim: the /say argument is carved out of the raw message
// text — internal whitespace survives, only the leading token and one
// separator are stripped.
func TestSayVerbatim(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/say hello", "hello"},
		{"/say  double  spaced", " double  spaced"},
		{"/say@WatchfireBot yes", "yes"},
		{"/say", ""},
		{"/say y", "y"},
	}
	for _, c := range cases {
		if got := sayVerbatim(c.in); got != c.want {
			t.Errorf("sayVerbatim(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// chatSession is a live chat-mode session — the target the plain-text
// conversation path may type into.
func chatSession() *fakeSessions {
	return &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "chat", Snapshot: func() []string { return []string{"> "} }},
	}}
}

// TestPlainTextTalksToChatAgent: a paired chat's non-command message is
// forwarded verbatim (plus exactly one \r) to the live CHAT session —
// the no-prefix conversation path. This chat opted out of watch, so the
// bridge acks with a /watch hint (a watching chat gets the reply via
// the relay instead — TestPlainTextAutoAttaches).
func TestPlainTextTalksToChatAgent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "how is it going?"))
	runner := &stubRunner{}
	chats := chatOnProject("p1")
	chats[0].WatchOff = true
	b := runControlBridge(runner, chatSession(), chats)
	startBridge(t, b)

	waitFor(t, "watch hint ack", func() bool { return sentContaining(fake, "/watch on to stream") })
	_, _, inputs := runner.snapshot()
	if len(inputs) != 2 || inputs[0] != "how is it going?" || inputs[1] != "\r" {
		t.Fatalf("injected chunks = %q, want text then a lone Enter", inputs)
	}
	runner.mu.Lock()
	proj := runner.inputProj[0]
	runner.mu.Unlock()
	if proj != "p1" {
		t.Fatalf("SendInput project = %q, want p1", proj)
	}
}

// TestPlainTextAutoAttaches: with no /use selection the message still
// reaches the (only) live chat session — a fresh pairing can talk
// without setup. The chat watches by default, so no ack is sent (the
// relay carries the conversation).
func TestPlainTextAutoAttaches(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "hello there"))
	runner := &stubRunner{}
	b := runControlBridge(runner, chatSession(), pairedChat42()) // no default project
	startBridge(t, b)

	waitFor(t, "delivery", func() bool {
		_, _, inputs := runner.snapshot()
		return len(inputs) >= 2
	})
	_, _, inputs := runner.snapshot()
	if inputs[0] != "hello there" || inputs[1] != "\r" {
		t.Fatalf("injected chunks = %q, want text then a lone Enter", inputs)
	}
	runner.mu.Lock()
	proj := runner.inputProj[0]
	runner.mu.Unlock()
	if proj != "p1" {
		t.Fatalf("auto-attach should target the live session's project, got %q", proj)
	}
	// Watching chat → no "→ sent" ack; the hint would be noise.
	time.Sleep(20 * time.Millisecond)
	if sentContaining(fake, "→ sent") {
		t.Fatalf("watching chat must not get a send ack: %+v", fake.sentMessages())
	}
}

// TestPlainTextBusyAgentOffersOptions: a live NON-chat session is never
// typed into implicitly — the reply names what's running and lists the
// ways out (/watch, /screen, explicit /say, /cancel), and neither the
// PTY seam nor StartChat is touched.
func TestPlainTextBusyAgentOffersOptions(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "are we ready for a release?"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "busy options reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	// No "/watch on" line: the chat watches by default, so that option
	// is omitted as redundant.
	for _, want := range []string{"busy", "#0007", "/screen", "/say", "/cancel 7"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("busy reply missing %q: %q", want, txt)
		}
	}
	if _, _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatalf("nothing may reach SendInput: %v", inputs)
	}
	runner.mu.Lock()
	chatStarts := len(runner.chatStarts)
	runner.mu.Unlock()
	if chatStarts != 0 {
		t.Fatalf("busy path must not auto-start a chat agent")
	}
}

// TestPlainTextStartsChatAgent: with a selected project and nothing
// running, plain text auto-starts a chat agent, announces it, and
// delivers the message once the session paints its first screen.
func TestPlainTextStartsChatAgent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "Are we ready for a release?"))
	runner := &stubRunner{}
	sessions := idleSessions()
	b := runControlBridge(runner, sessions, chatOnProject("p1"))
	b.chatStartPoll = 2 * time.Millisecond
	b.chatStartWait = 500 * time.Millisecond
	b.chatStartSettle = time.Millisecond
	startBridge(t, b)

	waitFor(t, "start announcement", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "starting a chat agent") {
		t.Fatalf("should announce the auto-start: %q", txt)
	}
	runner.mu.Lock()
	chatStarts := append([]string(nil), runner.chatStarts...)
	runner.mu.Unlock()
	if len(chatStarts) != 1 || chatStarts[0] != "p1" {
		t.Fatalf("StartChat calls = %v, want [p1]", chatStarts)
	}

	// The agent comes up: expose a live chat session with a painted
	// screen; the queued message must then be injected verbatim.
	sessions.mu.Lock()
	sessions.sess["p1"] = &WatchedSession{
		ProjectID: "p1", Mode: "chat",
		Snapshot: func() []string { return []string{"Claude Code", "> "} },
	}
	sessions.mu.Unlock()

	waitFor(t, "queued delivery", func() bool {
		_, _, inputs := runner.snapshot()
		return len(inputs) >= 2
	})
	_, _, inputs := runner.snapshot()
	if inputs[0] != "Are we ready for a release?" || inputs[1] != "\r" {
		t.Fatalf("injected chunks = %q, want text then a lone Enter", inputs)
	}
}

// TestPlainTextNoProjectNoSession: nothing running and no /use
// selection → guidance to pick a project; nothing is started or typed.
func TestPlainTextNoProjectNoSession(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "anyone home?"))
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), pairedChat42())
	startBridge(t, b)

	waitFor(t, "guidance reply", func() bool { return len(fake.sentMessages()) >= 1 })
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "No project selected") {
		t.Fatalf("should ask for a project: %q", txt)
	}
	if _, _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatalf("nothing may reach SendInput: %v", inputs)
	}
}

// TestWildfireVerb: bare /wildfire starts the loop through the seam
// (refusal-gated like /run; "on"/"start" are aliases), /wildfire off
// user-stops a running wildfire, and the degenerate cases reply
// clearly.
func TestWildfireVerb(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/wildfire"))
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "start reply", func() bool { return sentContaining(fake, "Wildfire started") })
	if !sentContaining(fake, "milestones") {
		t.Fatalf("watching chat should be promised the milestone feed: %+v", fake.sentMessages())
	}
	runner.mu.Lock()
	wf := append([]string(nil), runner.wildfireStarts...)
	runner.mu.Unlock()
	if len(wf) != 1 || wf[0] != "p1" {
		t.Fatalf("StartWildfire calls = %v, want [p1]", wf)
	}
}

// TestWildfireReplacesRunningAgent: /wildfire over a running task
// replaces it (like the GUI's Wildfire button), naming what it replaced;
// a wildfire already running is reported rather than restarted.
func TestWildfireReplacesRunningAgent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/wildfire"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "start reply", func() bool { return sentContaining(fake, "Wildfire started") })
	if !sentContaining(fake, "Replaced the running task #0007") {
		t.Fatalf("wildfire start should name the replaced session: %+v", fake.sentMessages())
	}
	runner.mu.Lock()
	wf := len(runner.wildfireStarts)
	runner.mu.Unlock()
	if wf != 1 {
		t.Fatalf("StartWildfire calls = %d, want 1 (replace, not refuse)", wf)
	}
}

func TestWildfireAlreadyRunningIsReported(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/wildfire"))
	runner := &stubRunner{}
	sessions := &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "wildfire", Phase: "refine"},
	}}
	b := runControlBridge(runner, sessions, chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "already-running reply", func() bool { return sentContaining(fake, "already running (refine phase)") })
	runner.mu.Lock()
	wf := len(runner.wildfireStarts)
	runner.mu.Unlock()
	if wf != 0 {
		t.Fatalf("a running wildfire must not be restarted by /wildfire")
	}
}

func TestWildfireVerbStop(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/wildfire off"),
	)
	runner := &stubRunner{}
	sessions := &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "wildfire", Phase: "execute", TaskNumber: 8, TaskTitle: "T"},
	}}
	b := runControlBridge(runner, sessions, chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "stop reply", func() bool { return sentContaining(fake, "Wildfire stopped") })
	runner.mu.Lock()
	stops := append([]string(nil), runner.stops...)
	runner.mu.Unlock()
	if len(stops) != 1 || stops[0] != "p1" {
		t.Fatalf("StopAgent calls = %v, want [p1]", stops)
	}
}

func TestWildfireVerbStopWhenIdle(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/wildfire off"))
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "not running reply", func() bool { return sentContaining(fake, "not running") })
	runner.mu.Lock()
	stops := len(runner.stops)
	runner.mu.Unlock()
	if stops != 0 {
		t.Fatalf("idle /wildfire off must not stop anything")
	}
}

// TestGenerateAndPlanVerbs: /generate and /plan start their sessions
// through the seam when idle and are refusal-gated while an agent runs.
func TestGenerateAndPlanVerbs(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/generate"),
		updateJSON(2, 42, 42, "nuno", "/plan"),
	)
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "both confirmations", func() bool {
		return sentContaining(fake, "Generating the project definition") && sentContaining(fake, "generating tasks from the project definition")
	})
	runner.mu.Lock()
	gen := append([]string(nil), runner.generateStarts...)
	plan := append([]string(nil), runner.planStarts...)
	runner.mu.Unlock()
	if len(gen) != 1 || gen[0] != "p1" || len(plan) != 1 || plan[0] != "p1" {
		t.Fatalf("StartGenerate=%v StartPlan=%v, want [p1] each", gen, plan)
	}
}

func TestGenerateReplacesRunningAgent(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/generate"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "confirmation", func() bool { return sentContaining(fake, "Generating the project definition") })
	if !sentContaining(fake, "Replaced the running task #0007") {
		t.Fatalf("generate should name the replaced session: %+v", fake.sentMessages())
	}
	runner.mu.Lock()
	gen := len(runner.generateStarts)
	runner.mu.Unlock()
	if gen != 1 {
		t.Fatalf("StartGenerate calls = %d, want 1 (replace, not refuse)", gen)
	}
}

// TestNewChatSession: /new restarts the chat session — replacing a
// running chat through the seam's RestartChat (the daemon's atomic
// chat-over-chat path) — and works from idle too; a working non-chat
// agent is refused, never displaced.
func TestNewChatSession(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/new"))
	runner := &stubRunner{}
	b := runControlBridge(runner, chatSession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "fresh-session reply", func() bool { return sentContaining(fake, "Fresh chat session started") })
	runner.mu.Lock()
	restarts := append([]string(nil), runner.chatRestarts...)
	runner.mu.Unlock()
	if len(restarts) != 1 || restarts[0] != "p1" {
		t.Fatalf("RestartChat calls = %v, want [p1]", restarts)
	}
}

func TestNewChatSessionFromIdle(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/new"))
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "fresh-session reply", func() bool { return sentContaining(fake, "Fresh chat session started") })
	runner.mu.Lock()
	restarts := len(runner.chatRestarts)
	runner.mu.Unlock()
	if restarts != 1 {
		t.Fatalf("RestartChat calls = %d, want 1", restarts)
	}
}

func TestNewRefusesWhileBusy(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/new"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "refusal", func() bool { return sentContaining(fake, "/new only replaces a chat session") })
	runner.mu.Lock()
	restarts := len(runner.chatRestarts)
	runner.mu.Unlock()
	if restarts != 0 {
		t.Fatalf("/new must never displace a working agent")
	}
}

// TestStopVerb: /stop user-stops whatever is running (naming it) and
// reports idle projects plainly; the seam is untouched when idle.
func TestStopVerb(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/stop"))
	runner := &stubRunner{}
	sessions := &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "wildfire", Phase: "execute", TaskNumber: 8, TaskTitle: "T"},
	}}
	b := runControlBridge(runner, sessions, chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "stop reply", func() bool { return sentContaining(fake, "🛑 Stopped: wildfire (execute)") })
	runner.mu.Lock()
	stops := append([]string(nil), runner.stops...)
	runner.mu.Unlock()
	if len(stops) != 1 || stops[0] != "p1" {
		t.Fatalf("StopAgent calls = %v, want [p1]", stops)
	}
}

func TestStopVerbWhenIdle(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/stop"))
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "idle reply", func() bool { return sentContaining(fake, "Nothing is running") })
	runner.mu.Lock()
	stops := len(runner.stops)
	runner.mu.Unlock()
	if stops != 0 {
		t.Fatalf("idle /stop must not touch the seam")
	}
}

// loginScreenSession is a scripted chat session whose screen advances
// through the real /login dialog (captured live from Claude Code
// v2.1.238/v2.1.239) as the bridge types into it: prompt → method
// picker → sign-in URL → "Login successful. Press Enter to continue…" →
// back to the prompt. The URL's PKCE challenge is per-process, which is
// why the flow must drive the live session rather than pre-generate a
// link; the confirmation screen is why the flow cannot stop at the code.
type loginScreenSession struct {
	mu    sync.Mutex
	stage int // 0 prompt, 1 picker, 2 url screen, 3 confirm, 4 done
}

const loginTestURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&response_type=code&code_challenge=CYTYPPF9vuYP&code_challenge_method=S256&state=2J_3XQ48"

// at reports the dialog's current stage under the lock.
func (l *loginScreenSession) at() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stage
}

func (l *loginScreenSession) screen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.stage {
	case 1:
		return []string{"Login", "Select login method:", "  ❯ 1. Claude account with subscription", "    2. Anthropic Console account", "Esc to cancel"}
	case 2, -2:
		// The terminal wraps the long URL across lines.
		return []string{"Login", "Browser didn't open? Use the url below to sign in (c to copy)",
			loginTestURL[:60], loginTestURL[60:], "", "Paste code here if prompted >", "Esc to cancel"}
	case 3:
		return []string{"Login", "", "Logged in as nuno@example.com",
			"Login successful. Press Enter to continue…"}
	default:
		return []string{"❯ Try \"how do I log an error?\"", "⏵⏵ bypass permissions on"}
	}
}

// advanceOnInput moves the dialog forward as the bridge's writes arrive,
// mirroring the real CLI: "/login" + its Enter opens the picker (the
// Enter that submits the command is NOT a picker selection); the next
// lone Enter selects option 1 and shows the URL; the code + its Enter
// exchanges the token and parks on the confirmation, which only a final
// Enter dismisses.
func (l *loginScreenSession) advanceOnInput(chunk string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.stage == 0 && chunk == "/login":
		l.stage = -1 // command typed, awaiting its Enter
	case l.stage == -1 && chunk == "\r":
		l.stage = 1
	case l.stage == 1 && chunk == "\r":
		l.stage = 2
	case l.stage == 2 && chunk != "" && chunk != "\r":
		l.stage = -2 // code typed, awaiting its Enter
	case l.stage == -2 && chunk == "\r":
		l.stage = 3
	case l.stage == 3 && chunk == "\r":
		l.stage = 4
	}
}

// TestLoginURLFromScreen: the wrapped URL is rebuilt without swallowing
// the "Paste code here if prompted >" line printed underneath it —
// v10.0.4 joined the whole screen and welded that text onto the state
// parameter, corrupting the code the user copied back.
func TestLoginURLFromScreen(t *testing.T) {
	screen := (&loginScreenSession{stage: 2}).screen()
	got := loginURLFromScreen(screen)
	if got != loginTestURL {
		t.Fatalf("URL mis-scraped:\n got %q\nwant %q", got, loginTestURL)
	}
	if strings.Contains(strings.ToLower(got), "paste") {
		t.Fatalf("trailing screen text glued onto the URL: %q", got)
	}
	// A URL sharing its line with other text ends at the space.
	boxed := []string{"│ Open https://claude.com/cai/oauth/authorize?state=abc now │"}
	if got := loginURLFromScreen(boxed); got != "https://claude.com/cai/oauth/authorize?state=abc" {
		t.Fatalf("boxed URL mis-scraped: %q", got)
	}
	if got := loginURLFromScreen([]string{"nothing here"}); got != "" {
		t.Fatalf("no URL should read as empty, got %q", got)
	}
}

// TestLoginFlow: /login types into the session, steps the picker, scrapes
// the (wrapped) OAuth URL, sends it, and arms the chat so the next plain
// message is pasted as the code — never treated as conversation.
func TestLoginFlow(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "/login"),
	)
	screen := &loginScreenSession{}
	runner := &stubRunner{}
	runner.onInput = screen.advanceOnInput
	sessions := &fakeSessions{sess: map[string]*WatchedSession{
		"p1": {ProjectID: "p1", Mode: "chat", Snapshot: screen.screen},
	}}
	b := runControlBridge(runner, sessions, chatOnProject("p1"))
	b.chatStartPoll = 2 * time.Millisecond
	b.sayEnterDelay = time.Millisecond
	startBridge(t, b)

	waitFor(t, "sign-in link relayed", func() bool { return sentContaining(fake, "Sign in to Claude") })
	if !sentContaining(fake, loginTestURL) {
		t.Fatalf("the full (unwrapped) OAuth URL must be relayed: %+v", fake.sentMessages())
	}
	// The bridge typed "/login" then Enter (two injectSay calls = four
	// chunks: "/login", "\r", "", "\r").
	_, _, inputs := runner.snapshot()
	if len(inputs) != 4 || inputs[0] != "/login" || inputs[1] != "\r" || inputs[2] != "" || inputs[3] != "\r" {
		t.Fatalf("expected /login + Enter + Enter, got %q", inputs)
	}
	b.mu.Lock()
	armed := b.loginPending[42]
	b.mu.Unlock()
	if armed != "p1" {
		t.Fatalf("chat should be armed for the code on p1, got %q", armed)
	}

	// Now the user sends the code as plain text: it's pasted, not chatted.
	fake.mu.Lock()
	fake.script = append(fake.script, updateJSON(2, 42, 42, "nuno", "AbC123-code#xyz"))
	fake.mu.Unlock()
	waitFor(t, "login confirmed", func() bool { return sentContaining(fake, "Logged in as") })
	if !sentContaining(fake, "nuno@example.com") {
		t.Fatalf("the confirmation should name the account: %+v", fake.sentMessages())
	}
	// The code + its Enter, then the Enter that dismisses "Login
	// successful. Press Enter to continue…" — without that last one the
	// session stays parked on the dialog (the v10.0.4 bug).
	_, _, inputs = runner.snapshot()
	if len(inputs) != 8 || inputs[4] != "AbC123-code#xyz" || inputs[5] != "\r" || inputs[6] != "" || inputs[7] != "\r" {
		t.Fatalf("code should be pasted verbatim + Enter, then a confirming Enter, got %q", inputs)
	}
	if screen.at() != 4 {
		t.Fatalf("the login dialog should be dismissed, stage = %d", screen.at())
	}
	runner.mu.Lock()
	chatStarts := len(runner.chatStarts)
	runner.mu.Unlock()
	if chatStarts != 0 {
		t.Fatalf("the code must not be treated as conversation (no chat start)")
	}
	b.mu.Lock()
	_, still := b.loginPending[42]
	b.mu.Unlock()
	if still {
		t.Fatalf("chat must be disarmed after the code is pasted")
	}
}

// TestLoginCancelAndNoSession: "cancel" disarms without pasting; /login
// with nothing running explains.
func TestLoginCancelAndNoSession(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/login"))
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	startBridge(t, b)
	waitFor(t, "no-session reply", func() bool { return sentContaining(fake, "No agent is running") })

	// Arm manually and cancel.
	b.mu.Lock()
	b.loginPending[42] = "p1"
	b.mu.Unlock()
	fake.mu.Lock()
	fake.script = append(fake.script, updateJSON(2, 42, 42, "nuno", "cancel"))
	fake.mu.Unlock()
	waitFor(t, "cancelled", func() bool { return sentContaining(fake, "Login cancelled") })
	if _, _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatalf("cancel must not paste anything: %q", inputs)
	}
}
