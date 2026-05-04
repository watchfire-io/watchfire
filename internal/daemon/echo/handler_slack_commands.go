package echo

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// slackCommandPrefix is the leading slash every Slack slash command
// arrives with. The form-encoded `command` field already includes it
// ("/watchfire"), so the handler passes it straight to `commands.Route`
// without re-prefixing.
const slackCommandPrefix = "/"

// SlackCommandsHandlerConfig wires the per-request state the Slack
// slash-command handler needs. Mirrors `DiscordHandlerConfig` and
// `SlackHandlerConfig` (interactivity) — the same shape so the daemon's
// wiring code can plug all three handlers identically:
//
//   - ResolveSigningSecret — keyring fetch for the v0 HMAC signing
//     secret. Run per request so a Settings-side rotation takes effect
//     without a daemon restart.
//   - Idempotency          — LRU+TTL cache keyed on the per-command
//     `trigger_id` (Slack's stable per-invocation identifier; falls back
//     to `client_msg_id` when the upstream surface is an Events API
//     `message` rather than a slash command).
//   - CommandContextFor    — factory that scopes FindProjects / Retry /
//     Cancel / LookupTask to the calling Slack workspace. Keyed on
//     team_id + user_id so a daemon serving multiple workspaces never
//     crosses project boundaries.
//   - RefundOnReplay       — per-IP rate-limit refund hook the parent
//     Server wires when the limiter is enabled. nil = no-op.
//   - RecordDelivery       — per-provider freshness hook. Called once
//     per verified delivery whose handler ran to completion.
//   - Logger               — instrumentation. Defaults to log.Default().
type SlackCommandsHandlerConfig struct {
	ResolveSigningSecret func() ([]byte, error)
	Idempotency          *Cache
	CommandContextFor    func(teamID, userID string) CommandContext
	RefundOnReplay       func(r *http.Request)
	RecordDelivery       func()
	Logger               *log.Logger
}

// NewSlackCommandsHandler returns the http.Handler that lives at
// `POST /echo/slack/commands`. Slack POSTs form-encoded slash-command
// invocations (`/watchfire status`, `/watchfire retry 42`, …) to this
// endpoint; the handler verifies the v0 HMAC signature, parses the
// form fields, dispatches into the shared `Route`, and renders the
// resulting `CommandResponse` as Slack's response JSON.
func NewSlackCommandsHandler(cfg SlackCommandsHandlerConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Idempotency == nil {
		cfg.Idempotency = NewCache(0, 0)
	}
	return &slackCommandsHandler{cfg: cfg}
}

type slackCommandsHandler struct{ cfg SlackCommandsHandlerConfig }

func (h *slackCommandsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if IsTooLarge(err) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			h.cfg.Logger.Printf("WARN: echo: slack command payload too large (>%d bytes)", MaxBodyBytes)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")
	if timestamp == "" || signature == "" {
		http.Error(w, "missing signature headers", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: slack command missing signature headers")
		return
	}

	secret, err := h.cfg.ResolveSigningSecret()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: slack signing secret not configured: %v", err)
		return
	}

	if vErr := VerifySlack(secret, timestamp, body, signature); vErr != nil {
		status := http.StatusUnauthorized
		if errors.Is(vErr, ErrMalformedHeader) {
			status = http.StatusBadRequest
		}
		http.Error(w, vErr.Error(), status)
		h.cfg.Logger.Printf("WARN: echo: slack command signature rejected: %v", vErr)
		return
	}

	form, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "malformed body", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: slack command body not form-encoded: %v", err)
		return
	}

	command := strings.TrimSpace(form.Get("command"))
	if command == "" {
		http.Error(w, "missing command field", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: slack command missing command field")
		return
	}
	if !strings.HasPrefix(command, slackCommandPrefix) {
		http.Error(w, "command must start with /", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: slack command field %q missing leading slash", command)
		return
	}

	teamID := form.Get("team_id")
	userID := form.Get("user_id")
	triggerID := form.Get("trigger_id")
	idempotencyKey := triggerID
	if idempotencyKey == "" {
		// Slash commands always carry a trigger_id; the fallback covers
		// Events API surfaces (e.g. message commands) that key on the
		// per-message client_msg_id instead.
		idempotencyKey = form.Get("client_msg_id")
	}

	if idempotencyKey != "" && h.cfg.Idempotency.Seen(idempotencyKey) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		h.cfg.Logger.Printf("INFO: echo: slack command %s replayed, returning empty ack", idempotencyKey)
		writeSlackCommandsAck(w)
		return
	}

	subcmd, rest := splitSlackCommandText(form.Get("text"))

	cc := h.cfg.CommandContextFor(teamID, userID)
	resp := Route(r.Context(), command, subcmd, rest, cc)

	body = RenderSlack(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(body); writeErr != nil {
		h.cfg.Logger.Printf("WARN: echo: slack command write response: %v", writeErr)
		return
	}
	if h.cfg.RecordDelivery != nil {
		h.cfg.RecordDelivery()
	}
	h.cfg.Logger.Printf("INFO: echo: slack command %q subcmd=%q team=%s user=%s", command, subcmd, teamID, userID)
}

// splitSlackCommandText divides the `text` field into the first word
// (the subcommand: "status" / "retry" / "cancel") and the remainder
// (the argument list). Whitespace-only `text` returns ("", ""), which
// `Route` interprets as an implicit `status`.
func splitSlackCommandText(text string) (subcmd, rest string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", ""
	}
	idx := strings.IndexAny(t, " \t")
	if idx < 0 {
		return t, ""
	}
	return t[:idx], strings.TrimSpace(t[idx+1:])
}

// writeSlackCommandsAck is the empty 200 returned for replayed slash-
// command deliveries. Slack treats a 200 with no body as a silent
// acknowledgement — the original synchronous response is already in
// the user's channel, so the replay should not double-render it.
func writeSlackCommandsAck(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
}
