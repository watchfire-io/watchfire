package config

import (
	"errors"
	"log"
	"sync"

	"github.com/zalando/go-keyring"
)

// keyringServiceName is the service id used for every Watchfire entry
// in the OS keyring. Picking one stable name lets users find / clear
// all secrets at once via Keychain Access (macOS) / secret-tool
// (Linux) / Credential Manager (Windows).
const keyringServiceName = "watchfire"

// keyringStore is a `secretStore` implementation backed by the
// `github.com/zalando/go-keyring` library. The constructor probes the
// keyring with a no-op set/get/delete cycle so a missing dbus daemon
// (Linux without a desktop session) or a locked Keychain (macOS over
// SSH) surfaces at construction time, letting `resolveSecretStore`
// fall back to the file store cleanly.
type keyringStore struct{}

// newKeyringStore returns a keyring-backed store if and only if the
// platform keyring is reachable. A probe roundtrip detects headless /
// no-dbus environments without leaving a stale entry behind.
func newKeyringStore() (*keyringStore, error) {
	const probeKey = "watchfire.keyring.probe"
	if err := keyring.Set(keyringServiceName, probeKey, "ok"); err != nil {
		return nil, err
	}
	if _, err := keyring.Get(keyringServiceName, probeKey); err != nil {
		return nil, err
	}
	_ = keyring.Delete(keyringServiceName, probeKey)
	return &keyringStore{}, nil
}

func (k *keyringStore) Get(key string) (string, bool) {
	v, err := keyring.Get(keyringServiceName, key)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", false
		}
		return "", false
	}
	return v, true
}

func (k *keyringStore) Set(key, value string) error {
	return keyring.Set(keyringServiceName, key, value)
}

func (k *keyringStore) Delete(key string) error {
	if err := keyring.Delete(keyringServiceName, key); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// keyringInitOnce + keyringWarnOnce coordinate the one-shot keyring
// probe + degradation log. We only want to emit the WARN once per
// daemon lifetime; subsequent secret operations on the file fallback
// stay quiet.
var (
	keyringInitOnce sync.Once
	keyringWarnOnce sync.Once
)

// chooseSecretStore returns a keyring-backed store on platforms where
// the OS keyring is reachable; otherwise it falls back to the file
// store and emits a single WARN. This is called from
// `resolveSecretStore` (`internal/config/integrations.go`) on first
// access; the result is cached for the rest of the process lifetime.
func chooseSecretStore() (secretStore, error) {
	if k, err := newKeyringStore(); err == nil {
		return k, nil
	} else {
		keyringWarnOnce.Do(func() {
			log.Printf("WARN: OS keyring unavailable (%v) — falling back to ~/.watchfire/.secrets/ file store", err)
		})
	}
	return newFileSecretStore()
}
