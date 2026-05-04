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

// integrationsOAuthCmd is the v8.x parent for the OAuth bot-token
// install flows. Provides headless-friendly entry points for the
// browser-driven flow:
//
//   watchfire integrations oauth slack    [--channel <id>]
//   watchfire integrations oauth discord  [--channel <id>]
//
// The OAuth daemon RPC opens the user's default browser pointed at
// the upstream provider's authorize URL; the daemon races a loopback
// callback listener that exchanges the resulting code for a bot
// token and persists it to the keyring. This CLI command polls
// `GetOAuthStatus` until the flow lands at CONNECTED or ERROR.
var integrationsOAuthCmd = &cobra.Command{
	Use:   "oauth [provider]",
	Short: "Install Watchfire's bot via OAuth (slack / discord)",
	Long: `Run the v8.x OAuth bot-token install flow for Slack or Discord.

Prerequisites: the project's OAuth client credentials must already be
configured in ~/.watchfire/integrations.yaml under inbound.slack_client_id /
inbound.discord_client_id (and the matching secret saved via the gRPC
SaveInboundConfig call). The redirect URI Watchfire generates is
http://127.0.0.1:<dynamic-port>/oauth/<provider>/callback — register that
exact URL in the upstream provider's OAuth app settings before running this
command.

After install completes the daemon stores the bot token in the OS keyring
and persists non-secret metadata (team id / bot username) to integrations.yaml.
Pass --hello to send a "hello" message through the captured token to verify
the install end-to-end.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseOAuthProvider(args[0])
		if err != nil {
			return err
		}

		channel, _ := cmd.Flags().GetString("channel")
		hello, _ := cmd.Flags().GetBool("hello")
		text, _ := cmd.Flags().GetString("text")
		timeoutSecs, _ := cmd.Flags().GetInt("timeout")

		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := connectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		client := pb.NewIntegrationsServiceClient(conn)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		begin, err := client.BeginOAuth(ctx, &pb.BeginOAuthRequest{
			Provider:       provider,
			DefaultChannel: channel,
		})
		if err != nil {
			return fmt.Errorf("begin oauth: %w", err)
		}

		fmt.Fprintf(os.Stdout, "Open the following URL in your browser to complete the install:\n\n  %s\n\n", begin.GetAuthorizeUrl())
		fmt.Fprintf(os.Stdout, "Watchfire is listening for the callback at:\n  %s\n\n", begin.GetRedirectUri())
		fmt.Fprintf(os.Stdout, "Waiting for browser redirect (timeout %ds)...\n", timeoutSecs)

		pollCtx, pollCancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
		defer pollCancel()

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		var final *pb.OAuthStatus
		for {
			select {
			case <-pollCtx.Done():
				_, _ = client.CancelOAuth(context.Background(), &pb.CancelOAuthRequest{Provider: provider})
				return fmt.Errorf("oauth flow timed out after %ds", timeoutSecs)
			case <-ticker.C:
				st, err := client.GetOAuthStatus(pollCtx, &pb.GetOAuthStatusRequest{Provider: provider})
				if err != nil {
					return fmt.Errorf("poll oauth status: %w", err)
				}
				switch st.GetState() {
				case pb.OAuthState_OAUTH_STATE_CONNECTED:
					final = st
				case pb.OAuthState_OAUTH_STATE_ERROR:
					return fmt.Errorf("oauth flow failed: %s", st.GetError())
				}
			}
			if final != nil {
				break
			}
		}

		fmt.Fprintf(os.Stdout, "\nConnected as %s\n", final.GetConnectedAs())

		if hello {
			helloCtx, helloCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer helloCancel()
			resp, err := client.PostOAuthHello(helloCtx, &pb.PostOAuthHelloRequest{
				Provider: provider,
				Channel:  channel,
				Text:     text,
			})
			if err != nil {
				return fmt.Errorf("post hello: %w", err)
			}
			if !resp.GetOk() {
				return fmt.Errorf("hello message failed: %s", resp.GetMessage())
			}
			fmt.Fprintf(os.Stdout, "Hello message: %s\n", resp.GetMessage())
		}

		return nil
	},
}

var integrationsOAuthStatusCmd = &cobra.Command{
	Use:   "oauth-status [provider]",
	Short: "Show the OAuth bot-token install status (slack / discord)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseOAuthProvider(args[0])
		if err != nil {
			return err
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st, err := client.GetOAuthStatus(ctx, &pb.GetOAuthStatusRequest{Provider: provider})
		if err != nil {
			return err
		}
		fmt.Printf("Provider:      %s\n", strings.ToLower(strings.TrimPrefix(provider.String(), "OAUTH_PROVIDER_")))
		fmt.Printf("State:         %s\n", strings.ToLower(strings.TrimPrefix(st.GetState().String(), "OAUTH_STATE_")))
		if st.GetConnectedAs() != "" {
			fmt.Printf("Connected as:  %s\n", st.GetConnectedAs())
		}
		if st.GetDefaultChannel() != "" {
			fmt.Printf("Channel:       %s\n", st.GetDefaultChannel())
		}
		if st.GetError() != "" {
			fmt.Printf("Last error:    %s\n", st.GetError())
		}
		return nil
	},
}

func parseOAuthProvider(s string) (pb.OAuthProvider, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "slack":
		return pb.OAuthProvider_OAUTH_PROVIDER_SLACK, nil
	case "discord":
		return pb.OAuthProvider_OAUTH_PROVIDER_DISCORD, nil
	}
	return pb.OAuthProvider_OAUTH_PROVIDER_UNSET, fmt.Errorf("unknown provider %q (expected slack / discord)", s)
}

func init() {
	integrationsOAuthCmd.Flags().String("channel", "", "Default channel for the post-install hello message")
	integrationsOAuthCmd.Flags().Bool("hello", false, "Post a 'hello' message after install completes")
	integrationsOAuthCmd.Flags().String("text", "", "Override the hello message text")
	integrationsOAuthCmd.Flags().Int("timeout", 600, "How long to wait for the browser callback (seconds)")
	integrationsCmd.AddCommand(integrationsOAuthCmd)
	integrationsCmd.AddCommand(integrationsOAuthStatusCmd)
}
