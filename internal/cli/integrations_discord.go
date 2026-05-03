package cli

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/discord"
)

// DiscordCommandsAPIBase is re-exported for backwards compatibility with
// tests and callers that referenced the CLI's constant. The canonical
// definition lives in the shared `internal/daemon/discord` package now.
const DiscordCommandsAPIBase = discord.CommandsAPIBase

// discordCommandsHTTPBase is the configurable base URL for tests. Set via
// the package-private setter so production code can use the constant
// without indirection.
var discordCommandsHTTPBase = DiscordCommandsAPIBase

// SetDiscordCommandsAPIBaseForTest swaps the API base URL — only used from
// `integrations_discord_test.go` to point the CLI at an `httptest.Server`.
// Pass empty to reset.
func SetDiscordCommandsAPIBaseForTest(s string) {
	if s == "" {
		discordCommandsHTTPBase = DiscordCommandsAPIBase
		return
	}
	discordCommandsHTTPBase = s
}

var integrationsRegisterDiscordCmd = &cobra.Command{
	Use:   "register-discord <guild_id>",
	Short: "Register Watchfire's slash commands against a Discord guild",
	Long: `Register the three v8.0 Echo slash commands (status / retry / cancel)
against the Discord guild identified by <guild_id>. Idempotent — re-running
updates the existing commands rather than creating duplicates.

Reads the Discord application id and bot token from the user's keyring (set
via Settings → Integrations → Inbound). The bot must already be invited to
the guild with the applications.commands scope.

Note: as of v8.x Echo, the daemon auto-registers slash commands against
every guild the bot is in (and against new guilds within ~30 seconds of
the bot being added). This CLI command remains the manual fallback for
operators without Gateway access or who want to force a re-register.`,
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

		client := &http.Client{Timeout: 10 * time.Second}
		results, err := discord.RegisterGuildCommands(cmd.Context(), client, discordCommandsHTTPBase, appID, guildID, token, discord.WatchfireSlashCommands())
		if err != nil {
			return err
		}
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s /%s\n", r.StatusGlyph(), r.Name)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Registered %d slash commands against guild %s\n", len(results), guildID)
		// If every command failed, surface a non-zero CLI exit-style note on
		// stderr so a CI pipeline catches the mistake. We don't return an
		// error because partial success is still useful diagnostic output.
		if !discord.AllOK(results) {
			allFailed := true
			for _, r := range results {
				if r.OK {
					allFailed = false
					break
				}
			}
			if allFailed {
				fmt.Fprintln(os.Stderr, "register-discord: every command failed — see per-command output above")
			}
		}
		return nil
	},
}

func init() {
	integrationsCmd.AddCommand(integrationsRegisterDiscordCmd)
}
