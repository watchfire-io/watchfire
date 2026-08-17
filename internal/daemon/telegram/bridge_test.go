package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
	"github.com/watchfire-io/watchfire/internal/models"
)

const testToken = "123456:bridge-test-token"

// --- test doubles -----------------------------------------------------------

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}
func (s *memStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}
func (s *memStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// withTestEnv points $HOME at a temp dir and swaps in an in-memory
// secret store so the bridge's persist path exercises the real
// config.Load/SaveIntegrations round-trip without touching the OS
// keyring.
func withTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	config.SetSecretStoreForTest(&memStore{m: map[string]string{}})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })
}

type sentMessage struct {
	ChatID string
	Text   string
}

// fakeBotAPI is an httptest stand-in for api.telegram.org. getUpdates
// responses are scripted and popped one per call; once the script is
// exhausted the fake returns empty batches (with a short delay so the
// bridge doesn't hot-spin).
type fakeBotAPI struct {
	t  *testing.T
	mu sync.Mutex

	script  []string // raw JSON bodies for successive getUpdates calls
	offsets []string // recorded getUpdates offset params
	sent    []sentMessage

	srv *httptest.Server
}

func newFakeBotAPI(t *testing.T, script ...string) *fakeBotAPI {
	t.Helper()
	f := &fakeBotAPI{t: t, script: script}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	prev := telegrambot.APIBase
	telegrambot.APIBase = f.srv.URL
	t.Cleanup(func() { telegrambot.APIBase = prev })
	return f
}

func (f *fakeBotAPI) handle(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/bot"+testToken+"/") {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/")
	_ = r.ParseForm()
	switch method {
	case "getMe":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":7,"is_bot":true,"username":"watchfire_bot"}}`))
	case "getUpdates":
		f.mu.Lock()
		f.offsets = append(f.offsets, r.Form.Get("offset"))
		var body string
		if len(f.script) > 0 {
			body = f.script[0]
			f.script = f.script[1:]
		}
		f.mu.Unlock()
		if body == "" {
			time.Sleep(20 * time.Millisecond)
			body = `{"ok":true,"result":[]}`
		}
		if strings.HasPrefix(body, "STATUS ") {
			// "STATUS <code> <json>" — scripted error response.
			parts := strings.SplitN(body, " ", 3)
			code := 500
			_, _ = fmt.Sscanf(parts[1], "%d", &code)
			w.WriteHeader(code)
			_, _ = w.Write([]byte(parts[2]))
			return
		}
		_, _ = w.Write([]byte(body))
	case "sendMessage":
		f.mu.Lock()
		f.sent = append(f.sent, sentMessage{ChatID: r.Form.Get("chat_id"), Text: r.Form.Get("text")})
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"}}}`))
	default:
		f.t.Errorf("unexpected Bot API method %q", method)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeBotAPI) sentMessages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

func (f *fakeBotAPI) recordedOffsets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.offsets...)
}

func (f *fakeBotAPI) scriptDrained() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.script) == 0
}

func updateJSON(updateID, chatID, userID int64, username, text string) string {
	u := map[string]any{
		"update_id": updateID,
		"message": map[string]any{
			"message_id": updateID,
			"from":       map[string]any{"id": userID, "is_bot": false, "username": username},
			"chat":       map[string]any{"id": chatID, "type": "private"},
			"text":       text,
		},
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": []any{u}})
	return string(raw)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func startBridge(t *testing.T, b *Bridge) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("bridge goroutine did not exit on context cancel")
		}
	})
}

// --- tests ------------------------------------------------------------------

// TestBridgePairingEndToEnd: Begin → /start with the deep-link payload →
// chat persisted with the correct fields → welcome reply → code single-use.
func TestBridgePairingEndToEnd(t *testing.T) {
	withTestEnv(t)
	pairing := NewPairing()
	code, _, err := pairing.Begin(0)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	fake := newFakeBotAPI(t,
		updateJSON(10, 42, 42, "nuno", "/start "+code),
	)
	b := New(Config{Token: testToken, Pairing: pairing, Hostname: "myhost.local"})
	startBridge(t, b)

	waitFor(t, "welcome reply", func() bool { return len(fake.sentMessages()) >= 1 })
	sent := fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected exactly 1 reply, got %d: %+v", len(sent), sent)
	}
	if sent[0].ChatID != "42" {
		t.Fatalf("welcome sent to chat %s, want 42", sent[0].ChatID)
	}
	if !strings.Contains(sent[0].Text, "myhost.local") || !strings.Contains(sent[0].Text, "/help") {
		t.Fatalf("welcome must name the host and point at /help: %q", sent[0].Text)
	}

	// Chat persisted with the correct fields.
	cfg, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if cfg.Telegram == nil || len(cfg.Telegram.PairedChats) != 1 {
		t.Fatalf("paired chat not persisted: %+v", cfg.Telegram)
	}
	pc := cfg.Telegram.PairedChats[0]
	if pc.ChatID != 42 || pc.UserID != 42 || pc.Username != "nuno" || pc.PairedAt.IsZero() {
		t.Fatalf("persisted chat fields wrong: %+v", pc)
	}

	// Pairing state reports paired; the code is spent.
	if st := pairing.Status(); st.State != StatePaired || st.Chat == nil || st.Chat.ChatID != 42 {
		t.Fatalf("pairing status after redeem: %+v", st)
	}
	if pairing.Consume(code) {
		t.Fatal("code redeemed twice")
	}
	if !b.IsPaired(42) {
		t.Fatal("bridge allowlist missing freshly paired chat")
	}
}

// TestBridgeUnpairedChatGetsInstructionsOrSilence: /pair with a bad code
// draws the pairing instructions; anything else from an unpaired chat
// draws nothing at all.
func TestBridgeUnpairedChatGetsInstructionsOrSilence(t *testing.T) {
	withTestEnv(t)
	pairing := NewPairing()
	if _, _, err := pairing.Begin(0); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	fake := newFakeBotAPI(t,
		updateJSON(1, 99, 99, "stranger", "hello, what projects are you running?"),
		updateJSON(2, 99, 99, "stranger", "/status"),
		updateJSON(3, 99, 99, "stranger", "/pair WRONGCOD"),
		updateJSON(4, 99, 99, "stranger", "/start"),
	)
	b := New(Config{Token: testToken, Pairing: pairing, Hostname: "myhost"})
	startBridge(t, b)

	waitFor(t, "script drained", fake.scriptDrained)
	waitFor(t, "instruction replies", func() bool { return len(fake.sentMessages()) >= 2 })
	// Give any stray replies a moment to land, then assert the exact set:
	// only the two /pair//start probes were answered.
	time.Sleep(100 * time.Millisecond)
	sent := fake.sentMessages()
	if len(sent) != 2 {
		t.Fatalf("unpaired chat drew %d replies, want exactly 2 (pair + start): %+v", len(sent), sent)
	}
	for _, m := range sent {
		if !strings.Contains(m.Text, "watchfire telegram pair") {
			t.Fatalf("reply is not the pairing instructions: %q", m.Text)
		}
		if strings.Contains(m.Text, "project") {
			t.Fatalf("reply leaks project data to an unpaired chat: %q", m.Text)
		}
	}

	// Nothing was persisted and the code is still live.
	cfg, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if cfg.Telegram != nil && len(cfg.Telegram.PairedChats) != 0 {
		t.Fatalf("unpaired probing persisted a chat: %+v", cfg.Telegram.PairedChats)
	}
	if st := pairing.Status(); st.State != StatePending {
		t.Fatalf("pairing state disturbed by bad codes: %+v", st)
	}
}

// TestBridgeExpiredCodeRefused: a TTL-lapsed code draws the pairing
// instructions, not a pairing.
func TestBridgeExpiredCodeRefused(t *testing.T) {
	withTestEnv(t)
	pairing := NewPairing()
	now := time.Now()
	pairing.now = func() time.Time { return now }
	code, expires, err := pairing.Begin(10 * time.Minute)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	now = expires.Add(time.Minute) // lapse the TTL

	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/start "+code))
	b := New(Config{Token: testToken, Pairing: pairing, Hostname: "myhost"})
	startBridge(t, b)

	waitFor(t, "instructions reply", func() bool { return len(fake.sentMessages()) >= 1 })
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "watchfire telegram pair") {
		t.Fatalf("expired code should draw instructions, got: %q", txt)
	}
	cfg, _ := config.LoadIntegrations()
	if cfg.Telegram != nil && len(cfg.Telegram.PairedChats) != 0 {
		t.Fatal("expired code persisted a pairing")
	}
	if st := pairing.Status(); st.State != StateExpired {
		t.Fatalf("state = %v, want expired", st.State)
	}
}

// TestBridgeOffsetTracking: the offset always advances past processed
// updates so nothing is ever reprocessed.
func TestBridgeOffsetTracking(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		updateJSON(100, 99, 99, "x", "ignored text"),
		updateJSON(101, 99, 99, "x", "more ignored text"),
	)
	b := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h"})
	startBridge(t, b)

	waitFor(t, "three polls", func() bool { return len(fake.recordedOffsets()) >= 3 })
	offsets := fake.recordedOffsets()
	if offsets[0] != "" {
		t.Fatalf("first poll offset = %q, want unset", offsets[0])
	}
	if offsets[1] != "101" {
		t.Fatalf("second poll offset = %q, want 101 (ack update 100)", offsets[1])
	}
	if offsets[2] != "102" {
		t.Fatalf("third poll offset = %q, want 102 (ack update 101)", offsets[2])
	}
}

// TestBridgeBackoffOn429AndErrors: network/API failures back off
// exponentially; a 429 honours retry_after instead.
func TestBridgeBackoffOn429AndErrors(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t,
		`STATUS 500 {"ok":false,"description":"boom"}`,
		`STATUS 500 {"ok":false,"description":"boom again"}`,
		`STATUS 429 {"ok":false,"description":"Too Many Requests","parameters":{"retry_after":7}}`,
		updateJSON(1, 99, 99, "x", "ignored"),
	)
	b := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h"})

	var mu sync.Mutex
	var sleeps []time.Duration
	b.sleepFn = func(_ context.Context, d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	startBridge(t, b)

	waitFor(t, "recovery poll after failures", func() bool { return len(fake.recordedOffsets()) >= 5 })
	mu.Lock()
	got := append([]time.Duration(nil), sleeps...)
	mu.Unlock()
	if len(got) < 3 {
		t.Fatalf("expected 3 backoff sleeps, got %v", got)
	}
	if got[0] != time.Second || got[1] != 2*time.Second {
		t.Fatalf("exponential backoff wrong: %v", got[:2])
	}
	if got[2] != 7*time.Second {
		t.Fatalf("429 must honour retry_after=7s, got %v", got[2])
	}
}

// TestBridgeBackoffCap: repeated failures never exceed the 60s cap.
func TestBridgeBackoffCap(t *testing.T) {
	withTestEnv(t)
	script := make([]string, 10)
	for i := range script {
		script[i] = `STATUS 500 {"ok":false,"description":"down"}`
	}
	fake := newFakeBotAPI(t, script...)
	b := New(Config{Token: testToken, Pairing: NewPairing(), Hostname: "h"})

	var mu sync.Mutex
	var sleeps []time.Duration
	b.sleepFn = func(_ context.Context, d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	startBridge(t, b)

	waitFor(t, "script drained", fake.scriptDrained)
	waitFor(t, "ten sleeps", func() bool { mu.Lock(); defer mu.Unlock(); return len(sleeps) >= 10 })
	mu.Lock()
	defer mu.Unlock()
	for i, d := range sleeps {
		if d > maxBackoff {
			t.Fatalf("sleep %d exceeded cap: %v", i, d)
		}
	}
	if sleeps[9] != maxBackoff {
		t.Fatalf("backoff should have reached the %s cap, got %v", maxBackoff, sleeps[9])
	}
}

// TestBridgeRevokeDropsChatImmediately: a revoked chat is treated as
// unpaired without any restart.
func TestBridgeRevokeDropsChatImmediately(t *testing.T) {
	withTestEnv(t)
	pairing := NewPairing()
	fake := newFakeBotAPI(t,
		updateJSON(1, 42, 42, "nuno", "some chatter"),
		updateJSON(2, 42, 42, "nuno", "/pair WRONGCOD"),
	)
	b := New(Config{
		Token:    testToken,
		Pairing:  pairing,
		Hostname: "h",
		PairedChats: []models.TelegramPairedChat{
			{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now()},
		},
	})
	if !b.IsPaired(42) {
		t.Fatal("seeded chat not on allowlist")
	}
	b.Revoke(42)
	if b.IsPaired(42) {
		t.Fatal("revoked chat still on allowlist")
	}

	startBridge(t, b)
	waitFor(t, "script drained", fake.scriptDrained)
	waitFor(t, "instructions reply", func() bool { return len(fake.sentMessages()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	sent := fake.sentMessages()
	// The chatter drew silence; the /pair probe drew the unpaired
	// instructions (not the "already paired" nudge).
	if len(sent) != 1 {
		t.Fatalf("revoked chat drew %d replies, want 1: %+v", len(sent), sent)
	}
	if !strings.Contains(sent[0].Text, "watchfire telegram pair") {
		t.Fatalf("revoked chat should get unpaired instructions, got: %q", sent[0].Text)
	}
}

// TestBridgePairedChatNudgeOnBadPair: an already-paired chat probing
// /pair with a stale code gets the "already paired" nudge, not the
// stranger instructions.
func TestBridgePairedChatNudgeOnBadPair(t *testing.T) {
	withTestEnv(t)
	fake := newFakeBotAPI(t, updateJSON(1, 42, 42, "nuno", "/pair OLDCODEX"))
	b := New(Config{
		Token:    testToken,
		Pairing:  NewPairing(),
		Hostname: "h",
		PairedChats: []models.TelegramPairedChat{
			{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now()},
		},
	})
	startBridge(t, b)
	waitFor(t, "nudge reply", func() bool { return len(fake.sentMessages()) >= 1 })
	if txt := fake.sentMessages()[0].Text; !strings.Contains(txt, "already paired") {
		t.Fatalf("paired chat should get the already-paired nudge, got: %q", txt)
	}
}

// TestNewFromConfigGating: the bridge constructor returns nil — i.e. no
// goroutine will start — unless Telegram is enabled AND a token resolved.
func TestNewFromConfigGating(t *testing.T) {
	pairing := NewPairing()
	cases := []struct {
		name string
		cfg  *models.IntegrationsConfig
		want bool
	}{
		{"nil config", nil, false},
		{"no telegram block", &models.IntegrationsConfig{}, false},
		{"disabled", &models.IntegrationsConfig{Telegram: &models.TelegramConfig{Enabled: false, BotToken: testToken}}, false},
		{"enabled without token", &models.IntegrationsConfig{Telegram: &models.TelegramConfig{Enabled: true}}, false},
		{"enabled with unresolved ref", &models.IntegrationsConfig{Telegram: &models.TelegramConfig{Enabled: true, BotTokenRef: "watchfire.integration.telegram.bot_token"}}, false},
		{"enabled with token", &models.IntegrationsConfig{Telegram: &models.TelegramConfig{Enabled: true, BotToken: testToken}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewFromConfig(tc.cfg, pairing, "h", nil)
			if (got != nil) != tc.want {
				t.Fatalf("NewFromConfig = %v, want bridge=%v", got, tc.want)
			}
		})
	}
}

// TestParseCommand covers the @botname suffix Telegram appends in
// group chats and non-command text.
func TestParseCommand(t *testing.T) {
	cases := []struct {
		in, cmd, arg string
	}{
		{"/start ABCD2345", "/start", "ABCD2345"},
		{"/pair@WatchfireBot ABCD2345", "/pair", "ABCD2345"},
		{"/START abcd2345", "/start", "abcd2345"},
		{"/pair", "/pair", ""},
		{"hello there", "", ""},
		{"", "", ""},
		{"   /start   X  Y ", "/start", "X"},
	}
	for _, tc := range cases {
		cmd, arg := parseCommand(tc.in)
		if cmd != tc.cmd || arg != tc.arg {
			t.Errorf("parseCommand(%q) = (%q, %q), want (%q, %q)", tc.in, cmd, arg, tc.cmd, tc.arg)
		}
	}
}
