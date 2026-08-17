package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	pb "github.com/watchfire-io/watchfire/proto"
)

// telegramCmd is the parent for the v10.0 Torch Telegram bridge CLI
// surface. Pairing is the security boundary: the bot is globally
// reachable on Telegram, and only chats paired through the one-time
// code minted here ever see project data.
var telegramCmd = &cobra.Command{
	Use:   "telegram",
	Short: "Pair and inspect the Telegram bridge",
	Long: `Manage the Telegram bridge (v10.0 Torch).

Set up the bot first: create one with @BotFather, then store the token via
Settings → Integrations → Telegram (GUI/TUI) and enable the integration.
Once the bridge is running, 'watchfire telegram pair' authorizes a chat.`,
}

var telegramPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair a Telegram chat with this daemon",
	Long: `Mint a one-time pairing code and wait for it to be redeemed.

Open the printed deep link on your phone (it delivers the code to the bot
automatically) or send '/pair <code>' to the bot yourself. The code is
single-use and expires after 10 minutes; Ctrl-C aborts the wait without
invalidating the code.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := ConnectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		client := pb.NewIntegrationsServiceClient(conn)

		beginCtx, beginCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer beginCancel()
		begin, err := client.BeginTelegramPairing(beginCtx, &pb.BeginTelegramPairingRequest{})
		if err != nil {
			return fmt.Errorf("begin telegram pairing: %w", err)
		}

		expiresAt := begin.GetExpiresAt().AsTime()
		fmt.Fprintf(os.Stdout, "Pairing code: %s\n\n", begin.GetCode())
		fmt.Fprintf(os.Stdout, "Open this link on your phone to pair @%s:\n\n  %s\n\n", begin.GetBotUsername(), begin.GetDeepLink())
		fmt.Fprintf(os.Stdout, "…or send the bot:  /pair %s\n\n", begin.GetCode())
		fmt.Fprintf(os.Stdout, "Waiting for pairing (code expires %s, Ctrl-C to stop waiting)...\n",
			expiresAt.Local().Format("15:04:05"))

		// Poll until the code is redeemed or its TTL lapses. A small
		// grace past the expiry lets the daemon-side state flip to
		// expired before we give up.
		pollCtx, pollCancel := context.WithDeadline(context.Background(), expiresAt.Add(10*time.Second))
		defer pollCancel()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return fmt.Errorf("pairing code expired before it was redeemed — run 'watchfire telegram pair' again")
			case <-ticker.C:
			}
			st, err := client.GetTelegramPairingStatus(pollCtx, &pb.GetTelegramPairingStatusRequest{})
			if err != nil {
				return fmt.Errorf("poll pairing status: %w", err)
			}
			switch st.GetState() {
			case pb.TelegramPairingState_TELEGRAM_PAIRING_PAIRED:
				chat := st.GetChat()
				who := chat.GetUsername()
				if who == "" {
					who = fmt.Sprintf("chat %d", chat.GetChatId())
				} else {
					who = "@" + who
				}
				fmt.Fprintf(os.Stdout, "\nPaired with %s (chat %d). Project data now flows to this chat only.\n", who, chat.GetChatId())
				return nil
			case pb.TelegramPairingState_TELEGRAM_PAIRING_EXPIRED:
				return fmt.Errorf("pairing code expired — run 'watchfire telegram pair' again")
			case pb.TelegramPairingState_TELEGRAM_PAIRING_NONE:
				return fmt.Errorf("pairing state was reset (daemon restarted?) — run 'watchfire telegram pair' again")
			}
		}
	},
}

var telegramStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Telegram bridge status and paired chats",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return err
		}
		conn, err := ConnectDaemon()
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		client := pb.NewIntegrationsServiceClient(conn)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		st, err := client.GetTelegramPairingStatus(ctx, &pb.GetTelegramPairingStatusRequest{})
		if err != nil {
			return fmt.Errorf("get telegram status: %w", err)
		}
		cfg, err := client.ListIntegrations(ctx, &pb.ListIntegrationsRequest{})
		if err != nil {
			return fmt.Errorf("list integrations: %w", err)
		}
		tg := cfg.GetTelegram()

		if st.GetBridgeRunning() {
			fmt.Fprintln(os.Stdout, "Bridge:      running")
		} else {
			fmt.Fprintln(os.Stdout, "Bridge:      not running")
		}
		if tg == nil {
			fmt.Fprintln(os.Stdout, "Configured:  no — add the integration in Settings → Integrations → Telegram")
			return nil
		}
		fmt.Fprintf(os.Stdout, "Enabled:     %v\n", tg.GetEnabled())
		fmt.Fprintf(os.Stdout, "Token:       %s\n", map[bool]string{true: "stored in keyring", false: "not set"}[tg.GetTokenSet()])
		if st.GetBotUsername() != "" {
			fmt.Fprintf(os.Stdout, "Bot:         @%s\n", st.GetBotUsername())
		}
		switch st.GetState() {
		case pb.TelegramPairingState_TELEGRAM_PAIRING_PENDING:
			fmt.Fprintf(os.Stdout, "Pairing:     code pending (expires %s)\n", st.GetExpiresAt().AsTime().Local().Format("15:04:05"))
		case pb.TelegramPairingState_TELEGRAM_PAIRING_EXPIRED:
			fmt.Fprintln(os.Stdout, "Pairing:     last code expired unredeemed")
		}

		chats := tg.GetPairedChats()
		if len(chats) == 0 {
			fmt.Fprintln(os.Stdout, "Paired chats: none — run 'watchfire telegram pair'")
			return nil
		}
		fmt.Fprintf(os.Stdout, "Paired chats (%d):\n", len(chats))
		for _, pc := range chats {
			who := pc.GetUsername()
			if who == "" {
				who = "(no username)"
			} else {
				who = "@" + who
			}
			flags := ""
			if pc.GetMuted() {
				flags += " muted"
			}
			if pc.GetWatch() {
				flags += " watch"
			}
			fmt.Fprintf(os.Stdout, "  %d  %s  paired %s%s\n",
				pc.GetChatId(), who, pc.GetPairedAt().AsTime().Local().Format("2006-01-02 15:04"), flags)
		}
		return nil
	},
}

func init() {
	telegramCmd.AddCommand(telegramPairCmd)
	telegramCmd.AddCommand(telegramStatusCmd)
	rootCmd.AddCommand(telegramCmd)
}
