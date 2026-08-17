package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	pb "github.com/watchfire-io/watchfire/proto"
)

// integrationsCmd is the parent for the v7.0 Relay outbound-integrations
// CLI surface. The settings UI lives in the GUI / TUI; this command
// covers headless workflows (CI checks, scripted setup verification).
var integrationsCmd = &cobra.Command{
	Use:   "integrations",
	Short: "Manage outbound integrations (Webhook / Slack / Discord / GitHub / Telegram)",
	Long: `Inspect and exercise the outbound integrations configured in ~/.watchfire/integrations.yaml.

For the Telegram bridge, chat pairing and status live under their own
command group: 'watchfire telegram pair' / 'watchfire telegram status'.`,
}

var integrationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured integrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := ConnectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := pb.NewIntegrationsServiceClient(conn)
		cfg, err := client.ListIntegrations(ctx, &pb.ListIntegrationsRequest{})
		if err != nil {
			return fmt.Errorf("list integrations: %w", err)
		}
		printIntegrations(cfg)
		return nil
	},
}

// telegramAddToken holds the --token flag for `integrations add telegram`
// so scripted setups can skip the interactive prompt.
var telegramAddToken string

var integrationsAddCmd = &cobra.Command{
	Use:   "add <kind>",
	Short: "Add an outbound integration (telegram)",
	Long: `Configure a new outbound integration from the terminal.

Currently only the Telegram bridge can be added here:

  watchfire integrations add telegram              # prompts for the bot token
  watchfire integrations add telegram --token ...  # non-interactive

Create the bot with @BotFather first, then paste its token at the prompt.
The token is stored in the OS keyring (write-only — 'watchfire integrations
list' reports only whether one is set). The integration is saved enabled;
on a fresh add the TASK_FAILED + RUN_COMPLETE events default on, while
re-adding over an existing config only rotates the token and keeps the
configured events.

Next steps: authorize your chat with 'watchfire telegram pair', then check
bridge health and paired chats with 'watchfire telegram status'.

Other kinds (webhook / slack / discord / github) are added in Settings →
Integrations (GUI or TUI).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kind := strings.ToLower(args[0])
		if kind != "telegram" {
			return fmt.Errorf("adding %q from the CLI is not supported — use Settings → Integrations in the GUI/TUI (only 'telegram' can be added here)", args[0])
		}

		token := strings.TrimSpace(telegramAddToken)
		if token == "" {
			var err error
			token, err = promptSecret("Bot token (from @BotFather): ")
			if err != nil {
				return err
			}
		}
		if token == "" {
			return fmt.Errorf("no token provided — create a bot with @BotFather and paste its token")
		}

		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := ConnectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		client := pb.NewIntegrationsServiceClient(conn)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Preserve the configured events on token rotation; fresh adds
		// get the default event set.
		existing, err := client.ListIntegrations(ctx, &pb.ListIntegrationsRequest{})
		if err != nil {
			return fmt.Errorf("list integrations: %w", err)
		}
		payload := buildTelegramAddPayload(existing.GetTelegram(), token)

		cfg, err := client.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
			Payload: &pb.SaveIntegrationRequest_Telegram{Telegram: payload},
		})
		if err != nil {
			return fmt.Errorf("save integration: %w", err)
		}

		tg := cfg.GetTelegram()
		fmt.Printf("✓ telegram integration saved (enabled, token %s, %d paired chat(s))\n",
			map[bool]string{true: "stored in keyring", false: "NOT stored"}[tg.GetTokenSet()],
			len(tg.GetPairedChats()),
		)
		if len(tg.GetPairedChats()) == 0 {
			fmt.Println("Next: run 'watchfire telegram pair' to authorize your chat.")
		}
		return nil
	},
}

// buildTelegramAddPayload rolls a bot token into a SaveIntegration
// payload. Fresh add: enabled with the default event set (TASK_FAILED +
// RUN_COMPLETE). Existing config: the add only rotates the token —
// events are preserved — but the integration always comes out enabled
// (the command's purpose is a working bridge). PairedChats stays nil:
// the daemon-side upsert never touches chats it isn't handed.
func buildTelegramAddPayload(existing *pb.TelegramIntegration, token string) *pb.TelegramIntegration {
	out := &pb.TelegramIntegration{
		Enabled:  true,
		BotToken: token,
		EnabledEvents: &pb.IntegrationEvents{
			TaskFailed:  true,
			RunComplete: true,
		},
	}
	if existing != nil && existing.GetEnabledEvents() != nil {
		ev := existing.GetEnabledEvents()
		out.EnabledEvents = &pb.IntegrationEvents{
			TaskFailed:   ev.GetTaskFailed(),
			RunComplete:  ev.GetRunComplete(),
			WeeklyDigest: ev.GetWeeklyDigest(),
		}
	}
	return out
}

// promptSecret reads a secret from stdin — without echo when stdin is a
// real terminal, as a plain line otherwise (pipes, heredocs, CI).
func promptSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read token: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

var integrationsTestCmd = &cobra.Command{
	Use:   "test [kind] <id>",
	Short: "Send a synthetic notification through an integration",
	Long: `Fire a synthetic notification through the named integration. Verifies the
plumbing end-to-end: keyring secret resolves, URL reachable, channel renders
the message correctly.

The single-arg form looks the id up across every configured integration; the
two-arg form pins the kind explicitly when an id is reused across kinds:

  watchfire integrations test <id>            # auto-detect across all kinds
  watchfire integrations test webhook  <id>
  watchfire integrations test slack    <id>
  watchfire integrations test discord  <id>
  watchfire integrations test github   _
  watchfire integrations test telegram

For Discord / Slack endpoints, the test sends one POST per supported
notification kind (TASK_FAILED, RUN_COMPLETE, WEEKLY_DIGEST) so every
template is exercised in a single command. The github and telegram
forms are single-instance and need no id. A telegram test delivers a
sample message to every paired chat (pair one first with 'watchfire
telegram pair').`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var kind pb.IntegrationKind
		var id string
		if len(args) == 2 {
			k, err := parseIntegrationKind(args[0])
			if err != nil {
				return err
			}
			kind = k
			id = args[1]
		} else {
			id = args[0]
		}

		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := ConnectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		client := pb.NewIntegrationsServiceClient(conn)

		// Auto-detect kind by scanning the configured integrations
		// when only the id was provided.
		if len(args) == 1 {
			lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer lookupCancel()
			cfg, err := client.ListIntegrations(lookupCtx, &pb.ListIntegrationsRequest{})
			if err != nil {
				return fmt.Errorf("list integrations: %w", err)
			}
			detected, ok := detectIntegrationKind(cfg, id)
			if !ok {
				return fmt.Errorf("no integration found with id %q (run `watchfire integrations list` to see configured ids)", id)
			}
			kind = detected
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.TestIntegration(ctx, &pb.TestIntegrationRequest{
			Kind: kind,
			Id:   id,
		})
		if err != nil {
			return fmt.Errorf("test integration: %w", err)
		}

		status := "✓"
		if !resp.GetOk() {
			status = "✗"
		}
		fmt.Printf("%s %s\n", status, resp.GetMessage())
		if !resp.GetOk() {
			os.Exit(1)
		}
		return nil
	},
}

// detectIntegrationKind searches every configured integration list for
// a matching id. Returns the matching kind on the first hit (webhook
// → slack → discord → github) or false when no entry matches. Used by
// the single-arg form of `watchfire integrations test`.
func detectIntegrationKind(cfg *pb.IntegrationsConfig, id string) (pb.IntegrationKind, bool) {
	if cfg == nil {
		return 0, false
	}
	for _, ep := range cfg.GetWebhooks() {
		if ep.GetId() == id {
			return pb.IntegrationKind_WEBHOOK, true
		}
	}
	for _, ep := range cfg.GetSlack() {
		if ep.GetId() == id {
			return pb.IntegrationKind_SLACK, true
		}
	}
	for _, ep := range cfg.GetDiscord() {
		if ep.GetId() == id {
			return pb.IntegrationKind_DISCORD, true
		}
	}
	if g := cfg.GetGithub(); g != nil && g.GetEnabled() && id == "github" {
		return pb.IntegrationKind_GITHUB, true
	}
	if tg := cfg.GetTelegram(); tg != nil && id == "telegram" {
		return pb.IntegrationKind_TELEGRAM, true
	}
	return 0, false
}

func parseIntegrationKind(s string) (pb.IntegrationKind, error) {
	switch strings.ToLower(s) {
	case "webhook":
		return pb.IntegrationKind_WEBHOOK, nil
	case "slack":
		return pb.IntegrationKind_SLACK, nil
	case "discord":
		return pb.IntegrationKind_DISCORD, nil
	case "github":
		return pb.IntegrationKind_GITHUB, nil
	case "telegram":
		return pb.IntegrationKind_TELEGRAM, nil
	}
	return 0, fmt.Errorf("unknown integration kind %q (want one of: webhook, slack, discord, github, telegram)", s)
}

func printIntegrations(cfg *pb.IntegrationsConfig) {
	if cfg == nil {
		fmt.Println("(no integrations configured)")
		return
	}
	any := false
	for _, ep := range cfg.GetWebhooks() {
		any = true
		fmt.Printf("webhook  %s  %s  %s  events=[%s]\n",
			ep.GetId(), trimDisplay(ep.GetLabel()), ep.GetUrlLabel(),
			eventSummary(ep.GetEnabledEvents()),
		)
	}
	for _, ep := range cfg.GetSlack() {
		any = true
		fmt.Printf("slack    %s  %s  %s  events=[%s]\n",
			ep.GetId(), trimDisplay(ep.GetLabel()), ep.GetUrlLabel(),
			eventSummary(ep.GetEnabledEvents()),
		)
	}
	for _, ep := range cfg.GetDiscord() {
		any = true
		fmt.Printf("discord  %s  %s  %s  events=[%s]\n",
			ep.GetId(), trimDisplay(ep.GetLabel()), ep.GetUrlLabel(),
			eventSummary(ep.GetEnabledEvents()),
		)
	}
	if g := cfg.GetGithub(); g != nil && g.GetEnabled() {
		any = true
		scopes := "(all)"
		if len(g.GetProjectScopes()) > 0 {
			scopes = strings.Join(g.GetProjectScopes(), ",")
		}
		fmt.Printf("github   auto-PR enabled  scopes=%s  draft=%v\n", scopes, g.GetDraftDefault())
	}
	if tg := cfg.GetTelegram(); tg != nil {
		any = true
		state := "disabled"
		if tg.GetEnabled() {
			state = "enabled"
		}
		token := "token=unset"
		if tg.GetTokenSet() {
			token = "token=set"
		}
		fmt.Printf("telegram %s  %s  paired_chats=%d  events=[%s]\n",
			state, token, len(tg.GetPairedChats()),
			eventSummary(tg.GetEnabledEvents()),
		)
	}
	if !any {
		fmt.Println("(no integrations configured)")
	}
}

func eventSummary(e *pb.IntegrationEvents) string {
	if e == nil {
		return ""
	}
	var on []string
	if e.GetTaskFailed() {
		on = append(on, "TASK_FAILED")
	}
	if e.GetRunComplete() {
		on = append(on, "RUN_COMPLETE")
	}
	if e.GetWeeklyDigest() {
		on = append(on, "WEEKLY_DIGEST")
	}
	return strings.Join(on, ",")
}

func trimDisplay(s string) string {
	if s == "" {
		return "(unlabelled)"
	}
	return s
}

func init() {
	integrationsAddCmd.Flags().StringVar(&telegramAddToken, "token", "", "bot token (skips the interactive prompt)")
	integrationsCmd.AddCommand(integrationsAddCmd)
	integrationsCmd.AddCommand(integrationsListCmd)
	integrationsCmd.AddCommand(integrationsTestCmd)
	rootCmd.AddCommand(integrationsCmd)
}
