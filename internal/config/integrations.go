package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/watchfire-io/watchfire/internal/models"
)

// IntegrationsFileName is the filename of the global integrations config
// inside ~/.watchfire/.
const IntegrationsFileName = "integrations.yaml"

// GlobalIntegrationsFile returns the path to ~/.watchfire/integrations.yaml.
func GlobalIntegrationsFile() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, IntegrationsFileName), nil
}

// secretStore abstracts the OS keyring so tests can inject a fake without
// pulling in a real keyring dep on CI / headless Linux. The real binary
// will swap in a `go-keyring` backed implementation as part of task 0062;
// this stub uses an in-memory map plus a file-backed fallback so the
// shapes round-trip end-to-end today.
type secretStore interface {
	Get(key string) (string, bool)
	Set(key, value string) error
	Delete(key string) error
}

// fileSecretStore persists secrets to ~/.watchfire/.secrets/<key>. It is
// the fallback path when no keyring is available; 0o600 permissions.
// Secrets are written one-file-per-key so the YAML can be read without
// pulling them in.
type fileSecretStore struct {
	mu  sync.Mutex
	dir string
}

func newFileSecretStore() (*fileSecretStore, error) {
	dir, err := GlobalDir()
	if err != nil {
		return nil, err
	}
	secretsDir := filepath.Join(dir, ".secrets")
	if mkErr := os.MkdirAll(secretsDir, 0o700); mkErr != nil {
		return nil, mkErr
	}
	return &fileSecretStore{dir: secretsDir}, nil
}

func (s *fileSecretStore) path(key string) string {
	return filepath.Join(s.dir, key)
}

func (s *fileSecretStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(key))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (s *fileSecretStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path(key), []byte(value), 0o600)
}

func (s *fileSecretStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// activeSecretStore is the package-level store. Tests call
// SetSecretStoreForTest to swap in a fake; the real loader resets it back
// to the file-backed default when teardown completes.
var (
	storeMu            sync.Mutex
	activeSecretStore  secretStore
	activeStoreInitErr error
	activeStoreInit    sync.Once
)

func resolveSecretStore() (secretStore, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if activeSecretStore != nil {
		return activeSecretStore, nil
	}
	activeStoreInit.Do(func() {
		// Prefer the OS keyring (go-keyring); on platforms without a
		// reachable keyring (Linux without dbus, headless CI) fall
		// through to the on-disk file store with a one-shot WARN.
		s, err := chooseSecretStore()
		if err != nil {
			activeStoreInitErr = err
			return
		}
		activeSecretStore = s
	})
	if activeStoreInitErr != nil {
		return nil, activeStoreInitErr
	}
	return activeSecretStore, nil
}

// SetSecretStoreForTest injects a fake secret store; pass nil to reset
// to the on-disk default. Test-only — not exported by intent (lower-case
// would shadow the type), kept exported so cross-package tests can use it.
func SetSecretStoreForTest(s secretStore) {
	storeMu.Lock()
	defer storeMu.Unlock()
	activeSecretStore = s
}

// SecretKeyForIntegration returns the canonical keyring key for a given
// integration ID + field. Slack / Discord store the URL itself; webhook
// endpoints store the HMAC secret.
func SecretKeyForIntegration(integrationID, field string) string {
	return fmt.Sprintf("watchfire.integration.%s.%s", integrationID, field)
}

// LoadIntegrations reads integrations.yaml + resolves secrets via the
// active secret store. A missing YAML file returns an empty config (no
// error) so a fresh install starts clean.
func LoadIntegrations() (*models.IntegrationsConfig, error) {
	path, err := GlobalIntegrationsFile()
	if err != nil {
		return nil, err
	}
	cfg, err := LoadYAMLOrDefault(path, models.NewIntegrationsConfig)
	if err != nil {
		return nil, err
	}

	store, storeErr := resolveSecretStore()
	if storeErr != nil {
		// Graceful degradation: return the YAML view without resolved
		// secrets. Adapters that need them log a WARN and refuse to
		// send (handled in the dispatcher in task 0062).
		return cfg, nil
	}

	for i := range cfg.Slack {
		if cfg.Slack[i].URLRef == "" {
			continue
		}
		if v, ok := store.Get(cfg.Slack[i].URLRef); ok {
			cfg.Slack[i].URL = v
		}
	}
	for i := range cfg.Discord {
		if cfg.Discord[i].URLRef == "" {
			continue
		}
		if v, ok := store.Get(cfg.Discord[i].URLRef); ok {
			cfg.Discord[i].URL = v
		}
	}
	return cfg, nil
}

// SaveIntegrations persists integrations.yaml + writes secrets through
// to the active secret store. Empty secrets are not pushed (preserves
// the existing keyring entry on partial updates).
func SaveIntegrations(cfg *models.IntegrationsConfig) error {
	if cfg == nil {
		return fmt.Errorf("cannot save nil integrations config")
	}
	path, err := GlobalIntegrationsFile()
	if err != nil {
		return err
	}

	store, storeErr := resolveSecretStore()
	if storeErr == nil {
		for i := range cfg.Slack {
			ep := &cfg.Slack[i]
			if ep.URL == "" {
				continue
			}
			if ep.URLRef == "" {
				ep.URLRef = SecretKeyForIntegration(ep.ID, "url")
			}
			if setErr := store.Set(ep.URLRef, ep.URL); setErr != nil {
				return fmt.Errorf("failed to store Slack URL: %w", setErr)
			}
		}
		for i := range cfg.Discord {
			ep := &cfg.Discord[i]
			if ep.URL == "" {
				continue
			}
			if ep.URLRef == "" {
				ep.URLRef = SecretKeyForIntegration(ep.ID, "url")
			}
			if setErr := store.Set(ep.URLRef, ep.URL); setErr != nil {
				return fmt.Errorf("failed to store Discord URL: %w", setErr)
			}
		}
	}

	// Detach the runtime URL field before serialising — the YAML must
	// not carry the secret.
	scrubbed := *cfg
	scrubbed.Slack = make([]models.SlackEndpoint, len(cfg.Slack))
	for i, ep := range cfg.Slack {
		ep.URL = ""
		scrubbed.Slack[i] = ep
	}
	scrubbed.Discord = make([]models.DiscordEndpoint, len(cfg.Discord))
	for i, ep := range cfg.Discord {
		ep.URL = ""
		scrubbed.Discord[i] = ep
	}
	return SaveYAML(path, &scrubbed)
}

// DeleteIntegrationSecret removes a secret entry by key. Used by the
// service handler when a Slack / Discord / Webhook integration is
// deleted so we don't leave dangling keyring entries behind.
func DeleteIntegrationSecret(key string) error {
	if key == "" {
		return nil
	}
	store, err := resolveSecretStore()
	if err != nil {
		return nil
	}
	return store.Delete(key)
}

// LookupIntegrationSecret resolves a secret value through the active
// store. Used by the test handler in the service layer to fetch the
// HMAC secret without round-tripping through `LoadIntegrations`.
func LookupIntegrationSecret(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	store, err := resolveSecretStore()
	if err != nil {
		return "", false
	}
	return store.Get(key)
}

// PutIntegrationSecret writes a secret value through the active store.
// Used by the IntegrationsService server to store webhook HMAC secrets
// directly (Slack / Discord URL secrets flow through SaveIntegrations
// since they live on the endpoint struct).
func PutIntegrationSecret(key, value string) error {
	if key == "" {
		return fmt.Errorf("PutIntegrationSecret: empty key")
	}
	store, err := resolveSecretStore()
	if err != nil {
		return err
	}
	return store.Set(key, value)
}
