// Package discord — auto-registrar that watches Gateway guild events and
// POSTs the Watchfire slash-command roster against each new guild.
//
// The registrar is the daemon-side counterpart to the manual
// `watchfire integrations register-discord <guild>` CLI. v8.x Echo lets
// the user opt out of the CLI step entirely: the registrar enumerates
// every guild the bot is in (Discord re-emits GUILD_CREATE for each one
// at session start) and registers the same three commands.
//
// Idempotency is delegated to Discord — POST upserts on command name —
// so re-running registration on a guild we already handled is a cheap
// no-op rather than a duplicate.
package discord

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"
)

// GuildStatus is the per-guild registration record the gRPC layer
// surfaces in InboundStatus. Names are user-facing — Settings UI lists
// them as "guildname (guild_id) ✓ registered Xs ago".
type GuildStatus struct {
	GuildID      string
	GuildName    string
	Registered   bool
	Error        string
	RegisteredAt time.Time
}

// RegistrarConfig parametrises the auto-registrar. Most fields are
// injected so tests can substitute fakes.
type RegistrarConfig struct {
	// Gateway is the source of guild events. The registrar drains its
	// Events channel until it closes.
	Gateway *Gateway

	// AppID + Token are the bot identity. AppID is the Discord
	// application id; Token is the same bot token Gateway IDENTIFY uses.
	AppID string
	Token string

	// CommandsBase is the REST base URL (e.g. "https://discord.com/api/v10").
	// Empty falls back to CommandsAPIBase.
	CommandsBase string

	// HTTPClient is injected for tests; production passes nil to use the
	// 10s-timeout default RegisterGuildCommands sets up.
	HTTPClient *http.Client

	// Commands is the roster to register. Empty falls back to
	// WatchfireSlashCommands.
	Commands []SlashCommand

	// Logger receives WARN / INFO lines.
	Logger Logger

	// Now is the clock seam. Defaults to time.Now.
	Now func() time.Time
}

// Logger is the narrow log surface the registrar uses; satisfied by
// `*log.Logger` and any compatible wrapper.
type Logger interface {
	Printf(format string, args ...any)
}

// Registrar is the daemon-side auto-registration orchestrator. Construct
// one, call Run, and read GuildStatuses from any goroutine for the gRPC
// status response.
type Registrar struct {
	cfg RegistrarConfig

	mu       sync.RWMutex
	statuses map[string]GuildStatus
}

// NewRegistrar builds a configured registrar. Callers usually pair it with
// a Gateway constructed from the same bot token.
func NewRegistrar(cfg RegistrarConfig) *Registrar {
	if cfg.CommandsBase == "" {
		cfg.CommandsBase = CommandsAPIBase
	}
	if len(cfg.Commands) == 0 {
		cfg.Commands = WatchfireSlashCommands()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Registrar{
		cfg:      cfg,
		statuses: make(map[string]GuildStatus),
	}
}

// Statuses returns a sorted snapshot for the gRPC response. Sort order is
// guild_id ascending — stable across calls so the UI doesn't shuffle.
func (r *Registrar) Statuses() []GuildStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]GuildStatus, 0, len(r.statuses))
	for _, s := range r.statuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GuildID < out[j].GuildID })
	return out
}

// Status returns the snapshot for a single guild, or zero if the
// registrar hasn't seen it.
func (r *Registrar) Status(guildID string) (GuildStatus, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.statuses[guildID]
	return s, ok
}

// Run blocks until the gateway's events channel closes (which happens
// when the gateway's Run returns — usually because ctx cancelled).
//
// For every GuildEventCreate, we POST the roster. For every
// GuildEventDelete, we drop the guild from the status snapshot so the
// Settings UI stops showing kicked guilds.
func (r *Registrar) Run(ctx context.Context) {
	if r.cfg.Gateway == nil {
		return
	}
	for ev := range r.cfg.Gateway.Events() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		switch ev.Type {
		case GuildEventCreate:
			r.handleCreate(ctx, ev)
		case GuildEventDelete:
			r.handleDelete(ev)
		}
	}
}

// handleCreate runs the per-guild registration. Failures land in the
// status map with the error message; a single ERR-level log line records
// the failure for the daemon log.
func (r *Registrar) handleCreate(ctx context.Context, ev GuildEvent) {
	results, err := RegisterGuildCommands(ctx, r.cfg.HTTPClient, r.cfg.CommandsBase, r.cfg.AppID, ev.GuildID, r.cfg.Token, r.cfg.Commands)
	if err != nil {
		r.recordStatus(GuildStatus{
			GuildID:      ev.GuildID,
			GuildName:    ev.GuildName,
			Registered:   false,
			Error:        err.Error(),
			RegisteredAt: r.cfg.Now(),
		})
		r.logf("ERROR: discord registrar: register guild %s: %v", ev.GuildID, err)
		return
	}
	if !AllOK(results) {
		errMsg := FirstError(results)
		r.recordStatus(GuildStatus{
			GuildID:      ev.GuildID,
			GuildName:    ev.GuildName,
			Registered:   false,
			Error:        errMsg,
			RegisteredAt: r.cfg.Now(),
		})
		r.logf("WARN: discord registrar: register guild %s: %s", ev.GuildID, errMsg)
		return
	}
	r.recordStatus(GuildStatus{
		GuildID:      ev.GuildID,
		GuildName:    ev.GuildName,
		Registered:   true,
		RegisteredAt: r.cfg.Now(),
	})
	r.logf("INFO: discord registrar: registered %d slash commands against guild %s (%s)", len(results), ev.GuildID, ev.GuildName)
}

func (r *Registrar) handleDelete(ev GuildEvent) {
	r.mu.Lock()
	delete(r.statuses, ev.GuildID)
	r.mu.Unlock()
	r.logf("INFO: discord registrar: removed guild %s from status (kicked / left)", ev.GuildID)
}

func (r *Registrar) recordStatus(s GuildStatus) {
	r.mu.Lock()
	r.statuses[s.GuildID] = s
	r.mu.Unlock()
}

func (r *Registrar) logf(format string, args ...any) {
	if r.cfg.Logger == nil {
		return
	}
	r.cfg.Logger.Printf(format, args...)
}
