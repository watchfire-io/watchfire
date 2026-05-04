// Package slackbot implements a thin client for Slack's Web API using
// a bot token (xoxb-...). It is the v8.x companion to the OAuth flow
// in `internal/daemon/oauth`: once the user has installed Watchfire's
// Slack app and Watchfire has captured a bot token, this package lets
// the daemon post messages and DMs without going through the legacy
// incoming-webhook-URL model.
//
// The v7.0 Relay Slack adapter (`internal/daemon/relay/slack.go`)
// continues to use incoming-webhook URLs for fanning notifications
// out to a channel — this package is purely additive, used by:
//
//   1. The "post hello after Connect" flow that proves the install
//      worked end-to-end.
//   2. Future v8.x slash-command DM responses to the originator on
//      private failures (covered separately in echo8sltr).
//
// All methods accept a context and a bot token as the first two
// arguments so callers can use a single shared client across multiple
// Slack installs.
package slackbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// API endpoints. Overridable for tests.
var (
	PostMessageURL = "https://slack.com/api/chat.postMessage"
	AuthTestURL    = "https://slack.com/api/auth.test"
)

// Client wraps an http.Client with a small set of Slack Web API
// helpers. Stateless — the bot token is passed per call so the same
// Client serves multiple workspaces.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with a 10-second per-request timeout.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// PostMessage sends a chat.postMessage to the named channel. `channel`
// is either a channel name (`#general`) or a channel ID (`C0123…`).
// Empty `channel` returns an error rather than silently dropping the
// message (Slack would 400 on `channel: ""` but the error message is
// less helpful than this).
func (c *Client) PostMessage(ctx context.Context, token, channel, text string) error {
	if c == nil || c.HTTP == nil {
		c = New()
	}
	if token == "" {
		return fmt.Errorf("slackbot: empty token")
	}
	if channel == "" {
		return fmt.Errorf("slackbot: empty channel")
	}

	body, _ := json.Marshal(map[string]any{
		"channel": channel,
		"text":    text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, PostMessageURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slackbot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("slackbot: POST chat.postMessage: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slackbot: chat.postMessage HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var parsed struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if uErr := json.Unmarshal(respBody, &parsed); uErr != nil {
		return fmt.Errorf("slackbot: parse response: %w (body=%s)", uErr, truncate(string(respBody), 200))
	}
	if !parsed.OK {
		msg := parsed.Error
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("slackbot: chat.postMessage rejected: %s", msg)
	}
	return nil
}

// AuthTest hits Slack's auth.test endpoint, returning the bot user's
// username. Used by the OAuth flow's post-completion verification +
// the settings UI's "Test connection" button.
func (c *Client) AuthTest(ctx context.Context, token string) (username string, err error) {
	if c == nil || c.HTTP == nil {
		c = New()
	}
	if token == "" {
		return "", fmt.Errorf("slackbot: empty token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, AuthTestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK    bool   `json:"ok"`
		User  string `json:"user"`
		Error string `json:"error"`
	}
	if uErr := json.Unmarshal(body, &parsed); uErr != nil {
		return "", uErr
	}
	if !parsed.OK {
		return "", fmt.Errorf("slackbot: auth.test rejected: %s", parsed.Error)
	}
	return parsed.User, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
