package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
	"github.com/watchfire-io/watchfire/internal/models"
)

// fakeTelegramAPI is an httptest stand-in for api.telegram.org. It
// records every sendMessage (chat_id + text) and can be told to fail
// specific chats (per-chat 400) or everything (500).
type fakeTelegramAPI struct {
	mu        sync.Mutex
	sends     []fakeTelegramSend
	failChats map[int64]bool
	failAll   bool

	server *httptest.Server
}

type fakeTelegramSend struct {
	ChatID int64
	Text   string
}

// startFakeTelegramAPI spins up the fake and points telegrambot.APIBase
// at it for the duration of t. Tests using it must not run in parallel
// (APIBase is a package global).
func startFakeTelegramAPI(t *testing.T) *fakeTelegramAPI {
	t.Helper()
	f := &fakeTelegramAPI{failChats: make(map[int64]bool)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		chatID, _ := strconv.ParseInt(r.PostFormValue("chat_id"), 10, 64)
		f.mu.Lock()
		failAll := f.failAll
		failChat := f.failChats[chatID]
		if !failAll && !failChat {
			f.sends = append(f.sends, fakeTelegramSend{ChatID: chatID, Text: r.PostFormValue("text")})
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if failAll {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"ok":false,"description":"synthetic outage"}`)
			return
		}
		if failChat {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"ok":false,"description":"chat not found"}`)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":%d}}}`, chatID)
	}))
	prev := telegrambot.APIBase
	telegrambot.APIBase = f.server.URL
	t.Cleanup(func() {
		telegrambot.APIBase = prev
		f.server.Close()
	})
	return f
}

func (f *fakeTelegramAPI) Sends() []fakeTelegramSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeTelegramSend, len(f.sends))
	copy(out, f.sends)
	return out
}

func telegramTestConfig(chats ...models.TelegramPairedChat) models.TelegramConfig {
	return models.TelegramConfig{
		Enabled:  true,
		BotToken: "12345:test-token",
		EnabledEvents: models.EventBitmask{
			TaskFailed:   true,
			RunComplete:  true,
			WeeklyDigest: true,
		},
		PairedChats: chats,
	}
}

var telegramSnapshotTime = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// TestFormatTelegramMessageSnapshots pins the exact HTML wire text per
// notification kind, including entity escaping of user-controlled
// fields.
func TestFormatTelegramMessageSnapshots(t *testing.T) {
	cases := []struct {
		name    string
		payload Payload
		want    string
	}{
		{
			name: "task_failed",
			payload: Payload{
				Kind:              string(notify.KindTaskFailed),
				EmittedAt:         telegramSnapshotTime,
				ProjectName:       "Watchfire & co",
				TaskNumber:        7,
				TaskTitle:         "Fix <tui> crash",
				TaskFailureReason: "exit code > 0",
			},
			want: "🚨 <b>Task failed — Watchfire &amp; co</b>\n" +
				"<b>Task #0007</b>: Fix &lt;tui&gt; crash\n" +
				"<b>Reason</b>: exit code &gt; 0\n" +
				"<i>Watchfire &amp; co · 2026-08-17T12:00:00Z</i>",
		},
		{
			name: "run_complete",
			payload: Payload{
				Kind:        string(notify.KindRunComplete),
				EmittedAt:   telegramSnapshotTime,
				ProjectName: "Watchfire",
				TaskNumber:  12,
				TaskTitle:   "Ship it",
			},
			want: "✅ <b>Run complete — Watchfire</b>\n" +
				"<b>Task #0012</b>: Ship it\n" +
				"<i>Watchfire · 2026-08-17T12:00:00Z</i>",
		},
		{
			name: "weekly_digest",
			payload: Payload{
				Kind:       string(notify.KindWeeklyDigest),
				EmittedAt:  telegramSnapshotTime,
				DigestDate: "2026-08-17",
				DigestBody: "## Your week\n\n5 tasks done & <2 failed>",
			},
			want: "📊 <b>Watchfire — your week (2026-08-17)</b>\n" +
				"## Your week\n\n5 tasks done &amp; &lt;2 failed&gt;\n" +
				"<i>Weekly digest · 2026-08-17T12:00:00Z</i>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FormatTelegramMessage(tc.payload)
			if err != nil {
				t.Fatalf("FormatTelegramMessage: %v", err)
			}
			if got != tc.want {
				t.Fatalf("snapshot mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestFormatTelegramMessageUnknownKind asserts unsupported kinds error
// instead of sending an empty message.
func TestFormatTelegramMessageUnknownKind(t *testing.T) {
	if _, err := FormatTelegramMessage(Payload{Kind: "MYSTERY"}); err == nil {
		t.Fatal("expected error for unsupported kind, got nil")
	}
}

// TestFormatTelegramMessageDigestTruncates asserts a huge digest body
// is snipped well below Telegram's 4096-char cap.
func TestFormatTelegramMessageDigestTruncates(t *testing.T) {
	got, err := FormatTelegramMessage(Payload{
		Kind:       string(notify.KindWeeklyDigest),
		EmittedAt:  telegramSnapshotTime,
		DigestBody: strings.Repeat("a", 5000),
	})
	if err != nil {
		t.Fatalf("FormatTelegramMessage: %v", err)
	}
	if len(got) >= 4096 {
		t.Fatalf("digest message not truncated: %d chars", len(got))
	}
	if !strings.Contains(got, "…") {
		t.Fatal("expected truncation ellipsis in digest body")
	}
}

// TestTelegramSupportsHonorsEnabledEvents asserts the per-event bitmask
// gates Supports.
func TestTelegramSupportsHonorsEnabledEvents(t *testing.T) {
	cfg := telegramTestConfig()
	cfg.EnabledEvents = models.EventBitmask{TaskFailed: true}
	a := NewTelegramAdapter(cfg, nil, nil)
	if !a.Supports(notify.KindTaskFailed) {
		t.Fatal("TASK_FAILED should be supported")
	}
	if a.Supports(notify.KindRunComplete) || a.Supports(notify.KindWeeklyDigest) {
		t.Fatal("RUN_COMPLETE / WEEKLY_DIGEST should be gated off")
	}
	if a.Supports(notify.Kind("MYSTERY")) {
		t.Fatal("unknown kinds must not be supported")
	}
}

// TestTelegramSendSkipsMutedChats asserts the global per-chat Muted
// flag suppresses delivery to that chat only.
func TestTelegramSendSkipsMutedChats(t *testing.T) {
	api := startFakeTelegramAPI(t)
	cfg := telegramTestConfig(
		models.TelegramPairedChat{ChatID: 111},
		models.TelegramPairedChat{ChatID: 222, Muted: true},
		models.TelegramPairedChat{ChatID: 333},
	)
	a := NewTelegramAdapter(cfg, telegrambot.New(), nil)

	err := a.Send(context.Background(), Payload{
		Kind:        string(notify.KindTaskFailed),
		EmittedAt:   telegramSnapshotTime,
		ProjectName: "p",
		TaskNumber:  1,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	sends := api.Sends()
	if len(sends) != 2 {
		t.Fatalf("expected 2 sends (muted chat skipped), got %d: %+v", len(sends), sends)
	}
	if sends[0].ChatID != 111 || sends[1].ChatID != 333 {
		t.Fatalf("wrong chats delivered: %+v", sends)
	}
}

// TestTelegramSendAggregatesPartialFailure asserts a per-chat failure
// still delivers to the healthy chats and surfaces one aggregate error
// so the dispatcher's retry policy applies.
func TestTelegramSendAggregatesPartialFailure(t *testing.T) {
	api := startFakeTelegramAPI(t)
	api.failChats[222] = true
	cfg := telegramTestConfig(
		models.TelegramPairedChat{ChatID: 111},
		models.TelegramPairedChat{ChatID: 222},
		models.TelegramPairedChat{ChatID: 333},
	)
	a := NewTelegramAdapter(cfg, telegrambot.New(), nil)

	err := a.Send(context.Background(), Payload{
		Kind:        string(notify.KindRunComplete),
		EmittedAt:   telegramSnapshotTime,
		ProjectName: "p",
		TaskNumber:  1,
	})
	if err == nil {
		t.Fatal("expected aggregate error on partial failure, got nil")
	}
	if !strings.Contains(err.Error(), "chat 222") {
		t.Fatalf("aggregate error should name the failing chat: %v", err)
	}
	sends := api.Sends()
	if len(sends) != 2 || sends[0].ChatID != 111 || sends[1].ChatID != 333 {
		t.Fatalf("healthy chats should still be delivered: %+v", sends)
	}
}

// TestTelegramSendNoTokenErrors asserts a missing (keyring-miss) token
// fails loudly rather than silently no-oping.
func TestTelegramSendNoTokenErrors(t *testing.T) {
	cfg := telegramTestConfig(models.TelegramPairedChat{ChatID: 111})
	cfg.BotToken = ""
	a := NewTelegramAdapter(cfg, telegrambot.New(), nil)
	if err := a.Send(context.Background(), Payload{Kind: string(notify.KindTaskFailed)}); err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

// TestDispatcherTelegramBreakerIsolated proves a hard-failing Telegram
// adapter trips its own circuit breaker while Slack/Discord-style
// adapters keep delivering untouched.
func TestDispatcherTelegramBreakerIsolated(t *testing.T) {
	api := startFakeTelegramAPI(t)
	api.failAll = true

	cfg := telegramTestConfig(models.TelegramPairedChat{ChatID: 111})
	tg := NewTelegramAdapter(cfg, telegrambot.New(), nil)
	healthy := &stubAdapter{
		id:       "slack-1",
		supports: map[notify.Kind]bool{notify.KindTaskFailed: true},
	}

	bus := notify.NewBus()
	d := NewDispatcher(
		bus,
		passthroughResolver,
		func() ([]Adapter, error) { return []Adapter{tg, healthy}, nil },
		WithRetryDelays(nil), // no retries: each emit = one hard failure
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	defer d.Stop()

	// Three hard failures open the breaker (default threshold 3).
	for i := 0; i < 4; i++ {
		bus.Emit(notify.Notification{
			Kind:      notify.KindTaskFailed,
			ProjectID: "p1",
			EmittedAt: time.Now(),
		})
	}

	if !waitFor(t, time.Second, func() bool { return len(healthy.Calls()) == 4 }) {
		t.Fatalf("healthy adapter should receive all 4 sends, got %d", len(healthy.Calls()))
	}
	if !d.breakerOpen("telegram") {
		t.Fatal("telegram breaker should be open after 3 hard failures")
	}
	if d.breakerOpen("slack-1") {
		t.Fatal("healthy adapter's breaker must stay closed")
	}
}
