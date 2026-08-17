// Package telegram is the v10.0 Torch Telegram bridge: a long-polling
// goroutine that connects the daemon to a @BotFather bot without any
// inbound listener (the daemon dials out — same local-only posture as
// the v9 MCP stdio decision).
//
// Pairing is the bridge's security boundary. Telegram bots are globally
// reachable — anyone can DM one — so the paired-chats list in
// TelegramConfig is the allowlist, and this file implements the only
// way onto it: a one-time code minted by the daemon, redeemed from the
// chat via /start or /pair.
package telegram

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// codeAlphabet is the unambiguous code alphabet — no 0/O, no 1/I/l, so
// a code read off a phone screen can be typed without transcription
// errors.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// CodeLength is the pairing-code length in characters.
const CodeLength = 8

// DefaultTTL is how long a pairing code stays redeemable.
const DefaultTTL = 10 * time.Minute

// PairingState is the lifecycle of the single active pairing attempt.
type PairingState int

const (
	// StateNone — no code has ever been issued (or the daemon restarted).
	StateNone PairingState = iota
	// StatePending — a code is live and unredeemed.
	StatePending
	// StatePaired — the most recent code was redeemed.
	StatePaired
	// StateExpired — the code's TTL lapsed unredeemed.
	StateExpired
)

// PairingStatus is a point-in-time snapshot of the pairing lifecycle.
type PairingStatus struct {
	State     PairingState
	ExpiresAt time.Time                  // valid while State == StatePending
	Chat      *models.TelegramPairedChat // set when State == StatePaired
}

// Pairing manages the single active one-time pairing code. At most one
// code is live at a time — Begin invalidates any prior code — and a
// code is single-use: the first successful Consume clears it.
//
// The Pairing is owned by the daemon Server (not the Bridge) so a
// bridge restart on config save doesn't strand an in-flight pairing.
type Pairing struct {
	mu        sync.Mutex
	code      string
	expiresAt time.Time
	paired    *models.TelegramPairedChat

	// now is injectable for TTL tests.
	now func() time.Time
}

// NewPairing returns an empty pairing manager (state none).
func NewPairing() *Pairing {
	return &Pairing{now: time.Now}
}

// Begin mints a fresh one-time code with the given TTL (0 → DefaultTTL),
// invalidating any previously active code and clearing any prior paired
// result. Returns the code and its expiry.
func (p *Pairing) Begin(ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	code, err := generateCode()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate pairing code: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code = code
	p.expiresAt = p.now().Add(ttl)
	p.paired = nil
	return code, p.expiresAt, nil
}

// Consume redeems candidate against the active code. Returns true and
// clears the code (single use) iff a live, unexpired code matches.
// Comparison is constant-time; candidate case is normalised so a code
// typed in lowercase still redeems.
func (p *Pairing) Consume(candidate string) bool {
	candidate = strings.ToUpper(strings.TrimSpace(candidate))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.code == "" || candidate == "" {
		return false
	}
	if p.now().After(p.expiresAt) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(candidate), []byte(p.code)) != 1 {
		return false
	}
	p.code = ""
	return true
}

// Complete records the chat that redeemed the code, flipping the state
// to paired. Called by the bridge after the chat has been persisted.
func (p *Pairing) Complete(chat models.TelegramPairedChat) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := chat
	p.paired = &c
}

// Status returns the current lifecycle snapshot.
func (p *Pairing) Status() PairingStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.paired != nil:
		c := *p.paired
		return PairingStatus{State: StatePaired, Chat: &c}
	case p.code == "" && p.expiresAt.IsZero():
		return PairingStatus{State: StateNone}
	case p.code == "":
		// Code consumed but Complete never arrived (persist failed) —
		// surface as none so the user simply begins again.
		return PairingStatus{State: StateNone}
	case p.now().After(p.expiresAt):
		return PairingStatus{State: StateExpired}
	default:
		return PairingStatus{State: StatePending, ExpiresAt: p.expiresAt}
	}
}

// generateCode draws CodeLength characters from codeAlphabet using
// crypto/rand with rejection sampling (no modulo bias).
func generateCode() (string, error) {
	out := make([]byte, 0, CodeLength)
	buf := make([]byte, 1)
	// Largest multiple of len(codeAlphabet) that fits in a byte —
	// values at or above it are rejected to keep the draw uniform.
	limit := byte(256 - 256%len(codeAlphabet))
	for len(out) < CodeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if buf[0] >= limit {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(out), nil
}
