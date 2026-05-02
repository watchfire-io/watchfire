package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	pb "github.com/watchfire-io/watchfire/proto"
)

// integrationsCmd is the parent for the v7.0 Relay outbound-integrations
// CLI surface. The settings UI lives in the GUI / TUI; this command
// covers headless workflows (CI checks, scripted setup verification).
var integrationsCmd = &cobra.Command{
	Use:   "integrations",
	Short: "Manage outbound integrations (Webhook / Slack / Discord / GitHub)",
	Long:  `Inspect and exercise the outbound integrations configured in ~/.watchfire/integrations.yaml.`,
}

var integrationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured integrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := connectDaemon()
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

For Discord / Slack endpoints, the test sends one POST per supported
notification kind (TASK_FAILED, RUN_COMPLETE, WEEKLY_DIGEST) so every
template is exercised in a single command. The github form has no id
(single-instance config); pass any placeholder.`,
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
		conn, err := connectDaemon()
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
	}
	return 0, fmt.Errorf("unknown integration kind %q (want one of: webhook, slack, discord, github)", s)
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
	integrationsCmd.AddCommand(integrationsListCmd)
	integrationsCmd.AddCommand(integrationsTestCmd)
	rootCmd.AddCommand(integrationsCmd)
}
