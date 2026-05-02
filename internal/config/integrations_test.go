package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/watchfire-io/watchfire/internal/models"
)

// fakeKeyring is an in-memory secretStore for round-trip tests; it
// stands in for the real OS keyring without touching the host's
// Keychain / Credential Manager / dbus secret-service.
type fakeKeyring struct {
	mu    sync.Mutex
	items map[string]string
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{items: make(map[string]string)} }

func (f *fakeKeyring) Get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.items[key]
	return v, ok
}
func (f *fakeKeyring) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[key] = value
	return nil
}
func (f *fakeKeyring) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, key)
	return nil
}

// withFakeKeyring redirects the package-level secret store to a fresh
// in-memory keyring for the duration of t. Cleanup restores the
// previous store so tests don't bleed into siblings.
func withFakeKeyring(t *testing.T) *fakeKeyring {
	t.Helper()
	fk := newFakeKeyring()
	SetSecretStoreForTest(fk)
	t.Cleanup(func() { SetSecretStoreForTest(nil) })
	return fk
}

// withTempHome redirects $HOME for the test so YAML lands inside a
// scratch dir.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

func TestIntegrationsRoundTripWebhookSecretViaKeyring(t *testing.T) {
	tmp := withTempHome(t)
	fk := withFakeKeyring(t)

	cfg := &models.IntegrationsConfig{
		Webhooks: []models.WebhookEndpoint{{
			ID:        "wh-1",
			Label:     "ops",
			URL:       "https://hooks.example.com/in",
			SecretRef: SecretKeyForIntegration("wh-1", "secret"),
			EnabledEvents: models.EventBitmask{
				TaskFailed:   true,
				RunComplete:  true,
				WeeklyDigest: false,
			},
			ProjectMuteIDs: []string{"proj-X"},
		}},
	}
	// Push a secret directly so SaveIntegrations doesn't need to push it.
	if err := PutIntegrationSecret(cfg.Webhooks[0].SecretRef, "topsecret"); err != nil {
		t.Fatalf("PutIntegrationSecret: %v", err)
	}
	if err := SaveIntegrations(cfg); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}

	// Confirm the YAML on disk does NOT contain the secret.
	yamlPath := filepath.Join(tmp, ".watchfire", "integrations.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	if got := string(raw); contains(got, "topsecret") {
		t.Fatalf("secret leaked into YAML:\n%s", got)
	}

	got, err := LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if len(got.Webhooks) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(got.Webhooks))
	}
	w := got.Webhooks[0]
	if w.ID != "wh-1" || w.URL != "https://hooks.example.com/in" || w.Label != "ops" {
		t.Fatalf("webhook round-trip mismatch: %+v", w)
	}
	if !w.EnabledEvents.TaskFailed || !w.EnabledEvents.RunComplete || w.EnabledEvents.WeeklyDigest {
		t.Fatalf("event bitmask mismatch: %+v", w.EnabledEvents)
	}
	if len(w.ProjectMuteIDs) != 1 || w.ProjectMuteIDs[0] != "proj-X" {
		t.Fatalf("mute round-trip mismatch: %v", w.ProjectMuteIDs)
	}
	if v, ok := fk.Get(w.SecretRef); !ok || v != "topsecret" {
		t.Fatalf("keyring secret round-trip failed: ok=%v v=%q", ok, v)
	}
}

func TestIntegrationsRoundTripSlackURLViaKeyring(t *testing.T) {
	withTempHome(t)
	fk := withFakeKeyring(t)

	cfg := &models.IntegrationsConfig{
		Slack: []models.SlackEndpoint{{
			ID:    "slack-1",
			Label: "engineering",
			URL:   "https://hooks.slack.com/services/T0/B0/abcdef",
			EnabledEvents: models.EventBitmask{
				TaskFailed: true,
			},
		}},
	}
	if err := SaveIntegrations(cfg); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}

	loaded, err := LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if len(loaded.Slack) != 1 {
		t.Fatalf("expected 1 slack endpoint, got %d", len(loaded.Slack))
	}
	s := loaded.Slack[0]
	if s.URL != "https://hooks.slack.com/services/T0/B0/abcdef" {
		t.Fatalf("slack URL not resolved through keyring: %q", s.URL)
	}
	if v, ok := fk.Get(s.URLRef); !ok || v != s.URL {
		t.Fatalf("slack secret not in fake keyring: ok=%v v=%q", ok, v)
	}
}

func TestIntegrationsLoadMissingFileReturnsEmpty(t *testing.T) {
	withTempHome(t)
	withFakeKeyring(t)

	got, err := LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations on missing file: %v", err)
	}
	if got == nil {
		t.Fatal("expected empty config, got nil")
	}
	if len(got.Webhooks) != 0 || len(got.Slack) != 0 || len(got.Discord) != 0 {
		t.Fatalf("expected empty integrations, got %+v", got)
	}
}

func TestIntegrationsKeyringMissDoesNotFailLoad(t *testing.T) {
	withTempHome(t)

	// Stash a config WITH a secret_ref but reset the secret store to
	// nil so LookupIntegrationSecret returns false. The load should
	// still succeed; the SecretRef stays present so the dispatcher
	// can detect the miss and refuse to send.
	fk := newFakeKeyring()
	SetSecretStoreForTest(fk)
	cfg := &models.IntegrationsConfig{
		Webhooks: []models.WebhookEndpoint{{
			ID:        "wh-1",
			URL:       "https://example.com",
			SecretRef: "watchfire.integration.wh-1.secret",
		}},
	}
	if err := SaveIntegrations(cfg); err != nil {
		t.Fatalf("SaveIntegrations: %v", err)
	}
	// Wipe the fake keyring so the secret is "lost".
	fk.items = map[string]string{}

	got, err := LoadIntegrations()
	if err != nil {
		t.Fatalf("LoadIntegrations: %v", err)
	}
	if len(got.Webhooks) != 1 || got.Webhooks[0].SecretRef == "" {
		t.Fatalf("expected webhook entry to retain SecretRef despite keyring miss: %+v", got)
	}
	SetSecretStoreForTest(nil)
}

func TestDeleteIntegrationSecretClearsFakeKeyring(t *testing.T) {
	withTempHome(t)
	fk := withFakeKeyring(t)

	key := SecretKeyForIntegration("wh-1", "secret")
	if err := PutIntegrationSecret(key, "v"); err != nil {
		t.Fatalf("PutIntegrationSecret: %v", err)
	}
	if _, ok := fk.Get(key); !ok {
		t.Fatal("secret missing after Put")
	}
	if err := DeleteIntegrationSecret(key); err != nil {
		t.Fatalf("DeleteIntegrationSecret: %v", err)
	}
	if _, ok := fk.Get(key); ok {
		t.Fatal("expected secret removed after Delete")
	}
}

func TestSecretKeyForIntegrationStable(t *testing.T) {
	a := SecretKeyForIntegration("wh-1", "secret")
	b := SecretKeyForIntegration("wh-1", "secret")
	if a != b {
		t.Fatalf("non-deterministic key: %q vs %q", a, b)
	}
	if a == "" {
		t.Fatal("empty key")
	}
	if SecretKeyForIntegration("wh-1", "url") == a {
		t.Fatal("expected different key for different field")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
