package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
)

// DiscordCommandsAPIBase is the per-application + per-guild Discord
// REST endpoint for managing slash commands. The CLI POSTs the three
// v8.0 command schemas (status / retry / cancel) here. Discord
// dedupes by command name, so re-running the CLI updates the existing
// commands rather than creating duplicates — this is the idempotency
// the spec asks for.
//
// https://discord.com/developers/docs/interactions/application-commands#bulk-overwrite-guild-application-commands
const DiscordCommandsAPIBase = "https://discord.com/api/v10"

// discordCommandsHTTPBase is the configurable base URL for tests. Set
// via the package-private setter so production code can use the
// constant without indirection.
var discordCommandsHTTPBase = DiscordCommandsAPIBase

// SetDiscordCommandsAPIBaseForTest swaps the API base URL — only used
// from `register_discord_test.go` to point the CLI at an
// `httptest.Server`. Pass empty to reset.
func SetDiscordCommandsAPIBaseForTest(s string) {
	if s == "" {
		discordCommandsHTTPBase = DiscordCommandsAPIBase
		return
	}
	discordCommandsHTTPBase = s
}

// discordSlashCommand is the subset of Discord's command-create JSON
// shape Watchfire uses. `Type: 1` is CHAT_INPUT (the slash-command
// type — Discord also has USER and MESSAGE commands but Watchfire
// only ships slash commands). `Options[].Type: 3` is STRING.
type discordSlashCommand struct {
	Name        string                 `json:"name"`
	Type        int                    `json:"type"`
	Description string                 `json:"description"`
	Options     []discordCommandOption `json:"options,omitempty"`
}

type discordCommandOption struct {
	Type        int    `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// watchfireSlashCommands is the canonical command roster Watchfire
// registers for v8.0 Echo. Adding a fourth command later is a YAML +
// CLI re-run; existing commands rotate by re-registering the slice.
func watchfireSlashCommands() []discordSlashCommand {
	return []discordSlashCommand{
		{
			Name:        "status",
			Type:        1,
			Description: "Show in-flight Watchfire tasks",
			Options: []discordCommandOption{
				{Type: 3, Name: "project", Description: "Optional project filter", Required: false},
			},
		},
		{
			Name:        "retry",
			Type:        1,
			Description: "Re-queue a failed Watchfire task",
			Options: []discordCommandOption{
				{Type: 3, Name: "task", Description: "Task id or task number", Required: true},
			},
		},
		{
			Name:        "cancel",
			Type:        1,
			Description: "Cancel a running Watchfire task",
			Options: []discordCommandOption{
				{Type: 3, Name: "task", Description: "Task id or task number", Required: true},
			},
		},
	}
}

var integrationsRegisterDiscordCmd = &cobra.Command{
	Use:   "register-discord <guild_id>",
	Short: "Register Watchfire's slash commands against a Discord guild",
	Long: `Register the three v8.0 Echo slash commands (status / retry / cancel)
against the Discord guild identified by <guild_id>. Idempotent — re-running
updates the existing commands rather than creating duplicates.

Reads the Discord application id and bot token from the user's keyring (set
via Settings → Integrations → Inbound). The bot must already be invited to
the guild with the applications.commands scope.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		guildID := strings.TrimSpace(args[0])
		if guildID == "" {
			return fmt.Errorf("register-discord: empty guild id")
		}

		cfg, err := config.LoadIntegrations()
		if err != nil {
			return fmt.Errorf("load integrations config: %w", err)
		}
		appID := cfg.Inbound.DiscordAppID
		if appID == "" {
			return fmt.Errorf("discord application id not configured (Settings → Integrations → Inbound → Discord App ID)")
		}
		tokenRef := cfg.Inbound.DiscordBotTokenRef
		if tokenRef == "" {
			return fmt.Errorf("discord bot token reference not configured (Settings → Integrations → Inbound → Discord Bot Token)")
		}
		token, ok := config.LookupIntegrationSecret(tokenRef)
		if !ok || token == "" {
			return fmt.Errorf("discord bot token not found in keyring under ref %q", tokenRef)
		}

		results, err := registerDiscordCommands(cmd.Context(), discordCommandsHTTPBase, appID, guildID, token, watchfireSlashCommands())
		if err != nil {
			return err
		}
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s /%s\n", r.statusGlyph(), r.Name)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Registered %d slash commands against guild %s\n", len(results), guildID)
		return nil
	},
}

type registerResult struct {
	Name string
	OK   bool
	Err  string
}

func (r registerResult) statusGlyph() string {
	if r.OK {
		return "✓"
	}
	return "✗"
}

// registerDiscordCommands POSTs each command in cmds to Discord's
// per-guild commands endpoint. The endpoint deduplicates by command
// name — Discord upserts on POST when a command with the same name
// already exists — so re-running this is idempotent without an
// explicit GET-first / DELETE-stale pass.
//
// Errors on individual commands are collected so the caller sees
// partial success when one command fails (e.g. the bot lacks the
// applications.commands scope on a single command). A 4xx from
// Discord is surfaced as an `[]registerResult` entry with `OK: false`
// rather than a Go-level error so the CLI prints a useful summary.
//
// Returns a Go-level error only when the request itself can't be
// built or the network is dead before the first POST.
func registerDiscordCommands(ctx context.Context, base, appID, guildID, token string, cmds []discordSlashCommand) ([]registerResult, error) {
	url := fmt.Sprintf("%s/applications/%s/guilds/%s/commands", base, appID, guildID)
	client := &http.Client{Timeout: 10 * time.Second}
	results := make([]registerResult, 0, len(cmds))
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
			results = append(results, registerResult{Name: c.Name, OK: false, Err: err.Error()})
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			results = append(results, registerResult{Name: c.Name, OK: true})
		} else {
			results = append(results, registerResult{Name: c.Name, OK: false, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))})
		}
	}
	// If every command failed with an error, surface a non-zero CLI exit.
	allFailed := len(results) > 0
	for _, r := range results {
		if r.OK {
			allFailed = false
			break
		}
	}
	if allFailed {
		fmt.Fprintln(os.Stderr, "register-discord: every command failed — see per-command output above")
	}
	return results, nil
}

func init() {
	integrationsCmd.AddCommand(integrationsRegisterDiscordCmd)
}
