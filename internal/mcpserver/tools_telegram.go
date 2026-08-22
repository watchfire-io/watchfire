// Telegram bridge control (v10.1 Torch). The bridge is Watchfire's
// phone surface: a paired chat can talk to a project's chat agent, watch
// a run stream, switch modes and re-authenticate Claude. These tools let
// an MCP client set that up and inspect it without leaving the session —
// the same operations `watchfire telegram` and the GUI/TUI Integrations
// panel perform, translated to the existing IntegrationsService RPCs.
//
// Registry groups follow the read-only invariant (TestToolAnnotationsMatchGroups):
// telegram_status is pure observation and carries groupInspect so
// --read-only keeps serving it; the three mutating tools carry
// groupTelegram and disappear entirely under --read-only.
package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/watchfire-io/watchfire/proto"
)

// mcpOrigin tags daemon requests as coming from the MCP surface, the
// same marker the run tools use (tools_run.go).
const mcpOrigin = "mcp"

var telegramTools = []toolDef{
	// Registry group "inspect", not "telegram": status is pure
	// observation, so --read-only serving must keep it.
	newTool(toolSpec{
		Group: groupInspect, Name: "telegram_status", Title: "Telegram bridge status",
		ReadOnly: true, Idempotent: true,
		Description: "Show the Telegram bridge's state: whether the long-poll bridge is running, whether the integration is enabled, whether a bot token is stored, the bot's @username, the state of any in-flight pairing, and every paired chat (chat id, username, selected project, muted/watch flags). Start here before telegram_configure or telegram_pair — it tells you which step is actually missing.",
	}, handleTelegramStatus),
	newTool(toolSpec{
		Group: groupTelegram, Name: "telegram_configure", Title: "Configure Telegram bridge",
		Description: "Enable or disable the Telegram bridge, and optionally store the bot token from @BotFather. SECURITY: a token passed here travels through the conversation and is retained in the transcript — prefer setting it once in the GUI/TUI (Settings -> Integrations -> Telegram), then using this tool only to flip \"enabled\". Omitting bot_token keeps the stored one (it is write-only and never returned). Enabling with no token stored fails. The bridge starts on the next daemon restart if it was not already running — check telegram_status.",
	}, handleTelegramConfigure),
	newTool(toolSpec{
		Group: groupTelegram, Name: "telegram_pair", Title: "Pair a Telegram chat",
		Description: "Mint a one-time pairing code and return it with a deep link. Pairing is the bridge's ONLY security boundary: anyone can message a Telegram bot, so a chat sees project data only after redeeming a code. Give the user the deep link (or have them send \"/pair <code>\" to the bot); the code is single-use and expires in 10 minutes. This returns immediately without waiting — poll telegram_status until pairing_state becomes \"paired\". Requires the bridge to be running.",
	}, handleTelegramPair),
	newTool(toolSpec{
		Group: groupTelegram, Name: "telegram_unpair", Title: "Unpair a Telegram chat",
		Destructive: true, Idempotent: true,
		Description: "Remove one chat from the paired allowlist by chat_id (see telegram_status). That chat immediately stops receiving project data and its commands are refused; re-pairing requires a fresh code. Use this when a device is lost or a chat should no longer have access.",
	}, handleTelegramUnpair),
}

// ---------------------------------------------------------------------------
// telegram_status

type telegramStatusArgs struct{}

type telegramChat struct {
	ChatID           int64  `json:"chat_id"`
	Username         string `json:"username,omitempty"`
	PairedAt         string `json:"paired_at,omitempty"`
	DefaultProjectID string `json:"default_project_id,omitempty"`
	Muted            bool   `json:"muted"`
	Watch            bool   `json:"watch"`
}

type telegramStatusResult struct {
	BridgeRunning bool           `json:"bridge_running"`
	Configured    bool           `json:"configured"`
	Enabled       bool           `json:"enabled"`
	TokenSet      bool           `json:"token_set"`
	BotUsername   string         `json:"bot_username,omitempty"`
	PairingState  string         `json:"pairing_state"`
	PairingExpiry string         `json:"pairing_expires_at,omitempty"`
	PairedChats   []telegramChat `json:"paired_chats"`
	NextStep      string         `json:"next_step,omitempty"`
}

// pairingStateName renders the proto enum as the lowercase word the tool
// contract documents, so a model never has to know the enum numbering.
func pairingStateName(s pb.TelegramPairingState) string {
	switch s {
	case pb.TelegramPairingState_TELEGRAM_PAIRING_PENDING:
		return "pending"
	case pb.TelegramPairingState_TELEGRAM_PAIRING_PAIRED:
		return "paired"
	case pb.TelegramPairingState_TELEGRAM_PAIRING_EXPIRED:
		return "expired"
	default:
		return "none"
	}
}

// telegramNextStep names the one action that actually unblocks setup, so
// the model does not have to infer it from four booleans.
func telegramNextStep(r telegramStatusResult) string {
	switch {
	case !r.Configured || !r.TokenSet:
		return "No bot token stored. Create a bot with @BotFather, then call telegram_configure with bot_token (or set it in the GUI/TUI: Settings -> Integrations -> Telegram)."
	case !r.Enabled:
		return "Token is stored but the integration is disabled. Call telegram_configure with enabled: true."
	case !r.BridgeRunning:
		return "Enabled with a token, but the bridge is not running. Restart the daemon (watchfire daemon stop && watchfire daemon start)."
	case len(r.PairedChats) == 0:
		return "Bridge is running with no paired chats. Call telegram_pair and send the user the deep link."
	default:
		return ""
	}
}

func handleTelegramStatus(ctx context.Context, s *server, _ telegramStatusArgs) (any, error) {
	st, err := s.integrations.GetTelegramPairingStatus(ctx, &pb.GetTelegramPairingStatusRequest{})
	if err != nil {
		return nil, rpcErr("get the Telegram pairing status", err)
	}
	cfg, err := s.integrations.ListIntegrations(ctx, &pb.ListIntegrationsRequest{})
	if err != nil {
		return nil, rpcErr("list integrations", err)
	}

	res := telegramStatusResult{
		BridgeRunning: st.GetBridgeRunning(),
		BotUsername:   st.GetBotUsername(),
		PairingState:  pairingStateName(st.GetState()),
		PairedChats:   []telegramChat{},
	}
	if st.GetState() == pb.TelegramPairingState_TELEGRAM_PAIRING_PENDING && st.GetExpiresAt() != nil {
		res.PairingExpiry = st.GetExpiresAt().AsTime().Format(time.RFC3339)
	}
	if tg := cfg.GetTelegram(); tg != nil {
		res.Configured = true
		res.Enabled = tg.GetEnabled()
		res.TokenSet = tg.GetTokenSet()
		for _, c := range tg.GetPairedChats() {
			row := telegramChat{
				ChatID:           c.GetChatId(),
				Username:         c.GetUsername(),
				DefaultProjectID: c.GetDefaultProjectId(),
				Muted:            c.GetMuted(),
				Watch:            c.GetWatch(),
			}
			if c.GetPairedAt() != nil {
				row.PairedAt = c.GetPairedAt().AsTime().Format(time.RFC3339)
			}
			res.PairedChats = append(res.PairedChats, row)
		}
	}
	res.NextStep = telegramNextStep(res)
	return res, nil
}

// ---------------------------------------------------------------------------
// telegram_configure

type telegramConfigureArgs struct {
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"Enable (true) or disable (false) the Telegram bridge. Omit to leave the current setting untouched."`
	BotToken string `json:"bot_token,omitempty" jsonschema:"Bot token from @BotFather. Stored write-only in the OS keyring and never returned. Omit to keep the token already stored. Warning: a token passed here is retained in the conversation transcript."`
}

type telegramConfigureResult struct {
	Enabled  bool   `json:"enabled"`
	TokenSet bool   `json:"token_set"`
	Note     string `json:"note,omitempty"`
}

func handleTelegramConfigure(ctx context.Context, s *server, args telegramConfigureArgs) (any, error) {
	token := strings.TrimSpace(args.BotToken)
	if args.Enabled == nil && token == "" {
		return nil, fmt.Errorf("nothing to change: pass \"enabled\" and/or \"bot_token\"")
	}

	// Read current state first: SaveIntegration replaces the whole
	// Telegram document, so an unspecified field must be carried over
	// rather than reset to its zero value.
	cfg, err := s.integrations.ListIntegrations(ctx, &pb.ListIntegrationsRequest{})
	if err != nil {
		return nil, rpcErr("list integrations", err)
	}
	cur := cfg.GetTelegram()

	enabled := cur.GetEnabled()
	if args.Enabled != nil {
		enabled = *args.Enabled
	}
	if enabled && token == "" && !cur.GetTokenSet() {
		return nil, fmt.Errorf("cannot enable the Telegram bridge: no bot token is stored — pass \"bot_token\" (from @BotFather) in this call, or set it in the GUI/TUI under Settings -> Integrations -> Telegram")
	}

	payload := &pb.TelegramIntegration{
		Enabled:       enabled,
		BotToken:      token, // empty means "keep the stored token"
		EnabledEvents: cur.GetEnabledEvents(),
	}
	saved, err := s.integrations.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Meta:    &pb.RequestMeta{Origin: mcpOrigin},
		Payload: &pb.SaveIntegrationRequest_Telegram{Telegram: payload},
	})
	if err != nil {
		return nil, rpcErr("save the Telegram integration", err)
	}

	tg := saved.GetTelegram()
	res := telegramConfigureResult{Enabled: tg.GetEnabled(), TokenSet: tg.GetTokenSet()}
	if tg.GetEnabled() {
		res.Note = "Saved. The bridge's long-poll loop starts with the daemon — if telegram_status still reports bridge_running: false, restart the daemon."
	} else {
		res.Note = "Telegram bridge disabled. Paired chats are kept and reactivate when it is enabled again."
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// telegram_pair

type telegramPairArgs struct{}

type telegramPairResult struct {
	Code        string `json:"code"`
	DeepLink    string `json:"deep_link"`
	BotUsername string `json:"bot_username,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	NextStep    string `json:"next_step"`
}

func handleTelegramPair(ctx context.Context, s *server, _ telegramPairArgs) (any, error) {
	begin, err := s.integrations.BeginTelegramPairing(ctx, &pb.BeginTelegramPairingRequest{
		Meta: &pb.RequestMeta{Origin: mcpOrigin},
	})
	if err != nil {
		return nil, rpcErr("begin Telegram pairing (the bridge must be running — check telegram_status)", err)
	}
	res := telegramPairResult{
		Code:        begin.GetCode(),
		DeepLink:    begin.GetDeepLink(),
		BotUsername: begin.GetBotUsername(),
		NextStep:    "Give the user the deep link (or tell them to send \"/pair " + begin.GetCode() + "\" to the bot), then poll telegram_status until pairing_state is \"paired\".",
	}
	if begin.GetExpiresAt() != nil {
		res.ExpiresAt = begin.GetExpiresAt().AsTime().Format(time.RFC3339)
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// telegram_unpair

type telegramUnpairArgs struct {
	ChatID int64 `json:"chat_id" jsonschema:"Telegram chat id to remove from the allowlist (see telegram_status paired_chats)."`
}

type telegramUnpairResult struct {
	ChatID    int64          `json:"chat_id"`
	Unpaired  bool           `json:"unpaired"`
	Remaining []telegramChat `json:"remaining_chats"`
}

func handleTelegramUnpair(ctx context.Context, s *server, args telegramUnpairArgs) (any, error) {
	if args.ChatID == 0 {
		return nil, fmt.Errorf("\"chat_id\" is required — list the paired chats with telegram_status")
	}
	cfg, err := s.integrations.RevokeTelegramChat(ctx, &pb.RevokeTelegramChatRequest{
		Meta:   &pb.RequestMeta{Origin: mcpOrigin},
		ChatId: args.ChatID,
	})
	if err != nil {
		return nil, rpcErr("revoke the Telegram chat", err)
	}
	res := telegramUnpairResult{ChatID: args.ChatID, Unpaired: true, Remaining: []telegramChat{}}
	for _, c := range cfg.GetTelegram().GetPairedChats() {
		res.Remaining = append(res.Remaining, telegramChat{
			ChatID:           c.GetChatId(),
			Username:         c.GetUsername(),
			DefaultProjectID: c.GetDefaultProjectId(),
			Muted:            c.GetMuted(),
			Watch:            c.GetWatch(),
		})
	}
	return res, nil
}
