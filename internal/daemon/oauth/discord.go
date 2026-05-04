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

// Discord OAuth2 endpoints. Same shape as Slack — a single
// authorize URL the user is redirected to, plus a token-exchange
// endpoint we POST to from the daemon.
const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"
	discordUserURL      = "https://discord.com/api/users/@me"
)

// DiscordBotScopes are the scopes Watchfire requests. `bot` grants the
// app a bot token; `applications.commands` lets us register slash
// commands without using the manual CLI flow. These are the minimum
// for v8.x parity with Slack.
const DiscordBotScopes = "bot applications.commands"

// DiscordPermissions is the integer permission bitmask requested for
// the bot. 2048 (SEND_MESSAGES) + 32768 (SEND_MESSAGES_IN_THREADS) +
// 2147483648 (USE_APPLICATION_COMMANDS) is the minimum for the v8.x
// scope. Users can elevate further from the install screen.
const DiscordPermissions = "2048"

// DiscordInstall captures the post-exchange data Watchfire wants to
// persist for Discord. Mirrors SlackInstall: a bot-style token plus
// non-secret metadata for the connected pill.
type DiscordInstall struct {
	BotToken      string
	Username      string
	Discriminator string
	GuildID       string
	GuildName     string
}

// DiscordTokenURL / DiscordUserURL / DiscordBotInfoURL are overridable
// by tests to point at httptest. Default targets the real Discord
// endpoints.
var (
	DiscordTokenURL    = discordTokenURL
	DiscordUserURL     = discordUserURL
	DiscordBotInfoURL  = "https://discord.com/api/applications/@me"
)

// BuildDiscordAuthURL constructs the Discord OAuth2 install URL.
// `permissions` is the integer bot-permissions bitmask; pass
// `DiscordPermissions` for the default minimum.
func BuildDiscordAuthURL(clientID, redirectURI, state, permissions string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("scope", DiscordBotScopes)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	if permissions != "" {
		q.Set("permissions", permissions)
	}
	return discordAuthorizeURL + "?" + q.Encode()
}

// ExchangeDiscordCode swaps the authorization code for an OAuth2
// access token + the bot token. Discord returns both: the user
// access_token (for /users/@me lookups) and the guild block carrying
// the bot's metadata.
//
// Note: Discord's OAuth flow returns the *user* access token; the
// permanent *bot* token is fetched separately via the application's
// "Bot" tab. For OAuth-driven bot installs, Discord returns the
// `guild` payload (with channels, members, etc.) but the bot token
// itself is the application's bot token, which Discord exposes via
// the application's settings page. v8.x assumes the user has pasted
// the bot token alongside the OAuth client credentials — OAuth's
// purpose here is to verify the install + capture the connected
// guild + the bot's display name.
func ExchangeDiscordCode(ctx context.Context, client *http.Client, clientID, clientSecret, code, redirectURI string) (*DiscordInstall, error) {
	if client == nil {
		client = http.DefaultClient
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, DiscordTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build discord exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: discord exchange POST: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth: discord exchange read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ErrTokenExchange{Provider: "discord", Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))}
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		BotToken    string `json:"bot_token"` // some flows surface the bot token directly; ignored when empty
		Guild       struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"guild"`
	}
	if uErr := json.Unmarshal(body, &parsed); uErr != nil {
		return nil, fmt.Errorf("oauth: discord exchange parse: %w (body=%s)", uErr, truncate(string(body), 200))
	}
	if parsed.AccessToken == "" && parsed.BotToken == "" {
		return nil, &ErrTokenExchange{Provider: "discord", Message: "empty token in response"}
	}

	install := &DiscordInstall{
		BotToken: parsed.BotToken,
		GuildID:  parsed.Guild.ID,
		GuildName: parsed.Guild.Name,
	}

	// Look up the bot's username via the application info endpoint.
	// Failure is non-fatal — the rest of the install can proceed.
	if username, discriminator, infoErr := discordAppInfo(ctx, client, parsed.AccessToken); infoErr == nil {
		install.Username = username
		install.Discriminator = discriminator
	}
	return install, nil
}

// discordAppInfo fetches the application's bot info using the user
// access token. Returns the bot username + discriminator. Best-effort —
// failure is non-fatal at the call site.
func discordAppInfo(ctx context.Context, client *http.Client, accessToken string) (username, discriminator string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DiscordBotInfoURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("discord app info HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Bot struct {
			Username      string `json:"username"`
			Discriminator string `json:"discriminator"`
		} `json:"bot"`
		Name string `json:"name"`
	}
	if uErr := json.Unmarshal(body, &parsed); uErr != nil {
		return "", "", uErr
	}
	if parsed.Bot.Username != "" {
		return parsed.Bot.Username, parsed.Bot.Discriminator, nil
	}
	return parsed.Name, "", nil
}
