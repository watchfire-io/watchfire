package echo

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// Discord interaction types — only PING and APPLICATION_COMMAND are
// handled. Everything else (button clicks, modal submits, autocomplete)
// is acknowledged with a polite "not supported in v8.0" reply so the
// app stays alive in the user's server even if they wire up extra UI.
//
// https://discord.com/developers/docs/interactions/receiving-and-responding#interaction-object-interaction-type
const (
	discordInteractionPing               = 1
	discordInteractionApplicationCommand = 2
	discordInteractionMessageComponent   = 3
	discordInteractionAutocomplete       = 4
	discordInteractionModalSubmit        = 5
)

// Discord interaction-response types — Watchfire emits PONG (1) for
// pings and CHANNEL_MESSAGE_WITH_SOURCE (4) for everything else.
//
// https://discord.com/developers/docs/interactions/receiving-and-responding#interaction-response-object-interaction-callback-type
const (
	discordResponsePong                       = 1
	discordResponseChannelMessageWithSource   = 4
)

// discordEphemeralFlag is bit 6 (64) in the message-flags bitfield,
// the EPHEMERAL flag. Set on a CHANNEL_MESSAGE_WITH_SOURCE response
// to make the message visible only to the invoking user.
const discordEphemeralFlag = 64

// DiscordHandlerConfig wires the per-request state the handler needs:
// a resolver for the application's Ed25519 public key (resolved
// through the keyring at request time so a key rotation in Settings
// doesn't require a daemon restart), an idempotency cache keyed off
// the interaction id, and a CommandContext factory that fills in the
// guild-scoped FindProjects + LookupTask + ListTopActiveTasks +
// Retry + Cancel callbacks.
//
// The factory pattern (rather than a single CommandContext) lets the
// handler scope FindProjects to the requesting `guild_id` per request
// — the same daemon serving multiple guilds doesn't accidentally leak
// projects across them.
//
// `RefundOnReplay` is the per-IP rate-limit refund callback wired by
// the parent Server when the limiter is enabled. nil = no-op (disables
// the legitimate-retry refund without breaking the handler).
type DiscordHandlerConfig struct {
	ResolvePublicKey   func() (ed25519.PublicKey, error)
	Idempotency        *Cache
	CommandContextFor  func(guildID, userID string) CommandContext
	RefundOnReplay     func(r *http.Request)
	Logger             *log.Logger
}

// NewDiscordHandler returns the http.Handler that lives at
// `POST /echo/discord/interactions`. The handler is purely a
// transport adapter: it verifies the signature, rejects malformed
// inputs with the right HTTP status, parses the interaction body,
// and either responds with PONG (for ping verification) or
// dispatches to the shared `Route` and renders the result.
func NewDiscordHandler(cfg DiscordHandlerConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Idempotency == nil {
		cfg.Idempotency = NewCache(0, 0)
	}
	return &discordHandler{cfg: cfg}
}

type discordHandler struct{ cfg DiscordHandlerConfig }

// discordInteraction is the subset of fields Watchfire reads off the
// interaction body. Discord's full schema is much richer; we only
// need the type, the per-request id (for idempotency), the calling
// guild + user, and — for application commands — the command name +
// option list.
//
// https://discord.com/developers/docs/interactions/receiving-and-responding#interaction-object
type discordInteraction struct {
	ID            string                       `json:"id"`
	ApplicationID string                       `json:"application_id"`
	Type          int                          `json:"type"`
	GuildID       string                       `json:"guild_id"`
	ChannelID     string                       `json:"channel_id"`
	Member        *discordMember               `json:"member"`
	User          *discordUser                 `json:"user"`
	Data          *discordApplicationCommandData `json:"data"`
}

type discordMember struct {
	User *discordUser `json:"user"`
}

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type discordApplicationCommandData struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	Options []discordCommandOption `json:"options"`
}

type discordCommandOption struct {
	Name  string          `json:"name"`
	Type  int             `json:"type"`
	Value json.RawMessage `json:"value"`
}

func (h *discordHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if IsTooLarge(err) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			h.cfg.Logger.Printf("WARN: echo: discord interaction payload too large (>%d bytes)", MaxBodyBytes)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("X-Signature-Ed25519")
	timestamp := r.Header.Get("X-Signature-Timestamp")
	if signature == "" || timestamp == "" {
		http.Error(w, "missing signature headers", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: discord interaction missing signature headers")
		return
	}

	publicKey, err := h.cfg.ResolvePublicKey()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: discord public key not configured: %v", err)
		return
	}

	if vErr := VerifyDiscord(publicKey, []byte(timestamp), body, signature); vErr != nil {
		status := http.StatusUnauthorized
		if errors.Is(vErr, ErrMalformedHeader) {
			status = http.StatusBadRequest
		}
		http.Error(w, vErr.Error(), status)
		h.cfg.Logger.Printf("WARN: echo: discord signature rejected: %v", vErr)
		return
	}

	var interaction discordInteraction
	if err := json.Unmarshal(body, &interaction); err != nil {
		http.Error(w, "malformed interaction", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: discord interaction body malformed: %v", err)
		return
	}

	// Idempotency: Discord re-delivers an interaction up to several
	// times if the initial response is delayed. A re-delivery should
	// produce the same response shape (PONG-equivalent for type 1,
	// ack-with-no-action for type 2) but should not double-act.
	if interaction.ID != "" && h.cfg.Idempotency.Seen(interaction.ID) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		h.cfg.Logger.Printf("INFO: echo: discord interaction %s replayed, returning ack", interaction.ID)
		writeDiscordAck(w)
		return
	}

	switch interaction.Type {
	case discordInteractionPing:
		writeDiscordPong(w)
		return

	case discordInteractionApplicationCommand:
		if interaction.Data == nil {
			http.Error(w, "missing application command data", http.StatusBadRequest)
			return
		}
		userID := ""
		if interaction.Member != nil && interaction.Member.User != nil {
			userID = interaction.Member.User.ID
		} else if interaction.User != nil {
			userID = interaction.User.ID
		}
		cc := h.cfg.CommandContextFor(interaction.GuildID, userID)

		// Discord delivers slash-command args as a structured
		// `options[]` array. The shared router expects a single
		// `rest` string. Flatten by joining option values with
		// spaces — for the three v8.0 commands (status / retry /
		// cancel) only one positional argument exists per command,
		// so the flatten is unambiguous.
		rest := flattenOptions(interaction.Data.Options)

		resp := Route(r.Context(), "/"+interaction.Data.Name, interaction.Data.Name, rest, cc)
		body := RenderInteraction(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		h.cfg.Logger.Printf("INFO: echo: discord command %q from guild=%s user=%s", interaction.Data.Name, interaction.GuildID, userID)
		return

	case discordInteractionMessageComponent,
		discordInteractionAutocomplete,
		discordInteractionModalSubmit:
		writeDiscordEphemeral(w, "Not supported in v8.0 — Watchfire only handles slash commands.")
		h.cfg.Logger.Printf("INFO: echo: discord interaction type %d ignored", interaction.Type)
		return

	default:
		writeDiscordEphemeral(w, "Not supported in v8.0 — Watchfire only handles slash commands.")
		h.cfg.Logger.Printf("WARN: echo: discord interaction type %d unknown", interaction.Type)
		return
	}
}

// writeDiscordPong responds to a Discord PING interaction. Discord's
// endpoint-verification flow at app registration time fires this
// before the app can be saved; failing it disables the app.
func writeDiscordPong(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"type":1}`))
}

// writeDiscordAck is the response for a replayed application-command
// interaction. Returning a no-content ack prevents a double action
// from a duplicate delivery while still confirming receipt.
func writeDiscordAck(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"type":1}`))
}

// writeDiscordEphemeral writes a CHANNEL_MESSAGE_WITH_SOURCE response
// flagged EPHEMERAL with the given content.
func writeDiscordEphemeral(w http.ResponseWriter, content string) {
	body, _ := json.Marshal(map[string]any{
		"type": discordResponseChannelMessageWithSource,
		"data": map[string]any{
			"content": content,
			"flags":   discordEphemeralFlag,
		},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func flattenOptions(opts []discordCommandOption) string {
	if len(opts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(opts))
	for _, opt := range opts {
		// Discord encodes string values with surrounding JSON quotes;
		// numeric values come through unquoted. json.Unmarshal handles
		// either path.
		var s string
		if err := json.Unmarshal(opt.Value, &s); err == nil {
			parts = append(parts, s)
			continue
		}
		parts = append(parts, strings.TrimSpace(string(opt.Value)))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ResolvePublicKeyFromHex builds a `ResolvePublicKey` callback from a
// keyring lookup function and a hex-encoded key resolver. The daemon
// wires this in `internal/daemon/server`; tests inject a closure
// returning a hardcoded key directly.
func ResolvePublicKeyFromHex(fetchHex func() (string, error)) func() (ed25519.PublicKey, error) {
	return func() (ed25519.PublicKey, error) {
		hex, err := fetchHex()
		if err != nil {
			return nil, err
		}
		if hex == "" {
			return nil, fmt.Errorf("discord public key not configured")
		}
		return DecodeDiscordPublicKey(hex)
	}
}
