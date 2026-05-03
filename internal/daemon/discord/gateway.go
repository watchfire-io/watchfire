// Package discord — minimal Discord Gateway client used by the v8.x Echo
// auto-registrar to learn which guilds the bot is in.
//
// Scope is deliberately narrow: open the WebSocket, IDENTIFY with the
// GUILDS intent (1 << 0), watch for READY + GUILD_CREATE / GUILD_DELETE,
// emit them on a channel for the registrar. Heartbeats keep the
// connection alive; on any disconnect we drop the session and IDENTIFY
// fresh — Discord re-emits GUILD_CREATE for every guild on a new
// session, so the registrar still sees the full list and re-registration
// is a no-op (Discord upserts on POST).
//
// We avoid the full discordgo dependency because we need so little of it:
// the entire client lives in this file plus `gateway_dial.go`.
package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

// DefaultGatewayURL is the bare gateway URL Discord publishes for bot
// connections. Bot apps don't need to call `GET /gateway/bot` to discover
// the URL — the documentation guarantees this hostname is canonical and
// the v=10 / encoding=json query is what the rest of v10 expects.
//
// https://discord.com/developers/docs/topics/gateway#connecting
const DefaultGatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

// IntentGuilds is the GUILDS intent (1 << 0). Required for GUILD_CREATE /
// GUILD_DELETE / GUILD_UPDATE dispatches — exactly what the auto-registrar
// needs and nothing else. Not a privileged intent, so the bot doesn't need
// approval in the developer portal.
//
// https://discord.com/developers/docs/topics/gateway#gateway-intents
const IntentGuilds = 1 << 0

// Gateway op codes Watchfire cares about.
const (
	opDispatch     = 0
	opHeartbeat    = 1
	opIdentify     = 2
	opReconnect    = 7
	opInvalidSess  = 9
	opHello        = 10
	opHeartbeatAck = 11
)

// reconnectMin / reconnectMax bound the exponential backoff between
// reconnect attempts. Discord's docs recommend exponential backoff, capped
// at a few minutes; we err on the lower end (max 60s) so users adding the
// bot to a guild see registration well within the 30-second SLA the v8.x
// Echo spec sets.
const (
	reconnectMin = 2 * time.Second
	reconnectMax = 60 * time.Second
)

// GuildEventType distinguishes the two guild-membership transitions the
// registrar reacts to. GUILD_UPDATE is intentionally ignored — the
// registrar keys off id, not name, and Discord re-issues GUILD_CREATE
// whenever the bot is re-added.
type GuildEventType int

const (
	// GuildEventCreate fires both at session start (one per guild the bot
	// is already in) and when the bot is added to a fresh guild.
	GuildEventCreate GuildEventType = iota
	// GuildEventDelete fires when the bot is kicked / banned / leaves.
	GuildEventDelete
)

// GuildEvent is the channel payload the Gateway client emits.
type GuildEvent struct {
	Type      GuildEventType
	GuildID   string
	GuildName string
}

// GatewayDialer is the WebSocket-dial seam tests substitute. Production
// uses `defaultDialer` which forwards to `nhooyr.io/websocket.Dial`.
type GatewayDialer func(ctx context.Context, url string) (GatewayConn, error)

// GatewayConn is the narrow subset of the underlying *websocket.Conn the
// gateway client uses. Defined as an interface so tests can supply a
// fake without spinning up a full TLS WebSocket server.
type GatewayConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, data []byte) error
	Close(code websocket.StatusCode, reason string) error
}

// GatewayConfig parametrises a single Gateway client run.
type GatewayConfig struct {
	URL     string         // empty defaults to DefaultGatewayURL
	Token   string         // bot token (without the "Bot " prefix)
	Intents int            // intent bitfield; defaults to IntentGuilds when 0
	Dialer  GatewayDialer  // empty falls back to nhooyr.io/websocket.Dial
	Logger  *log.Logger    // empty falls back to log.Default()
	// Now is a clock seam for tests. Defaults to time.Now.
	Now func() time.Time
}

// Gateway is the public entry point. Construct one, call Run with a
// cancellable context, and consume the Events channel from another
// goroutine. Run reconnects on its own; the channel only closes when Run
// returns.
type Gateway struct {
	cfg    GatewayConfig
	events chan GuildEvent

	// running flips to true on the first Run call. Used to make
	// double-Run a panic instead of a subtle race.
	running atomic.Bool
}

// NewGateway builds a configured but un-started Gateway.
func NewGateway(cfg GatewayConfig) *Gateway {
	if cfg.URL == "" {
		cfg.URL = DefaultGatewayURL
	}
	if cfg.Intents == 0 {
		cfg.Intents = IntentGuilds
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Dialer == nil {
		cfg.Dialer = defaultDialer
	}
	return &Gateway{
		cfg:    cfg,
		events: make(chan GuildEvent, 32),
	}
}

// Events is the channel the registrar reads. Closed when Run returns.
func (g *Gateway) Events() <-chan GuildEvent { return g.events }

// Run blocks until ctx is cancelled, reconnecting on every disconnect
// with exponential backoff. Returns nil on graceful shutdown (ctx
// cancelled); a non-nil error means the configured token / URL is
// fundamentally invalid (4004 INVALID_TOKEN), in which case the caller
// should not retry without operator action.
func (g *Gateway) Run(ctx context.Context) error {
	if !g.running.CompareAndSwap(false, true) {
		panic("discord.Gateway.Run called twice on the same instance")
	}
	defer close(g.events)

	backoff := reconnectMin
	for {
		err := g.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errInvalidToken) {
			// Hard failure — operator must rotate the token. Surface it
			// up so the daemon can log a single WARN per process.
			g.cfg.Logger.Printf("ERROR: discord gateway: invalid token, giving up")
			return err
		}
		g.cfg.Logger.Printf("WARN: discord gateway: connection ended (%v), reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// errInvalidToken is the sentinel for Discord close-code 4004. Reaching
// this means the configured bot token is wrong; no amount of reconnecting
// will fix it.
var errInvalidToken = errors.New("discord gateway: invalid token")

// runOnce dials the gateway, identifies, and pumps messages until the
// connection ends. Returns nil only when ctx cancels mid-pump; any other
// error is treated as a connection-level failure the caller should
// reconnect from.
func (g *Gateway) runOnce(ctx context.Context) error {
	conn, err := g.cfg.Dialer(ctx, g.cfg.URL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// HELLO — Discord sends this immediately after the WebSocket opens.
	hello, err := readGatewayMessage(ctx, conn)
	if err != nil {
		return fmt.Errorf("read HELLO: %w", err)
	}
	if hello.Op != opHello {
		return fmt.Errorf("expected HELLO (op 10), got op %d", hello.Op)
	}
	var helloData struct {
		HeartbeatIntervalMs int `json:"heartbeat_interval"`
	}
	if jsonErr := json.Unmarshal(hello.Data, &helloData); jsonErr != nil {
		return fmt.Errorf("decode HELLO: %w", jsonErr)
	}
	if helloData.HeartbeatIntervalMs <= 0 {
		return fmt.Errorf("HELLO had non-positive heartbeat_interval: %d", helloData.HeartbeatIntervalMs)
	}

	// IDENTIFY — needs to fly before the first heartbeat so Discord
	// doesn't drop the connection with 4003 NOT_AUTHENTICATED.
	if identifyErr := g.sendIdentify(ctx, conn); identifyErr != nil {
		return fmt.Errorf("send IDENTIFY: %w", identifyErr)
	}

	// Sequence is updated by the read loop and consumed by the heartbeat
	// goroutine. atomic.Int64 keeps both lockless.
	var sequence atomic.Int64
	sequence.Store(-1)

	// Heartbeat goroutine — fires until heartbeatCtx is cancelled, which
	// happens when the read loop exits.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Duration(helloData.HeartbeatIntervalMs) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				seq := sequence.Load()
				var d any
				if seq >= 0 {
					d = seq
				}
				payload, _ := json.Marshal(gatewayOutgoing{Op: opHeartbeat, Data: d})
				if writeErr := conn.Write(heartbeatCtx, websocket.MessageText, payload); writeErr != nil {
					// Read loop will surface the disconnect.
					return
				}
			}
		}
	}()
	defer func() { <-heartbeatDone }()

	// Read loop — pump messages, fan out events, return on disconnect.
	for {
		msg, err := readGatewayMessage(ctx, conn)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if isInvalidTokenClose(err) {
				return errInvalidToken
			}
			return fmt.Errorf("read: %w", err)
		}
		if msg.Sequence != nil {
			sequence.Store(int64(*msg.Sequence))
		}
		switch msg.Op {
		case opDispatch:
			if dispatchErr := g.handleDispatch(ctx, msg); dispatchErr != nil {
				g.cfg.Logger.Printf("WARN: discord gateway: dispatch %s: %v", msg.Type, dispatchErr)
			}
		case opHeartbeatAck:
			// Healthy — nothing to do.
		case opReconnect:
			// Discord asked us to reconnect. Returning here cycles back
			// through Run's reconnect loop with fresh backoff.
			return errors.New("server requested reconnect (op 7)")
		case opInvalidSess:
			// Re-IDENTIFY by reconnecting. Spec says wait 1-5s before
			// reconnecting; the runOnce backoff handles the sleep.
			return errors.New("invalid session (op 9)")
		}
	}
}

// sendIdentify constructs the op-2 IDENTIFY payload and writes it.
func (g *Gateway) sendIdentify(ctx context.Context, conn GatewayConn) error {
	identify := struct {
		Op   int `json:"op"`
		Data struct {
			Token      string `json:"token"`
			Intents    int    `json:"intents"`
			Properties struct {
				OS      string `json:"os"`
				Browser string `json:"browser"`
				Device  string `json:"device"`
			} `json:"properties"`
		} `json:"d"`
	}{Op: opIdentify}
	identify.Data.Token = g.cfg.Token
	identify.Data.Intents = g.cfg.Intents
	identify.Data.Properties.OS = "linux"
	identify.Data.Properties.Browser = "watchfire"
	identify.Data.Properties.Device = "watchfire"
	payload, err := json.Marshal(identify)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

// handleDispatch routes the events the registrar cares about. READY is
// logged at info; GUILD_CREATE / GUILD_DELETE go on the events channel.
func (g *Gateway) handleDispatch(ctx context.Context, msg gatewayIncoming) error {
	switch msg.Type {
	case "READY":
		// READY's full payload is huge (entire user object, application,
		// shard); we only log that the session is up.
		g.cfg.Logger.Printf("INFO: discord gateway: session ready, awaiting GUILD_CREATE")
		return nil
	case "GUILD_CREATE":
		var d struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Unavailable bool   `json:"unavailable"`
		}
		if err := json.Unmarshal(msg.Data, &d); err != nil {
			return fmt.Errorf("decode GUILD_CREATE: %w", err)
		}
		if d.Unavailable {
			// Outage — Discord will re-emit when the guild comes back up.
			return nil
		}
		select {
		case g.events <- GuildEvent{Type: GuildEventCreate, GuildID: d.ID, GuildName: d.Name}:
		case <-ctx.Done():
		}
		return nil
	case "GUILD_DELETE":
		var d struct {
			ID          string `json:"id"`
			Unavailable bool   `json:"unavailable"`
		}
		if err := json.Unmarshal(msg.Data, &d); err != nil {
			return fmt.Errorf("decode GUILD_DELETE: %w", err)
		}
		if d.Unavailable {
			// Transient outage, not a real removal — keep tracking.
			return nil
		}
		select {
		case g.events <- GuildEvent{Type: GuildEventDelete, GuildID: d.ID}:
		case <-ctx.Done():
		}
		return nil
	}
	return nil
}

// gatewayIncoming is the wire shape of inbound messages. `Data` stays raw
// so each dispatch handler can decode the bits it needs without a giant
// shared struct.
type gatewayIncoming struct {
	Op       int             `json:"op"`
	Data     json.RawMessage `json:"d"`
	Sequence *int            `json:"s"`
	Type     string          `json:"t"`
}

// gatewayOutgoing is the wire shape of outbound messages.
type gatewayOutgoing struct {
	Op   int `json:"op"`
	Data any `json:"d"`
}

// readGatewayMessage reads + parses one JSON frame from the connection.
// Discord only sends text frames; binary (zlib-stream / etf) requires
// extra negotiation we don't enable.
func readGatewayMessage(ctx context.Context, conn GatewayConn) (gatewayIncoming, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return gatewayIncoming{}, err
	}
	if typ != websocket.MessageText {
		return gatewayIncoming{}, fmt.Errorf("unexpected non-text frame: %v", typ)
	}
	var msg gatewayIncoming
	if jsonErr := json.Unmarshal(data, &msg); jsonErr != nil {
		// Treat a malformed frame as a fatal connection error so
		// runOnce reconnects fresh.
		return gatewayIncoming{}, fmt.Errorf("decode: %w (%s)", jsonErr, truncateForLog(data))
	}
	return msg, nil
}

// isInvalidTokenClose reports whether err carries Discord close-code 4004.
// 4004 means the bot token is wrong — there's no point reconnecting.
func isInvalidTokenClose(err error) bool {
	if err == nil {
		return false
	}
	return websocket.CloseStatus(err) == 4004
}

// truncateForLog clips byte slices for log lines so a 1 MiB malformed
// frame doesn't blow up the log file.
func truncateForLog(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// defaultDialer is the production GatewayDialer — wraps
// `nhooyr.io/websocket.Dial`.
func defaultDialer(ctx context.Context, url string) (GatewayConn, error) {
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{})
	if err != nil {
		return nil, err
	}
	// Discord can send frames up to ~4 MiB on guilds with many members;
	// raise the read limit from the 32 KiB default. We don't enable
	// compression (would require zlib-stream / payload negotiation).
	conn.SetReadLimit(8 << 20)
	return &nhooyrConn{Conn: conn}, nil
}

// nhooyrConn adapts *websocket.Conn to the GatewayConn interface, mainly
// so the `Read` signature returns `(MessageType, []byte, error)` from the
// helper that allocates the byte slice itself.
type nhooyrConn struct{ Conn *websocket.Conn }

func (n *nhooyrConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	typ, r, err := n.Conn.Reader(ctx)
	if err != nil {
		return 0, nil, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, nil, err
	}
	return typ, data, nil
}

func (n *nhooyrConn) Write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	return n.Conn.Write(ctx, typ, data)
}

func (n *nhooyrConn) Close(code websocket.StatusCode, reason string) error {
	return n.Conn.Close(code, reason)
}
