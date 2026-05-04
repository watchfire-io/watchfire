// Package discordbot is the v8.x bot-token client for Discord's REST
// API. Mirrors `internal/daemon/slackbot` — a thin wrapper that the
// post-OAuth "hello message" flow uses to confirm the install
// succeeded and that future v8.x slash-command DM responses can route
// through.
//
// Auth uses the `Authorization: Bot <token>` header (note: prefix is
// `Bot`, not `Bearer` — Discord's convention). All endpoints accept
// the bot token per call so the same Client can serve multiple
// applications.
package discordbot

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
	APIBase            = "https://discord.com/api/v10"
	CreateMessageURL   = APIBase + "/channels/%s/messages"
	BotInfoURL         = APIBase + "/users/@me"
	GuildCommandsURL   = APIBase + "/applications/%s/guilds/%s/commands"
	GlobalCommandsURL  = APIBase + "/applications/%s/commands"
)

// Client wraps an http.Client with Discord REST helpers. Stateless.
type Client struct {
	HTTP *http.Client
}

// New returns a client with a 10-second per-request timeout.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// PostMessage sends a message to the named channel ID. Discord
// channel IDs are snowflakes (numeric strings); the bot must already
// be a member of the guild containing the channel + have
// `SEND_MESSAGES` permission there.
func (c *Client) PostMessage(ctx context.Context, token, channelID, content string) error {
	if c == nil || c.HTTP == nil {
		c = New()
	}
	if token == "" {
		return fmt.Errorf("discordbot: empty token")
	}
	if channelID == "" {
		return fmt.Errorf("discordbot: empty channel id")
	}

	body, _ := json.Marshal(map[string]any{"content": content})
	url := fmt.Sprintf(CreateMessageURL, channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discordbot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("discordbot: POST create message: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discordbot: create message HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	return nil
}

// BotInfo fetches the bot user's profile (username + discriminator).
// Used post-OAuth to populate the "Connected as" pill without keeping
// the response of /users/@me hot in memory across requests.
type BotInfo struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
}

// GetBotInfo fetches the bot's profile via /users/@me.
func (c *Client) GetBotInfo(ctx context.Context, token string) (*BotInfo, error) {
	if c == nil || c.HTTP == nil {
		c = New()
	}
	if token == "" {
		return nil, fmt.Errorf("discordbot: empty token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BotInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discordbot: /users/@me HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var info BotInfo
	if uErr := json.Unmarshal(body, &info); uErr != nil {
		return nil, uErr
	}
	return &info, nil
}

// ApplicationCommand mirrors the minimal Discord application-command
// shape — name, description, type. Used by RegisterCommands when the
// caller wants to upsert a guild-scoped command set.
type ApplicationCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        int    `json:"type,omitempty"`
}

// RegisterGuildCommands upserts the command set on a single guild via
// PUT (atomic replacement). Used by the OAuth flow's post-install
// hook when the user enables auto-register; the dedicated
// `internal/daemon/discord/registrar` package has the more nuanced
// per-attempt status tracking but this is the simpler path for a
// one-shot install.
func (c *Client) RegisterGuildCommands(ctx context.Context, token, appID, guildID string, cmds []ApplicationCommand) error {
	if c == nil || c.HTTP == nil {
		c = New()
	}
	if appID == "" || guildID == "" {
		return fmt.Errorf("discordbot: appID and guildID required")
	}
	body, _ := json.Marshal(cmds)
	url := fmt.Sprintf(GuildCommandsURL, appID, guildID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("discordbot: register commands: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discordbot: register commands HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
