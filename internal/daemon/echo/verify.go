// Package echo is v8.0 Echo's inbound HTTP listener. It accepts
// signed requests from external services (GitHub PR webhooks, Slack
// slash commands, Discord interactions) and dispatches them into the
// daemon's existing task lifecycle helpers.
//
// The package is split into:
//
//   - server.go        — the HTTP listener + per-provider handler registration
//   - verify.go        — provider-specific signature verification (this file)
//   - idempotency.go   — LRU+TTL replay-protection cache
//   - commands.go      — transport-agnostic slash-command router (status/retry/cancel)
//   - handler_discord.go + discord_render.go — Discord interactions endpoint (v8.0 task 0073)
//
// Each verifier is constant-time on the signature comparison so a probe
// loop cannot recover the secret from timing side-channels. All three
// verifiers reject malformed input upfront (wrong prefix / wrong length
// / undecodable hex) before reaching the expensive HMAC / Ed25519 step.
package echo

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ReplayWindow is the maximum drift between the request timestamp and
// wall-clock time accepted by `VerifySlack` / `VerifyDiscord`. Slack
// recommends 5 minutes (https://api.slack.com/authentication/verifying-requests-from-slack);
// Discord doesn't pin an explicit value but the same window is the
// industry default. Tests inject `clockNow` to bypass it deterministically.
const ReplayWindow = 5 * time.Minute

// clockNow is the wall-clock provider for replay-window checks. Tests
// override it via SetClockForTest; the package-level default is
// time.Now so production code does not need to thread a clock through
// every call site.
var clockNow = time.Now

// SetClockForTest swaps the wall-clock used by replay-window checks.
// Pass nil to reset to time.Now after the test finishes.
func SetClockForTest(fn func() time.Time) {
	if fn == nil {
		clockNow = time.Now
		return
	}
	clockNow = fn
}

// Errors surfaced by the verifiers. Callers translate these to HTTP
// status codes:
//   - ErrInvalidSignature  → 401
//   - ErrTimestampDrift    → 401 (mark as drift in the log line)
//   - ErrMalformedHeader   → 400
//   - ErrEmptySecret       → 503 (server-side misconfig, not a client fault)
var (
	ErrInvalidSignature = errors.New("echo: invalid signature")
	ErrTimestampDrift   = errors.New("echo: timestamp outside replay window")
	ErrMalformedHeader  = errors.New("echo: malformed signature header")
	ErrEmptySecret      = errors.New("echo: signing secret not configured")
)

// gitHubPrefix is the algorithm tag GitHub prepends to its signature
// header (`X-Hub-Signature-256: sha256=<hex>`).
const gitHubPrefix = "sha256="

// VerifyGitHub reports whether candidate is the canonical
// `sha256=<hex>` HMAC-SHA256 GitHub would emit for body keyed on
// secret. Comparison is constant-time on the raw HMAC bytes.
//
// GitHub's webhook delivery model and signature format:
// https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
func VerifyGitHub(secret, body []byte, candidate string) error {
	if len(secret) == 0 {
		return ErrEmptySecret
	}
	if len(candidate) <= len(gitHubPrefix) || candidate[:len(gitHubPrefix)] != gitHubPrefix {
		return ErrMalformedHeader
	}
	want, err := hex.DecodeString(candidate[len(gitHubPrefix):])
	if err != nil {
		return ErrMalformedHeader
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return ErrInvalidSignature
	}
	return nil
}

// slackPrefix is the algorithm tag Slack prepends to its signature
// header (`X-Slack-Signature: v0=<hex>`).
const slackPrefix = "v0="

// VerifySlack reports whether candidate is the canonical
// `v0=<hex>` HMAC-SHA256 Slack would emit for `v0:<timestamp>:<body>`
// keyed on signingSecret. Returns ErrTimestampDrift if the timestamp
// is more than `ReplayWindow` away from now (replay protection).
//
// Slack's signing protocol is documented at
// https://api.slack.com/authentication/verifying-requests-from-slack.
func VerifySlack(signingSecret []byte, timestamp string, body []byte, candidate string) error {
	if len(signingSecret) == 0 {
		return ErrEmptySecret
	}
	if len(candidate) <= len(slackPrefix) || candidate[:len(slackPrefix)] != slackPrefix {
		return ErrMalformedHeader
	}
	want, err := hex.DecodeString(candidate[len(slackPrefix):])
	if err != nil {
		return ErrMalformedHeader
	}

	tsInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrMalformedHeader
	}
	if drift := absDuration(clockNow().Sub(time.Unix(tsInt, 0))); drift > ReplayWindow {
		return ErrTimestampDrift
	}

	mac := hmac.New(sha256.New, signingSecret)
	// Slack's basestring is the exact concatenation `v0:<ts>:<body>`.
	mac.Write([]byte("v0:"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(":"))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyDiscord reports whether sigHex is a valid Ed25519 signature
// over `timestamp || body` under publicKey. publicKey is the raw 32
// bytes (`ed25519.PublicKey`); callers feed in the hex-decoded value
// of `application_public_key` from the Discord developer portal.
//
// The `timestamp || body` concatenation is Discord's canonical signing
// surface (https://discord.com/developers/docs/interactions/receiving-and-responding#security-and-authorization).
// `ed25519.Verify` is itself constant-time on the comparison step, so
// no hmac.Equal wrap is needed.
//
// As with Slack, the timestamp is expected to be a Unix-seconds string
// and is rejected outside the replay window. Discord doesn't strictly
// require this check at their end — but every signed-message protocol
// benefits from it, and rejecting old replays here matches the Slack
// behaviour for code symmetry.
func VerifyDiscord(publicKey ed25519.PublicKey, timestamp, body []byte, sigHex string) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return ErrEmptySecret
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return ErrMalformedHeader
	}
	if len(sig) != ed25519.SignatureSize {
		return ErrMalformedHeader
	}

	if tsInt, err := strconv.ParseInt(string(timestamp), 10, 64); err == nil {
		if drift := absDuration(clockNow().Sub(time.Unix(tsInt, 0))); drift > ReplayWindow {
			return ErrTimestampDrift
		}
	}
	// If the timestamp doesn't parse as Unix-seconds we still allow the
	// signature check to proceed: Discord's signing surface is byte-exact,
	// and the public key + signature pair is the security boundary. A
	// malformed timestamp string will simply fail the signature check
	// since the signer would have used the same string we did.

	msg := make([]byte, 0, len(timestamp)+len(body))
	msg = append(msg, timestamp...)
	msg = append(msg, body...)
	if !ed25519.Verify(publicKey, msg, sig) {
		return ErrInvalidSignature
	}
	return nil
}

// DecodeDiscordPublicKey decodes a hex-encoded Discord application
// public key into the raw 32-byte form `VerifyDiscord` accepts. The
// keyring stores the value as a hex string (the form Discord shows in
// the developer portal); callers run it through this helper once at
// configuration load.
func DecodeDiscordPublicKey(hexKey string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode discord public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("decode discord public key: want %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
