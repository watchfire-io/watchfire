package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// fakeTelegramSendAPI stands in for api.telegram.org and records every
// sendMessage POST (chat_id + text). failChats forces per-chat API
// errors so partial-failure reporting can be asserted.
type fakeTelegramSendAPI struct {
	mu        sync.Mutex
	sends     map[int64][]string // chat_id → texts delivered
	failChats map[int64]bool
}

func startFakeTelegramSendAPI(t *testing.T) *fakeTelegramSendAPI {
	t.Helper()
	f := &fakeTelegramSendAPI{sends: make(map[int64][]string), failChats: make(map[int64]bool)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
			return
		}
		_ = r.ParseForm()
		chatID, _ := strconv.ParseInt(r.PostFormValue("chat_id"), 10, 64)
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failChats[chatID] {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"ok":false,"description":"chat not found"}`)
			return
		}
		f.sends[chatID] = append(f.sends[chatID], r.PostFormValue("text"))
		fmt.Fprintf(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":%d}}}`, chatID)
	}))
	prev := telegrambot.APIBase
	telegrambot.APIBase = srv.URL
	t.Cleanup(func() {
		telegrambot.APIBase = prev
		srv.Close()
	})
	return f
}

func (f *fakeTelegramSendAPI) TextsFor(chatID int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sends[chatID]))
	copy(out, f.sends[chatID])
	return out
}

func saveTelegramSendConfig(t *testing.T, chats ...models.TelegramPairedChat) {
	t.Helper()
	if err := config.SaveIntegrations(&models.IntegrationsConfig{
		Telegram: &models.TelegramConfig{
			Enabled:       true,
			BotToken:      tgTestToken,
			EnabledEvents: models.EventBitmask{TaskFailed: true, RunComplete: true, WeeklyDigest: true},
			PairedChats:   chats,
		},
	}); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}
}

// TestTestIntegrationTelegramDeliversPerChat asserts the TELEGRAM test
// sends all three synthetic kinds to every non-muted paired chat, skips
// muted chats, and reports per-chat success.
func TestTestIntegrationTelegramDeliversPerChat(t *testing.T) {
	tgTestSetup(t)
	api := startFakeTelegramSendAPI(t)
	saveTelegramSendConfig(t,
		models.TelegramPairedChat{ChatID: 111, Username: "nuno"},
		models.TelegramPairedChat{ChatID: 222, Username: "muted-user", Muted: true},
		models.TelegramPairedChat{ChatID: 333},
	)

	svc := newIntegrationsService()
	resp, err := svc.TestIntegration(context.Background(), &pb.TestIntegrationRequest{
		Kind: pb.IntegrationKind_TELEGRAM,
	})
	if err != nil {
		t.Fatalf("TestIntegration: %v", err)
	}
	if !resp.GetOk() {
		t.Fatalf("expected ok=true, got msg=%q", resp.GetMessage())
	}
	for _, chatID := range []int64{111, 333} {
		texts := api.TextsFor(chatID)
		if len(texts) != 3 {
			t.Fatalf("chat %d: want 3 messages (one per kind), got %d", chatID, len(texts))
		}
		joined := strings.Join(texts, "\n---\n")
		for _, want := range []string{"Task failed", "Run complete", "your week"} {
			if !strings.Contains(joined, want) {
				t.Errorf("chat %d: missing %q in delivered texts:\n%s", chatID, want, joined)
			}
		}
	}
	if got := api.TextsFor(222); len(got) != 0 {
		t.Fatalf("muted chat must not receive test messages, got %d", len(got))
	}
	msg := resp.GetMessage()
	if !strings.Contains(msg, "@nuno: OK") || !strings.Contains(msg, "chat 333: OK") {
		t.Fatalf("message should report per-chat success, got %q", msg)
	}
	if strings.Contains(msg, "muted-user") {
		t.Fatalf("muted chat should not appear in the report, got %q", msg)
	}
}

// TestTestIntegrationTelegramReportsPartialFailure asserts a failing
// chat flips ok=false and is named in the report while healthy chats
// still deliver.
func TestTestIntegrationTelegramReportsPartialFailure(t *testing.T) {
	tgTestSetup(t)
	api := startFakeTelegramSendAPI(t)
	api.failChats[333] = true
	saveTelegramSendConfig(t,
		models.TelegramPairedChat{ChatID: 111, Username: "nuno"},
		models.TelegramPairedChat{ChatID: 333},
	)

	svc := newIntegrationsService()
	resp, err := svc.TestIntegration(context.Background(), &pb.TestIntegrationRequest{
		Kind: pb.IntegrationKind_TELEGRAM,
	})
	if err != nil {
		t.Fatalf("TestIntegration: %v", err)
	}
	if resp.GetOk() {
		t.Fatal("expected ok=false on partial failure")
	}
	msg := resp.GetMessage()
	if !strings.Contains(msg, "chat 333") {
		t.Fatalf("failing chat should be named in report, got %q", msg)
	}
	if !strings.Contains(msg, "@nuno: OK") {
		t.Fatalf("healthy chat should still report OK, got %q", msg)
	}
	if got := api.TextsFor(111); len(got) != 3 {
		t.Fatalf("healthy chat should receive all 3 kinds, got %d", len(got))
	}
}
