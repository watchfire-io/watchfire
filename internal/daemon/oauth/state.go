// Package oauth implements the v8.x OAuth bot-token install flows for
// Slack and Discord.
//
// v8.0 Echo shipped with a "paste a signing secret" / "paste a public
// key" model that lets the daemon verify inbound deliveries but does
// not let it talk back (Slack chat.postMessage / Discord channel POST
// both require a bearer-style bot token). v8.x adds OAuth so the user
// can install Watchfire's app to a workspace / guild via the standard
// browser-driven consent flow and have the daemon receive a bot token
// it can use to post DMs, reply to slash commands with rich Block Kit
// blocks, and (in a follow-up) auto-register slash commands.
//
// This package is transport-agnostic: the OAuth handlers expose a
// `BeginFlow` / `CompleteFlow` pair that operate on opaque state +
// code strings. The integrations service wires these to a local
// callback HTTP server (see `internal/daemon/oauth/server.go`) and to
// the gRPC API (see `internal/daemon/server/integrations_oauth.go`).
//
// All cross-process state lives in the OS keyring through
// `internal/config`'s secret-store abstraction; nothing in this
// package writes to disk directly.
package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// stateTTL bounds how long a generated CSRF state is valid. 10 minutes
// covers a slow user (browser tab idle, second factor, workspace admin
// review) without keeping a stale entry around long enough to be a
// useful target for replay.
const stateTTL = 10 * time.Minute

// state is one in-flight OAuth request. Keyed by the random `value`
// the caller embeds in the authorization URL; matched by the same
// value coming back on the callback. Provider distinguishes Slack vs
// Discord so a stray callback to the wrong provider gets rejected.
type state struct {
	value     string
	provider  string
	createdAt time.Time
	// metadata the integrations service handed off at BeginFlow time and
	// wants back at CompleteFlow time. Holds redirect_uri + client_id +
	// client_secret so the token-exchange call has everything it needs
	// without re-reading config.
	clientID     string
	clientSecret string
	redirectURI  string
	// optional channel the user picked when starting the flow, threaded
	// through to the post-completion "hello" message.
	defaultChannel string
}

// StateStore is a small in-memory CSRF-state cache for in-flight OAuth
// flows. Entries auto-expire after `stateTTL`; callers don't need to
// prune. Concurrent-safe — the integrations gRPC handler may call
// `Begin` from one goroutine and `Consume` from a different one.
type StateStore struct {
	mu      sync.Mutex
	entries map[string]*state
	now     func() time.Time
}

// NewStateStore returns an empty store. Tests inject `now` via
// `WithClock`; production paths use the package-level default.
func NewStateStore() *StateStore {
	return &StateStore{
		entries: make(map[string]*state),
		now:     time.Now,
	}
}

// WithClock overrides the time source. Test-only.
func (s *StateStore) WithClock(now func() time.Time) *StateStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
	return s
}

// Begin records a fresh in-flight flow and returns the CSRF state
// value to embed in the authorization URL.
func (s *StateStore) Begin(provider, clientID, clientSecret, redirectURI, defaultChannel string) (string, error) {
	val, err := randomToken(24)
	if err != nil {
		return "", fmt.Errorf("oauth: state generate: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.entries[val] = &state{
		value:          val,
		provider:       provider,
		createdAt:      s.now(),
		clientID:       clientID,
		clientSecret:   clientSecret,
		redirectURI:    redirectURI,
		defaultChannel: defaultChannel,
	}
	return val, nil
}

// Consume validates a state value and returns its associated flow
// metadata. The entry is removed on success so a replay returns
// `ErrUnknownState`.
func (s *StateStore) Consume(provider, val string) (clientID, clientSecret, redirectURI, defaultChannel string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	entry, ok := s.entries[val]
	if !ok {
		return "", "", "", "", ErrUnknownState
	}
	if entry.provider != provider {
		// Don't tear the entry down — a different provider's callback
		// arriving with our state value is a misroute, not a replay.
		return "", "", "", "", ErrProviderMismatch
	}
	delete(s.entries, val)
	return entry.clientID, entry.clientSecret, entry.redirectURI, entry.defaultChannel, nil
}

// Pending returns whether at least one entry exists for the given
// provider. Used by the gRPC status endpoint to surface "OAuth in
// progress" without exposing the state values themselves.
func (s *StateStore) Pending(provider string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	for _, e := range s.entries {
		if e.provider == provider {
			return true
		}
	}
	return false
}

// gcLocked evicts expired entries. Called from every public method
// under the mutex so the cache never grows unbounded even when no one
// completes a flow.
func (s *StateStore) gcLocked() {
	cutoff := s.now().Add(-stateTTL)
	for k, e := range s.entries {
		if e.createdAt.Before(cutoff) {
			delete(s.entries, k)
		}
	}
}

// randomToken returns a URL-safe base64-encoded random token of length
// >= n. The entropy source is `crypto/rand` — the OAuth state guards
// the redirect path against CSRF, so we want a token an attacker can't
// guess inside the 10-minute TTL.
func randomToken(n int) (string, error) {
	if n <= 0 {
		n = 24
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
