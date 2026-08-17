package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/telegram"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

func tgPairingSetup(t *testing.T) (*integrationsService, *fakeInboundProvider) {
	t.Helper()
	tgTestSetup(t)
	svc := newIntegrationsService()
	provider := &fakeInboundProvider{pairing: telegram.NewPairing()}
	svc.bindEchoServer(provider)
	return svc, provider
}

func TestBeginTelegramPairingRequiresRunningBridge(t *testing.T) {
	svc, _ := tgPairingSetup(t)

	_, err := svc.BeginTelegramPairing(context.Background(), &pb.BeginTelegramPairingRequest{})
	if err == nil {
		t.Fatal("BeginTelegramPairing must fail when the bridge is not running")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("error code = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestBeginTelegramPairingAndStatusLifecycle(t *testing.T) {
	svc, provider := tgPairingSetup(t)

	// Live bridge against a fake Bot API that answers getMe.
	srv := newTgFakeGetMeServer(t, "watchfire_bot")
	provider.bridge = telegram.New(telegram.Config{
		Token:   tgTestToken,
		Pairing: provider.pairing,
	})
	_ = srv // APIBase already routed by the helper

	// No code yet → none, but the bridge reads as running.
	st, err := svc.GetTelegramPairingStatus(context.Background(), &pb.GetTelegramPairingStatusRequest{})
	if err != nil {
		t.Fatalf("GetTelegramPairingStatus: %v", err)
	}
	if st.GetState() != pb.TelegramPairingState_TELEGRAM_PAIRING_NONE || !st.GetBridgeRunning() {
		t.Fatalf("initial status mismatch: %+v", st)
	}

	begin, err := svc.BeginTelegramPairing(context.Background(), &pb.BeginTelegramPairingRequest{})
	if err != nil {
		t.Fatalf("BeginTelegramPairing: %v", err)
	}
	if len(begin.GetCode()) != telegram.CodeLength {
		t.Fatalf("code length = %d, want %d", len(begin.GetCode()), telegram.CodeLength)
	}
	wantLink := "https://t.me/watchfire_bot?start=" + begin.GetCode()
	if begin.GetDeepLink() != wantLink {
		t.Fatalf("deep link = %q, want %q", begin.GetDeepLink(), wantLink)
	}
	if begin.GetBotUsername() != "watchfire_bot" {
		t.Fatalf("bot username = %q", begin.GetBotUsername())
	}
	if until := time.Until(begin.GetExpiresAt().AsTime()); until < 9*time.Minute || until > 10*time.Minute+time.Second {
		t.Fatalf("expiry out of the 10-minute range: %s", until)
	}

	st, err = svc.GetTelegramPairingStatus(context.Background(), &pb.GetTelegramPairingStatusRequest{})
	if err != nil {
		t.Fatalf("GetTelegramPairingStatus: %v", err)
	}
	if st.GetState() != pb.TelegramPairingState_TELEGRAM_PAIRING_PENDING {
		t.Fatalf("state after Begin = %v, want PENDING", st.GetState())
	}
	if st.GetBotUsername() != "watchfire_bot" {
		t.Fatalf("cached bot username missing from status: %+v", st)
	}

	// Simulate the bridge redeeming the code (what the poll loop does).
	if !provider.pairing.Consume(begin.GetCode()) {
		t.Fatal("minted code failed to redeem")
	}
	provider.pairing.Complete(models.TelegramPairedChat{
		ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now().UTC(),
	})

	st, err = svc.GetTelegramPairingStatus(context.Background(), &pb.GetTelegramPairingStatusRequest{})
	if err != nil {
		t.Fatalf("GetTelegramPairingStatus: %v", err)
	}
	if st.GetState() != pb.TelegramPairingState_TELEGRAM_PAIRING_PAIRED {
		t.Fatalf("state after redeem = %v, want PAIRED", st.GetState())
	}
	if st.GetChat().GetChatId() != 42 || st.GetChat().GetUsername() != "nuno" {
		t.Fatalf("paired chat info mismatch: %+v", st.GetChat())
	}
}

func TestRevokeTelegramChat(t *testing.T) {
	svc, provider := tgPairingSetup(t)
	provider.bridge = telegram.New(telegram.Config{
		Token:   tgTestToken,
		Pairing: provider.pairing,
		PairedChats: []models.TelegramPairedChat{
			{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now().UTC()},
			{ChatID: 77, UserID: 77, Username: "other", PairedAt: time.Now().UTC()},
		},
	})

	seed := &models.IntegrationsConfig{
		Telegram: &models.TelegramConfig{
			Enabled: true,
			PairedChats: []models.TelegramPairedChat{
				{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now().UTC()},
				{ChatID: 77, UserID: 77, Username: "other", PairedAt: time.Now().UTC()},
			},
		},
	}
	if err := config.SaveIntegrations(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := svc.RevokeTelegramChat(context.Background(), &pb.RevokeTelegramChatRequest{ChatId: 42})
	if err != nil {
		t.Fatalf("RevokeTelegramChat: %v", err)
	}
	chats := resp.GetTelegram().GetPairedChats()
	if len(chats) != 1 || chats[0].GetChatId() != 77 {
		t.Fatalf("revoke response should keep only chat 77: %+v", chats)
	}

	// Persisted and dropped from the live bridge immediately.
	cfg, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if len(cfg.Telegram.PairedChats) != 1 || cfg.Telegram.PairedChats[0].ChatID != 77 {
		t.Fatalf("revoke not persisted: %+v", cfg.Telegram.PairedChats)
	}
	if provider.bridge.IsPaired(42) {
		t.Fatal("bridge still lists revoked chat 42")
	}
	if !provider.bridge.IsPaired(77) {
		t.Fatal("bridge dropped the wrong chat")
	}

	// Revoking an unknown chat is NotFound.
	if _, err := svc.RevokeTelegramChat(context.Background(), &pb.RevokeTelegramChatRequest{ChatId: 999}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown chat revoke error = %v, want NotFound", err)
	}
}

func TestSaveTelegramTriggersBridgeRestart(t *testing.T) {
	svc, provider := tgPairingSetup(t)

	if _, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Telegram{
			Telegram: &pb.TelegramIntegration{Enabled: true, BotToken: tgTestToken},
		},
	}); err != nil {
		t.Fatalf("SaveIntegration: %v", err)
	}
	if provider.telegramRestartHits != 1 {
		t.Fatalf("telegram save restart hits = %d, want 1", provider.telegramRestartHits)
	}

	// A non-telegram save must NOT bounce the bridge.
	if _, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Github{
			Github: &pb.GitHubIntegration{Enabled: true},
		},
	}); err != nil {
		t.Fatalf("SaveIntegration github: %v", err)
	}
	if provider.telegramRestartHits != 1 {
		t.Fatalf("github save bounced the telegram bridge: hits = %d", provider.telegramRestartHits)
	}

	// Deleting the telegram integration bounces it again.
	if _, err := svc.DeleteIntegration(context.Background(), &pb.DeleteIntegrationRequest{
		Kind: pb.IntegrationKind_TELEGRAM,
	}); err != nil {
		t.Fatalf("DeleteIntegration: %v", err)
	}
	if provider.telegramRestartHits != 2 {
		t.Fatalf("telegram delete restart hits = %d, want 2", provider.telegramRestartHits)
	}
}

// newTgFakeGetMeServer stands up a fake Bot API answering getMe with
// the given username and routes telegrambot.APIBase at it for the test.
func newTgFakeGetMeServer(t *testing.T, username string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"description":"unexpected method"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"id": 7, "is_bot": true, "username": username},
		})
	}))
	t.Cleanup(srv.Close)
	prev := telegrambot.APIBase
	telegrambot.APIBase = srv.URL
	t.Cleanup(func() { telegrambot.APIBase = prev })
	return srv.URL
}

func TestGetTelegramPairingStatusWithoutProvider(t *testing.T) {
	tgTestSetup(t)
	svc := newIntegrationsService() // no bindEchoServer at all

	st, err := svc.GetTelegramPairingStatus(context.Background(), &pb.GetTelegramPairingStatusRequest{})
	if err != nil {
		t.Fatalf("GetTelegramPairingStatus: %v", err)
	}
	if st.GetBridgeRunning() || st.GetState() != pb.TelegramPairingState_TELEGRAM_PAIRING_NONE {
		t.Fatalf("unbound service status mismatch: %+v", st)
	}
	_, beginErr := svc.BeginTelegramPairing(context.Background(), &pb.BeginTelegramPairingRequest{})
	if status.Code(beginErr) != codes.FailedPrecondition {
		t.Fatalf("Begin without provider = %v, want FailedPrecondition", beginErr)
	}
	if !strings.Contains(beginErr.Error(), "bridge is not running") {
		t.Fatalf("error message should be actionable: %q", beginErr.Error())
	}
}
