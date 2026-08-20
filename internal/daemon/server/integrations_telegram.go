package server

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/telegram"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// v10.0 Torch (task 0136) — Telegram pairing RPCs. Pairing is the
// bridge's security boundary: these handlers mint the one-time code the
// bridge redeems, surface the pairing lifecycle to polling clients, and
// revoke chats off the allowlist.

// BeginTelegramPairing mints a fresh one-time pairing code (invalidating
// any prior code) and returns it with the t.me deep link. Requires the
// bridge to be running — without a live getUpdates loop the code could
// never be redeemed.
func (s *integrationsService) BeginTelegramPairing(ctx context.Context, _ *pb.BeginTelegramPairingRequest) (*pb.BeginTelegramPairingResponse, error) {
	bridge, pairing := s.telegramState()
	if bridge == nil || pairing == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"telegram bridge is not running — enable Telegram and store a bot token first (Settings → Integrations → Telegram)")
	}
	username, err := bridge.BotUsername(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve bot username via getMe: %w", err)
	}
	code, expiresAt, err := pairing.Begin(telegram.DefaultTTL)
	if err != nil {
		return nil, err
	}
	return &pb.BeginTelegramPairingResponse{
		Code:        code,
		ExpiresAt:   timestamppb.New(expiresAt),
		DeepLink:    fmt.Sprintf("https://t.me/%s?start=%s", username, code),
		BotUsername: username,
	}, nil
}

// GetTelegramPairingStatus reports the current pairing lifecycle plus
// bridge liveness. Cheap enough to poll — no network calls (the bot
// username is served from the bridge's getMe cache only).
func (s *integrationsService) GetTelegramPairingStatus(_ context.Context, _ *pb.GetTelegramPairingStatusRequest) (*pb.TelegramPairingStatus, error) {
	bridge, pairing := s.telegramState()
	out := &pb.TelegramPairingStatus{
		State:         pb.TelegramPairingState_TELEGRAM_PAIRING_NONE,
		BridgeRunning: bridge != nil,
	}
	if bridge != nil {
		out.BotUsername = bridge.CachedBotUsername()
	}
	if pairing == nil {
		return out, nil
	}
	st := pairing.Status()
	switch st.State {
	case telegram.StatePending:
		out.State = pb.TelegramPairingState_TELEGRAM_PAIRING_PENDING
		out.ExpiresAt = timestamppb.New(st.ExpiresAt)
	case telegram.StatePaired:
		out.State = pb.TelegramPairingState_TELEGRAM_PAIRING_PAIRED
		if st.Chat != nil {
			out.Chat = pairedChatToProto(*st.Chat)
		}
	case telegram.StateExpired:
		out.State = pb.TelegramPairingState_TELEGRAM_PAIRING_EXPIRED
	}
	return out, nil
}

// RevokeTelegramChat removes a chat from the paired allowlist, persists
// the removal, and drops the chat from the live bridge immediately.
func (s *integrationsService) RevokeTelegramChat(_ context.Context, req *pb.RevokeTelegramChatRequest) (*pb.IntegrationsConfig, error) {
	chatID := req.GetChatId()
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}
	if cfg.Telegram == nil {
		return nil, status.Error(codes.NotFound, "telegram is not configured")
	}
	kept := cfg.Telegram.PairedChats[:0]
	found := false
	for _, pc := range cfg.Telegram.PairedChats {
		if pc.ChatID == chatID {
			found = true
			continue
		}
		kept = append(kept, pc)
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "chat %d is not paired", chatID)
	}
	cfg.Telegram.PairedChats = kept
	if err := config.SaveIntegrations(cfg); err != nil {
		return nil, fmt.Errorf("save integrations: %w", err)
	}
	if bridge, _ := s.telegramState(); bridge != nil {
		bridge.Revoke(chatID)
	}
	cfg, err = config.LoadIntegrations()
	if err != nil {
		return nil, err
	}
	return scrubConfigToProto(cfg), nil
}

// telegramState nil-safely fetches the live bridge + pairing manager
// from the bound parent server.
func (s *integrationsService) telegramState() (*telegram.Bridge, *telegram.Pairing) {
	if s.server == nil {
		return nil, nil
	}
	return s.server.TelegramBridge(), s.server.TelegramPairing()
}

func pairedChatToProto(pc models.TelegramPairedChat) *pb.TelegramPairedChatInfo {
	return &pb.TelegramPairedChatInfo{
		ChatId:           pc.ChatID,
		Username:         pc.Username,
		PairedAt:         timestamppb.New(pc.PairedAt),
		DefaultProjectId: pc.DefaultProjectID,
		Muted:            pc.Muted,
		Watch:            pc.Watching(),
	}
}
