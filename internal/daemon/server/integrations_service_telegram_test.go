package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

const tgTestToken = "123456:test-telegram-token"

func tgTestSetup(t *testing.T) *memSecretStore {
	t.Helper()
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })
	return mem
}

func TestTelegramSaveListScrubsToken(t *testing.T) {
	mem := tgTestSetup(t)
	svc := newIntegrationsService()

	resp, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Telegram{
			Telegram: &pb.TelegramIntegration{
				Enabled:  true,
				BotToken: tgTestToken,
				EnabledEvents: &pb.IntegrationEvents{
					TaskFailed:  true,
					RunComplete: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveIntegration: %v", err)
	}

	tg := resp.GetTelegram()
	if tg == nil {
		t.Fatal("telegram missing from Save response")
	}
	if !tg.GetEnabled() || !tg.GetTokenSet() {
		t.Fatalf("enabled/token_set mismatch: %+v", tg)
	}
	if tg.GetBotToken() != "" {
		t.Fatalf("bot_token leaked in response: %q", tg.GetBotToken())
	}
	if !tg.GetEnabledEvents().GetTaskFailed() || tg.GetEnabledEvents().GetWeeklyDigest() {
		t.Fatalf("events mismatch: %+v", tg.GetEnabledEvents())
	}

	// Keyring holds the token; YAML does not.
	key := config.SecretKeyForIntegration("telegram", "bot_token")
	if v, ok := mem.Get(key); !ok || v != tgTestToken {
		t.Fatalf("keyring token mismatch: ok=%v v=%q", ok, v)
	}
	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".watchfire", "integrations.yaml"))
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	if strings.Contains(string(raw), tgTestToken) {
		t.Fatalf("token leaked into YAML:\n%s", raw)
	}

	// List agrees with the Save response.
	listed, err := svc.ListIntegrations(context.Background(), &pb.ListIntegrationsRequest{})
	if err != nil {
		t.Fatalf("ListIntegrations: %v", err)
	}
	if lt := listed.GetTelegram(); lt == nil || !lt.GetTokenSet() || lt.GetBotToken() != "" {
		t.Fatalf("List scrub mismatch: %+v", lt)
	}
}

func TestTelegramSaveEmptyTokenKeepsKeyringEntry(t *testing.T) {
	mem := tgTestSetup(t)
	svc := newIntegrationsService()

	save := func(token string, enabled bool) *pb.IntegrationsConfig {
		t.Helper()
		resp, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
			Payload: &pb.SaveIntegrationRequest_Telegram{
				Telegram: &pb.TelegramIntegration{Enabled: enabled, BotToken: token},
			},
		})
		if err != nil {
			t.Fatalf("SaveIntegration: %v", err)
		}
		return resp
	}

	save(tgTestToken, true)
	resp := save("", false) // update settings without re-entering the token

	if resp.GetTelegram().GetEnabled() {
		t.Fatal("enabled flag not updated")
	}
	if !resp.GetTelegram().GetTokenSet() {
		t.Fatal("empty token on update should keep the stored token")
	}
	key := config.SecretKeyForIntegration("telegram", "bot_token")
	if v, ok := mem.Get(key); !ok || v != tgTestToken {
		t.Fatalf("keyring entry lost on tokenless update: ok=%v v=%q", ok, v)
	}
}

func TestTelegramSaveCannotForgePairingButTogglesFlags(t *testing.T) {
	tgTestSetup(t)

	// Seed a paired chat through the config layer (the pairing flow's
	// write path).
	seeded := &models.IntegrationsConfig{
		Telegram: &models.TelegramConfig{
			Enabled:  true,
			BotToken: tgTestToken,
			PairedChats: []models.TelegramPairedChat{{
				ChatID:   111,
				UserID:   111,
				Username: "nuno",
				PairedAt: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
			}},
		},
	}
	if err := config.SaveIntegrations(seeded); err != nil {
		t.Fatalf("seed SaveIntegrations: %v", err)
	}

	svc := newIntegrationsService()
	resp, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Telegram{
			Telegram: &pb.TelegramIntegration{
				Enabled: true,
				PairedChats: []*pb.TelegramPairedChatInfo{
					{ChatId: 111, Muted: true, Watch: true, DefaultProjectId: "proj-9"},
					{ChatId: 666, Username: "attacker"}, // must NOT be added
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveIntegration: %v", err)
	}

	chats := resp.GetTelegram().GetPairedChats()
	if len(chats) != 1 {
		t.Fatalf("Save must never add paired chats; got %d", len(chats))
	}
	pc := chats[0]
	if pc.GetChatId() != 111 || !pc.GetMuted() || !pc.GetWatch() || pc.GetDefaultProjectId() != "proj-9" {
		t.Fatalf("per-chat toggles not applied: %+v", pc)
	}
	if pc.GetUsername() != "nuno" {
		t.Fatalf("username must come from the pairing record, got %q", pc.GetUsername())
	}
}

func TestTelegramDeleteRemovesConfigAndSecret(t *testing.T) {
	mem := tgTestSetup(t)
	svc := newIntegrationsService()

	if _, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Telegram{
			Telegram: &pb.TelegramIntegration{Enabled: true, BotToken: tgTestToken},
		},
	}); err != nil {
		t.Fatalf("SaveIntegration: %v", err)
	}

	resp, err := svc.DeleteIntegration(context.Background(), &pb.DeleteIntegrationRequest{
		Kind: pb.IntegrationKind_TELEGRAM,
	})
	if err != nil {
		t.Fatalf("DeleteIntegration: %v", err)
	}
	if resp.GetTelegram() != nil {
		t.Fatalf("telegram config survived delete: %+v", resp.GetTelegram())
	}
	key := config.SecretKeyForIntegration("telegram", "bot_token")
	if _, ok := mem.Get(key); ok {
		t.Fatal("keyring token survived delete")
	}
}

// TestTelegramTestIntegrationRequiresChats pins the v10 task-0138
// semantics: "Test" now delivers real messages to paired chats (see
// integrations_service_telegram_send_test.go), so an unconfigured
// bridge or one with no unmuted paired chats reports an honest failure
// instead of a getMe-only success.
func TestTelegramTestIntegrationRequiresChats(t *testing.T) {
	tgTestSetup(t)

	svc := newIntegrationsService()

	// Unconfigured → honest failure, no RPC error.
	resp, err := svc.TestIntegration(context.Background(), &pb.TestIntegrationRequest{Kind: pb.IntegrationKind_TELEGRAM})
	if err != nil {
		t.Fatalf("TestIntegration (unconfigured): %v", err)
	}
	if resp.GetOk() {
		t.Fatal("unconfigured telegram test should fail")
	}

	// Token stored but nobody paired → still a failure, with guidance.
	if _, err := svc.SaveIntegration(context.Background(), &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Telegram{
			Telegram: &pb.TelegramIntegration{Enabled: true, BotToken: tgTestToken},
		},
	}); err != nil {
		t.Fatalf("SaveIntegration: %v", err)
	}

	resp, err = svc.TestIntegration(context.Background(), &pb.TestIntegrationRequest{Kind: pb.IntegrationKind_TELEGRAM})
	if err != nil {
		t.Fatalf("TestIntegration: %v", err)
	}
	if resp.GetOk() {
		t.Fatal("test should fail with no paired chats")
	}
	if !strings.Contains(resp.GetMessage(), "no unmuted paired chats") {
		t.Fatalf("unexpected message: %q", resp.GetMessage())
	}
}
