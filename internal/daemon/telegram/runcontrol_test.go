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

	taskStarts   []int    // task numbers passed to StartTask
	taskProjects []string // project ids passed to StartTask
	runAllStarts []string // project ids passed to StartRunAll
	inputs       []string // raw byte payloads passed to SendInput
	inputProj    []string // project ids passed to SendInput
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

func (r *stubRunner) SendInput(projectID string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputProj = append(r.inputProj, projectID)
	r.inputs = append(r.inputs, string(data))
	return r.inputErr
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

// TestRunRefusesWhileAgentRunning: /run against a project with a live
// session is refused — never queued, never replaced — naming the
// in-flight task, and the RunController is never invoked.
func TestRunRefusesWhileAgentRunning(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/run 9"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "refusal reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "already running") || !strings.Contains(txt, "#0007") {
		t.Fatalf("refusal should name the in-flight task: %q", txt)
	}
	if !strings.Contains(txt, "Busy &lt;task&gt;") {
		t.Fatalf("in-flight task title missing or unescaped: %q", txt)
	}
	if starts, runAlls, inputs := runner.snapshot(); len(starts)+len(runAlls)+len(inputs) != 0 {
		t.Fatalf("refusal must not touch the RunController: %v %v %v", starts, runAlls, inputs)
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

// TestRunAllRefusesWhileAgentRunning: /runall has the same
// never-queue-never-replace semantics as /run.
func TestRunAllRefusesWhileAgentRunning(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/runall"))
	runner := &stubRunner{}
	b := runControlBridge(runner, busySession(), chatOnProject("p1"))
	startBridge(t, b)

	waitFor(t, "refusal reply", func() bool { return len(fake.sentMessages()) >= 1 })
	txt := fake.sentMessages()[0].Text
	if !strings.Contains(txt, "already running") || !strings.Contains(txt, "#0007") {
		t.Fatalf("refusal should name the in-flight task: %q", txt)
	}
	if _, runAlls, _ := runner.snapshot(); len(runAlls) != 0 {
		t.Fatalf("refusal must not start run-all: %v", runAlls)
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
// (internal whitespace preserved, HTML not escaped) plus exactly one
// trailing \r, and acks with "→ sent".
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
	if len(inputs) != 1 {
		t.Fatalf("SendInput calls = %d, want 1", len(inputs))
	}
	if inputs[0] != "echo  hi <there>\r" {
		t.Fatalf("injected bytes = %q, want %q", inputs[0], "echo  hi <there>\r")
	}
	if strings.Count(inputs[0], "\r") != 1 || strings.Contains(inputs[0], "\n") {
		t.Fatalf("payload must end in exactly one \\r and no \\n: %q", inputs[0])
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
