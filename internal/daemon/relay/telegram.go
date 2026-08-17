package relay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
	"github.com/watchfire-io/watchfire/internal/models"
)

// telegramDigestSnippetRunes caps how much of the weekly-digest body is
// inlined into the Telegram message. Telegram's hard sendMessage limit
// is 4096 characters; 800 runes of body (matching the Slack digest
// snippet) leaves generous headroom for the headline + context lines
// even after HTML entity escaping inflates the byte count.
const telegramDigestSnippetRunes = 800

// telegramHTMLEscaper escapes exactly the three characters Telegram's
// HTML parse mode requires (&, <, >). Mirrors the inbound renderer in
// internal/daemon/telegram — kept local so the relay package does not
// grow a dependency on the bridge/echo stack for three replacements.
var telegramHTMLEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// telegramEscape escapes user-controlled text for parse_mode=HTML.
func telegramEscape(s string) string { return telegramHTMLEscaper.Replace(s) }

// TelegramAdapter delivers v7.0 Relay notifications to every paired
// Telegram chat via the Bot API (v10.0 Torch). Unlike Slack/Discord —
// where one adapter binds one webhook URL — Telegram is single-instance:
// one bot token, fan-out to every non-muted `TelegramPairedChat`. The
// dispatcher's retry + circuit-breaker policy applies to the aggregate:
// any per-chat failure surfaces from Send so the whole fan-out is
// retried (Telegram sendMessage is not idempotent, but the alternative
// — swallowing partial failures — silently drops notifications).
type TelegramAdapter struct {
	cfg    models.TelegramConfig
	client *telegrambot.Client
	logger *log.Logger
}

// NewTelegramAdapter builds an adapter over the shared telegrambot
// client. Client and logger fall back to sane defaults so production
// callers can pass nil.
func NewTelegramAdapter(cfg models.TelegramConfig, client *telegrambot.Client, logger *log.Logger) *TelegramAdapter {
	if client == nil {
		client = telegrambot.New()
	}
	if logger == nil {
		logger = log.Default()
	}
	return &TelegramAdapter{cfg: cfg, client: client, logger: logger}
}

// ID returns the fixed adapter id — Telegram is single-instance, so a
// constant id is stable across reloads (keeps circuit-breaker state
// attached to "the Telegram bridge" rather than churning per rebuild).
func (t *TelegramAdapter) ID() string { return "telegram" }

// Kind reports the adapter kind for the dispatcher's per-kind routing.
func (t *TelegramAdapter) Kind() string { return "telegram" }

// Supports gates the adapter on the config's event bitmask. The
// dispatcher skips Send entirely (no connection opened) when this
// returns false.
func (t *TelegramAdapter) Supports(kind notify.Kind) bool {
	switch kind {
	case notify.KindTaskFailed:
		return t.cfg.EnabledEvents.TaskFailed
	case notify.KindRunComplete:
		return t.cfg.EnabledEvents.RunComplete
	case notify.KindWeeklyDigest:
		return t.cfg.EnabledEvents.WeeklyDigest
	}
	return false
}

// Send formats the payload once and fans it out to every paired chat
// whose global Muted flag is unset. Per-chat failures are aggregated
// into one returned error so the dispatcher's retry + circuit-breaker
// governs delivery; a fully successful fan-out returns nil.
func (t *TelegramAdapter) Send(ctx context.Context, p Payload) error {
	if t.cfg.BotToken == "" {
		return fmt.Errorf("telegram adapter: bot token not resolved (keyring miss?)")
	}
	text, err := FormatTelegramMessage(p)
	if err != nil {
		return err
	}
	var errs []error
	for _, chat := range t.cfg.PairedChats {
		if chat.Muted {
			continue
		}
		if _, sendErr := t.client.SendMessage(ctx, t.cfg.BotToken, chat.ChatID, text); sendErr != nil {
			errs = append(errs, fmt.Errorf("telegram adapter: chat %d: %w", chat.ChatID, sendErr))
		}
	}
	return errors.Join(errs...)
}

// SendToChat delivers the payload to a single chat, bypassing the mute
// check. Used by the TestIntegration handler so it can report per-chat
// success instead of Send's aggregate.
func (t *TelegramAdapter) SendToChat(ctx context.Context, chatID int64, p Payload) error {
	if t.cfg.BotToken == "" {
		return fmt.Errorf("telegram adapter: bot token not resolved (keyring miss?)")
	}
	text, err := FormatTelegramMessage(p)
	if err != nil {
		return err
	}
	if _, sendErr := t.client.SendMessage(ctx, t.cfg.BotToken, chatID, text); sendErr != nil {
		return fmt.Errorf("telegram adapter: chat %d: %w", chatID, sendErr)
	}
	return nil
}

// FormatTelegramMessage renders the canonical Payload as Telegram HTML
// (parse_mode=HTML). All user-controlled fields (project name, task
// title, failure reason, digest body) are entity-escaped; the weekly
// digest body is truncated well below Telegram's 4096-character cap.
// Exported so the daemon's TestIntegration handler renders the exact
// wire text the dispatcher would send.
func FormatTelegramMessage(p Payload) (string, error) {
	switch notify.Kind(p.Kind) {
	case notify.KindTaskFailed:
		return strings.Join([]string{
			fmt.Sprintf("🚨 <b>Task failed — %s</b>", telegramEscape(p.ProjectName)),
			fmt.Sprintf("<b>Task #%04d</b>: %s", p.TaskNumber, telegramEscape(p.TaskTitle)),
			fmt.Sprintf("<b>Reason</b>: %s", telegramEscape(p.TaskFailureReason)),
			fmt.Sprintf("<i>%s · %s</i>", telegramEscape(p.ProjectName), rfc3339(p.EmittedAt)),
		}, "\n"), nil
	case notify.KindRunComplete:
		// The canonical Payload carries the run's final task (number +
		// title) as its window summary — same shape the Slack/Discord
		// templates render for RUN_COMPLETE.
		return strings.Join([]string{
			fmt.Sprintf("✅ <b>Run complete — %s</b>", telegramEscape(p.ProjectName)),
			fmt.Sprintf("<b>Task #%04d</b>: %s", p.TaskNumber, telegramEscape(p.TaskTitle)),
			fmt.Sprintf("<i>%s · %s</i>", telegramEscape(p.ProjectName), rfc3339(p.EmittedAt)),
		}, "\n"), nil
	case notify.KindWeeklyDigest:
		headline := "📊 <b>Watchfire — your week</b>"
		if p.DigestDate != "" {
			headline = fmt.Sprintf("📊 <b>Watchfire — your week (%s)</b>", telegramEscape(p.DigestDate))
		}
		lines := []string{headline}
		if body := digestSnippet(p.DigestBody, telegramDigestSnippetRunes); body != "" {
			lines = append(lines, telegramEscape(body))
		}
		lines = append(lines, fmt.Sprintf("<i>Weekly digest · %s</i>", rfc3339(p.EmittedAt)))
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("telegram adapter: unsupported notification kind %q", p.Kind)
}

// Compile-time assertion that TelegramAdapter satisfies the Adapter
// interface — the dispatcher iterates `[]Adapter` so this catches
// accidental signature drift at build time.
var _ Adapter = (*TelegramAdapter)(nil)
