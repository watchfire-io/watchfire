package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Slack OAuth v2 endpoints. Both are relative to slack.com so testing
// can swap the base URL via `SlackTokenURL`.
const (
	slackAuthorizeURL = "https://slack.com/oauth/v2/authorize"
	slackAccessURL    = "https://slack.com/api/oauth.v2.access"
)

// SlackBotScopes are the scopes Watchfire requests during install.
// These are the minimum needed for chat.postMessage + slash-command
// reply + DM. Anything beyond this is opt-in for v8.x follow-ups
// (interactive components, modal opens, etc.).
const SlackBotScopes = "chat:write,commands,im:write,users:read"

// SlackInstall captures the post-exchange data Watchfire wants to
// persist. The bot token is the secret; everything else is non-secret
// metadata used by the settings UI to render "Connected as @bot in
// <team>".
type SlackInstall struct {
	BotToken    string
	BotUserID   string
	BotUsername string
	TeamID      string
	TeamName    string
}

// SlackTokenURL is overridden by tests to point at httptest. Default
// targets the real Slack endpoint.
var SlackTokenURL = slackAccessURL

// BuildSlackAuthURL constructs the Slack OAuth v2 authorization URL.
// Callers fetch the state from `StateStore.Begin` first.
func BuildSlackAuthURL(clientID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("scope", SlackBotScopes)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return slackAuthorizeURL + "?" + q.Encode()
}

// ExchangeSlackCode swaps the authorization code for a bot token by
// POSTing to oauth.v2.access. Returns the install metadata on success.
//
// Slack returns 200 even on a failed exchange; the caller has to
// unwrap the `ok` field. Non-ok responses surface as ErrTokenExchange
// with the upstream's `error` string.
func ExchangeSlackCode(ctx context.Context, client *http.Client, clientID, clientSecret, code, redirectURI string) (*SlackInstall, error) {
	if client == nil {
		client = http.DefaultClient
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SlackTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build slack exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: slack exchange POST: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth: slack exchange read body: %w", err)
	}

	var parsed struct {
		OK        bool   `json:"ok"`
		Error     string `json:"error"`
		AccessToken string `json:"access_token"`
		BotUserID string `json:"bot_user_id"`
		Team      struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		AuthedUser struct {
			ID string `json:"id"`
		} `json:"authed_user"`
		AppID string `json:"app_id"`
	}
	if uErr := json.Unmarshal(body, &parsed); uErr != nil {
		return nil, fmt.Errorf("oauth: slack exchange parse: %w (body=%s)", uErr, truncate(string(body), 200))
	}
	if !parsed.OK {
		msg := parsed.Error
		if msg == "" {
			msg = "unknown error"
		}
		return nil, &ErrTokenExchange{Provider: "slack", Message: msg}
	}
	if parsed.AccessToken == "" {
		return nil, &ErrTokenExchange{Provider: "slack", Message: "empty access_token"}
	}

	install := &SlackInstall{
		BotToken:  parsed.AccessToken,
		BotUserID: parsed.BotUserID,
		TeamID:    parsed.Team.ID,
		TeamName:  parsed.Team.Name,
	}
	// `auth.test` returns the bot username; we run it inline here so the
	// settings UI can render the pill without an extra round trip.
	if name, nameErr := slackAuthTest(ctx, client, parsed.AccessToken); nameErr == nil {
		install.BotUsername = name
	}
	return install, nil
}

// slackAuthTest hits Slack's auth.test endpoint to fetch the bot's
// username. Failure is non-fatal — the rest of the install can
// proceed; the UI just renders without the @name suffix.
func slackAuthTest(ctx context.Context, client *http.Client, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SlackAuthTestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK   bool   `json:"ok"`
		User string `json:"user"`
	}
	if uErr := json.Unmarshal(body, &parsed); uErr != nil {
		return "", uErr
	}
	if !parsed.OK {
		return "", fmt.Errorf("slack auth.test not ok")
	}
	return parsed.User, nil
}

// SlackAuthTestURL is overridden by tests; default targets the real
// Slack endpoint.
var SlackAuthTestURL = "https://slack.com/api/auth.test"

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
