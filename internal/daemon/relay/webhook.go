package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// SignatureHeader is the HTTP header carrying the HMAC-SHA256 signature
// the webhook adapter sets on every POST. Receivers verify against it
// using `relay.VerifyHMAC` before trusting the payload body.
const SignatureHeader = "X-Watchfire-Signature"

// EventHeader carries the canonical NotificationKind string ("TASK_FAILED",
// "RUN_COMPLETE", "WEEKLY_DIGEST") so receivers can branch on event type
// without parsing the body — useful for cheap routing in proxies.
const EventHeader = "X-Watchfire-Event"

// WebhookAdapter is the v7.0 Relay generic outbound webhook. One adapter
// binds to one endpoint (one URL = one receiver). The dispatcher creates
// one WebhookAdapter per `models.WebhookEndpoint` and rebuilds the slice
// when integrations.yaml changes.
//
// Authentication is HMAC-SHA256: the adapter signs the canonical JSON
// body with the endpoint's secret (resolved through the OS keyring at
// dispatcher build time) and surfaces the result on `X-Watchfire-
// Signature`. A missing keyring entry surfaces as an explicit Send error
// instead of an unsigned send so receivers configured to require a
// signature don't silently start dropping events.
type WebhookAdapter struct {
	endpoint   models.WebhookEndpoint
	secret     []byte
	httpClient *http.Client
	logger     *log.Logger
}

// NewWebhookAdapter constructs a webhook adapter for the given endpoint.
// `secret` is the resolved keyring value — pass the byte slice directly
// rather than the keyring key so the dispatcher controls the lookup
// timing (and so tests can inject a known secret without round-tripping
// through the keyring fake).
func NewWebhookAdapter(endpoint models.WebhookEndpoint, secret []byte, client *http.Client, logger *log.Logger) *WebhookAdapter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if logger == nil {
		logger = log.Default()
	}
	return &WebhookAdapter{
		endpoint:   endpoint,
		secret:     secret,
		httpClient: client,
		logger:     logger,
	}
}

// ID returns the stable id from the IntegrationsConfig entry.
func (w *WebhookAdapter) ID() string { return w.endpoint.ID }

// Kind reports the adapter kind for the dispatcher's per-kind routing.
func (w *WebhookAdapter) Kind() string { return "webhook" }

// Supports gates the adapter on the per-endpoint event bitmask. Returns
// false for any kind the user has unchecked; the dispatcher skips Send
// without ever opening a connection.
func (w *WebhookAdapter) Supports(kind notify.Kind) bool {
	switch kind {
	case notify.KindTaskFailed:
		return w.endpoint.EnabledEvents.TaskFailed
	case notify.KindRunComplete:
		return w.endpoint.EnabledEvents.RunComplete
	case notify.KindWeeklyDigest:
		return w.endpoint.EnabledEvents.WeeklyDigest
	}
	return false
}

// IsProjectMuted reports whether the source project sits inside the
// adapter's per-project mute list. The dispatcher checks this before
// calling Send so muted projects never reach the network.
func (w *WebhookAdapter) IsProjectMuted(projectID string) bool {
	return IsProjectMuted(w.endpoint.ProjectMuteIDs, projectID)
}

// Send marshals the payload to JSON, signs it with the adapter's secret,
// and POSTs to the configured URL with the canonical headers. 2xx is
// success; anything else returns an error so the dispatcher's retry +
// circuit-breaker can act on it.
func (w *WebhookAdapter) Send(ctx context.Context, p Payload) error {
	if w.endpoint.URL == "" {
		return fmt.Errorf("webhook adapter %q: URL not set", w.endpoint.ID)
	}
	if w.endpoint.SecretRef != "" && len(w.secret) == 0 {
		return fmt.Errorf("webhook adapter %q: secret not resolved (keyring miss?)", w.endpoint.ID)
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("webhook adapter %q: marshal payload: %w", w.endpoint.ID, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook adapter %q: build request: %w", w.endpoint.ID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "watchfire/"+buildinfo.Version)
	req.Header.Set(EventHeader, p.Kind)
	if len(w.secret) > 0 {
		req.Header.Set(SignatureHeader, SignHMAC(w.secret, body))
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook adapter %q: POST: %w", w.endpoint.ID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook adapter %q: HTTP %d", w.endpoint.ID, resp.StatusCode)
	}
	return nil
}

// Compile-time assertion that WebhookAdapter satisfies the Adapter
// interface — the dispatcher iterates `[]Adapter` so this catches
// accidental signature drift at build time.
var _ Adapter = (*WebhookAdapter)(nil)
