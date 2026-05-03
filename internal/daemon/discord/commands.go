// Package discord houses the canonical Watchfire slash-command roster and
// the helpers that register that roster against a single Discord guild.
//
// The package is shared between the v8.0 Echo CLI (`watchfire integrations
// register-discord <guild>` — manual fallback, see
// `internal/cli/integrations_discord.go`) and the v8.x Echo daemon
// auto-registrar (this directory's `gateway.go` + `registrar.go`). Both
// callers POST the same three commands; only the trigger differs.
//
// Discord deduplicates by command name on POST, so re-running registration
// is idempotent — both the CLI and the auto-registrar rely on this. See:
// https://discord.com/developers/docs/interactions/application-commands#bulk-overwrite-guild-application-commands
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CommandsAPIBase is the per-application + per-guild Discord REST endpoint
// for managing slash commands. Both the manual CLI and the daemon
// auto-registrar POST against
// `{base}/applications/{app_id}/guilds/{guild_id}/commands`.
const CommandsAPIBase = "https://discord.com/api/v10"

// SlashCommand is the subset of Discord's command-create JSON shape
// Watchfire uses. `Type: 1` is CHAT_INPUT (the slash-command type — Discord
// also has USER and MESSAGE commands but Watchfire only ships slash
// commands). `Options[].Type: 3` is STRING.
type SlashCommand struct {
	Name        string           `json:"name"`
	Type        int              `json:"type"`
	Description string           `json:"description"`
	Options     []CommandOption  `json:"options,omitempty"`
}

// CommandOption is a single argument to a SlashCommand.
type CommandOption struct {
	Type        int    `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// WatchfireSlashCommands is the canonical command roster Watchfire registers
// for v8.0 Echo. Adding a fourth command later is a constant edit + a
// re-register; the registrar will land it on the next guild event.
func WatchfireSlashCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:        "status",
			Type:        1,
			Description: "Show in-flight Watchfire tasks",
			Options: []CommandOption{
				{Type: 3, Name: "project", Description: "Optional project filter", Required: false},
			},
		},
		{
			Name:        "retry",
			Type:        1,
			Description: "Re-queue a failed Watchfire task",
			Options: []CommandOption{
				{Type: 3, Name: "task", Description: "Task id or task number", Required: true},
			},
		},
		{
			Name:        "cancel",
			Type:        1,
			Description: "Cancel a running Watchfire task",
			Options: []CommandOption{
				{Type: 3, Name: "task", Description: "Task id or task number", Required: true},
			},
		},
	}
}

// RegisterResult is the per-command outcome of a registration attempt.
type RegisterResult struct {
	Name string
	OK   bool
	Err  string
}

// StatusGlyph returns "✓" / "✗" for CLI rendering.
func (r RegisterResult) StatusGlyph() string {
	if r.OK {
		return "✓"
	}
	return "✗"
}

// RegisterGuildCommands POSTs each command in cmds to Discord's per-guild
// commands endpoint at `{base}/applications/{appID}/guilds/{guildID}/commands`.
// Discord upserts on POST when a command with the same name already exists,
// so re-running this is idempotent without an explicit GET-first / DELETE-stale
// pass.
//
// Errors on individual commands are collected so the caller sees partial
// success when one command fails (e.g. the bot lacks the
// applications.commands scope on a single command). A 4xx from Discord is
// surfaced as a per-command result with `OK: false` rather than a Go-level
// error so callers can render a useful summary.
//
// Returns a Go-level error only when the request itself can't be built or
// the network is dead before the first POST.
func RegisterGuildCommands(ctx context.Context, client *http.Client, base, appID, guildID, token string, cmds []SlashCommand) ([]RegisterResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	url := fmt.Sprintf("%s/applications/%s/guilds/%s/commands", base, appID, guildID)
	results := make([]RegisterResult, 0, len(cmds))
	for _, c := range cmds {
		body, err := json.Marshal(c)
		if err != nil {
			return nil, fmt.Errorf("marshal command %q: %w", c.Name, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bot "+token)
		req.Header.Set("User-Agent", "watchfire-echo/1")

		resp, err := client.Do(req)
		if err != nil {
			results = append(results, RegisterResult{Name: c.Name, OK: false, Err: err.Error()})
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			results = append(results, RegisterResult{Name: c.Name, OK: true})
		} else {
			results = append(results, RegisterResult{Name: c.Name, OK: false, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))})
		}
	}
	return results, nil
}

// AllOK reports whether every result in rs succeeded.
func AllOK(rs []RegisterResult) bool {
	if len(rs) == 0 {
		return false
	}
	for _, r := range rs {
		if !r.OK {
			return false
		}
	}
	return true
}

// FirstError returns the first non-empty error message in rs, or "" if all
// succeeded. Used by the registrar to surface a single error string per
// guild in the InboundStatus response.
func FirstError(rs []RegisterResult) string {
	for _, r := range rs {
		if !r.OK {
			return fmt.Sprintf("%s: %s", r.Name, r.Err)
		}
	}
	return ""
}
