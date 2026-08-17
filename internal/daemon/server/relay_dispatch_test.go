package server

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/relay"
	"github.com/watchfire-io/watchfire/internal/models"
)

func telegramIntegrations(mutate func(*models.TelegramConfig)) *models.IntegrationsConfig {
	tg := &models.TelegramConfig{
		Enabled:       true,
		BotToken:      tgTestToken,
		EnabledEvents: models.EventBitmask{TaskFailed: true},
		PairedChats: []models.TelegramPairedChat{{
			ChatID:   111,
			UserID:   111,
			Username: "nuno",
			PairedAt: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
		}},
	}
	if mutate != nil {
		mutate(tg)
	}
	return &models.IntegrationsConfig{Telegram: tg}
}

func hasTelegramAdapter(adapters []relay.Adapter) bool {
	for _, a := range adapters {
		if a.Kind() == "telegram" {
			return true
		}
	}
	return false
}

// TestBuildRelayAdaptersTelegramGating asserts the Telegram adapter is
// registered only when the bridge is deliverable: enabled + token
// resolving + at least one paired chat.
func TestBuildRelayAdaptersTelegramGating(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*models.TelegramConfig)
		want   bool
	}{
		{name: "enabled with token and chat", mutate: nil, want: true},
		{name: "disabled", mutate: func(tg *models.TelegramConfig) { tg.Enabled = false }, want: false},
		{name: "no paired chats", mutate: func(tg *models.TelegramConfig) { tg.PairedChats = nil }, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tgTestSetup(t)
			if err := config.SaveIntegrations(telegramIntegrations(tc.mutate)); err != nil {
				t.Fatalf("SaveIntegrations: %v", err)
			}
			adapters, err := buildRelayAdapters()
			if err != nil {
				t.Fatalf("buildRelayAdapters: %v", err)
			}
			if got := hasTelegramAdapter(adapters); got != tc.want {
				t.Fatalf("telegram adapter registered = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBuildRelayAdaptersTelegramTokenMiss asserts an unresolvable token
// (keyring miss) keeps the adapter off the roster — a broken bridge
// should not soak up dispatcher retries.
func TestBuildRelayAdaptersTelegramTokenMiss(t *testing.T) {
	tgTestSetup(t)
	if err := config.SaveIntegrations(telegramIntegrations(nil)); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}
	// Swap in an empty store: the YAML still carries bot_token_ref but
	// the secret can no longer be resolved.
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: newMemSecretStore()})
	adapters, err := buildRelayAdapters()
	if err != nil {
		t.Fatalf("buildRelayAdapters: %v", err)
	}
	if hasTelegramAdapter(adapters) {
		t.Fatal("telegram adapter must not register without a resolvable token")
	}
}

// TestRelayDispatcherHotReloadPicksUpTelegram exercises the same path
// the daemon runs on EventIntegrationsChanged: the dispatcher's Reload
// re-invokes buildRelayAdapters, so enabling Telegram (or pairing the
// first chat) activates the adapter with no daemon restart.
func TestRelayDispatcherHotReloadPicksUpTelegram(t *testing.T) {
	tgTestSetup(t)
	if err := config.SaveIntegrations(&models.IntegrationsConfig{}); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}

	d := relay.NewDispatcher(nil, nil, buildRelayAdapters)
	if hasTelegramAdapter(d.Adapters()) {
		t.Fatal("telegram adapter present before configuration")
	}

	if err := config.SaveIntegrations(telegramIntegrations(nil)); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}
	d.Reload()
	if !hasTelegramAdapter(d.Adapters()) {
		t.Fatal("telegram adapter missing after Reload")
	}
}
