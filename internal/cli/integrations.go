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
	Use:   "test <kind> <id>",
	Short: "Send a synthetic notification through an integration",
	Long: `Fire a synthetic notification through the named integration. Verifies the
plumbing end-to-end: keyring secret resolves, URL reachable, channel renders
the message correctly.

For Discord endpoints, the test sends one POST per supported notification
kind (TASK_FAILED, RUN_COMPLETE, WEEKLY_DIGEST) so every embed template is
exercised in a single command.

  watchfire integrations test discord  <id>
  watchfire integrations test slack    <id>
  watchfire integrations test webhook  <id>
  watchfire integrations test github   _

The github form has no id (single-instance config); pass any placeholder.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		kind, err := parseIntegrationKind(args[0])
		if err != nil {
			return err
		}
		id := args[1]

		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := connectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		client := pb.NewIntegrationsServiceClient(conn)
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
