// Package models — integrations config shapes for v7.0 Relay.
//
// The settings UI (task 0066) lives upstream of the four adapter tasks
// (0062 webhook, 0063 Slack, 0064 Discord, 0065 GitHub auto-PR), so this
// file lands the canonical struct shapes here once. Adapters fill in the
// runtime semantics (HMAC signing, Block Kit rendering, etc.); the UI
// reads / writes these structs and the YAML they serialise to.
package models

// EventBitmask is a tri-event toggle for outbound integrations. Each
// integration carries its own copy so the user can fan TASK_FAILED to
// Slack but RUN_COMPLETE to Discord without duplicating endpoints.
type EventBitmask struct {
	TaskFailed   bool `yaml:"task_failed" json:"task_failed"`
	RunComplete  bool `yaml:"run_complete" json:"run_complete"`
	WeeklyDigest bool `yaml:"weekly_digest" json:"weekly_digest"`
}

// AnySet returns true if at least one event bit is set.
func (e EventBitmask) AnySet() bool {
	return e.TaskFailed || e.RunComplete || e.WeeklyDigest
}

// WebhookEndpoint is a single generic outbound webhook target. Secret is
// stored in the OS keyring keyed by SecretRef; YAML carries only the
// reference + URL + label.
type WebhookEndpoint struct {
	ID             string       `yaml:"id" json:"id"`
	Label          string       `yaml:"label" json:"label"`
	URL            string       `yaml:"url" json:"url"`
	SecretRef      string       `yaml:"secret_ref,omitempty" json:"secret_ref,omitempty"`
	EnabledEvents  EventBitmask `yaml:"enabled_events" json:"enabled_events"`
	ProjectMuteIDs []string     `yaml:"project_mute_ids,omitempty" json:"project_mute_ids,omitempty"`
}

// SlackEndpoint targets a Slack incoming webhook. The URL is itself the
// secret, so YAML stores only an empty placeholder + the keyring ref;
// `LoadIntegrations` resolves the URL on demand.
type SlackEndpoint struct {
	ID             string       `yaml:"id" json:"id"`
	Label          string       `yaml:"label" json:"label"`
	URLRef         string       `yaml:"url_ref,omitempty" json:"url_ref,omitempty"`
	URL            string       `yaml:"-" json:"-"`
	EnabledEvents  EventBitmask `yaml:"enabled_events" json:"enabled_events"`
	ProjectMuteIDs []string     `yaml:"project_mute_ids,omitempty" json:"project_mute_ids,omitempty"`
}

// DiscordEndpoint targets a Discord webhook URL. Mirrors SlackEndpoint —
// URL stored in keyring, YAML carries only the reference. v8.0 Echo adds
// `GuildID` so inbound interactions delivered against a Discord guild
// can be routed to the projects the user has wired to that guild
// (see `internal/daemon/echo/handler_discord.go`).
type DiscordEndpoint struct {
	ID             string       `yaml:"id" json:"id"`
	Label          string       `yaml:"label" json:"label"`
	URLRef         string       `yaml:"url_ref,omitempty" json:"url_ref,omitempty"`
	URL            string       `yaml:"-" json:"-"`
	GuildID        string       `yaml:"guild_id,omitempty" json:"guild_id,omitempty"`
	EnabledEvents  EventBitmask `yaml:"enabled_events" json:"enabled_events"`
	ProjectMuteIDs []string     `yaml:"project_mute_ids,omitempty" json:"project_mute_ids,omitempty"`
}

// GitHubConfig is the single-instance auto-PR configuration. Only one
// GitHubConfig exists per Watchfire install — the project scopes list
// names which projects get the PR flow instead of the silent merge.
//
// Authentication piggybacks on `gh` CLI auth — no token field here.
type GitHubConfig struct {
	Enabled       bool     `yaml:"enabled" json:"enabled"`
	DraftDefault  bool     `yaml:"draft_default" json:"draft_default"`
	ProjectScopes []string `yaml:"project_scopes,omitempty" json:"project_scopes,omitempty"`
}

// AutoPRApplies returns true if the GitHub auto-PR flow should fire for
// the given project. Empty ProjectScopes means "all projects" so a fresh
// install with `enabled: true` lights up across the fleet.
func (g GitHubConfig) AutoPRApplies(projectID string) bool {
	if !g.Enabled {
		return false
	}
	if len(g.ProjectScopes) == 0 {
		return true
	}
	for _, id := range g.ProjectScopes {
		if id == projectID {
			return true
		}
	}
	return false
}

// InboundConfig holds the v8.0 Echo inbound HTTP server configuration.
// Outbound integrations (the v7.0 fields above) are unaffected — this
// is purely additive. An empty / zero-value `InboundConfig` means "no
// inbound listener", preserving v7.0 behaviour for installs that have
// not opted in.
//
// `DiscordPublicKeyRef` is the keyring reference for the Discord
// application's Ed25519 public key (Discord interactions verify against
// a single per-application key, not per-guild secrets — see
// https://discord.com/developers/docs/interactions/receiving-and-responding).
// `DiscordAppID` and `DiscordBotTokenRef` are needed by the
// `watchfire integrations register-discord` CLI to register slash
// commands against a guild on the user's behalf.
type InboundConfig struct {
	ListenAddr           string `yaml:"listen_addr,omitempty" json:"listen_addr,omitempty"`
	PublicURL            string `yaml:"public_url,omitempty" json:"public_url,omitempty"`
	GitHubSecretRef      string `yaml:"github_secret_ref,omitempty" json:"github_secret_ref,omitempty"`
	SlackSecretRef       string `yaml:"slack_secret_ref,omitempty" json:"slack_secret_ref,omitempty"`
	DiscordPublicKeyRef  string `yaml:"discord_public_key_ref,omitempty" json:"discord_public_key_ref,omitempty"`
	DiscordAppID         string `yaml:"discord_app_id,omitempty" json:"discord_app_id,omitempty"`
	DiscordBotTokenRef   string `yaml:"discord_bot_token_ref,omitempty" json:"discord_bot_token_ref,omitempty"`
	Disabled             bool   `yaml:"disabled,omitempty" json:"disabled,omitempty"`

	// RateLimitPerMin is the v8.x per-IP token-bucket budget applied
	// across all `/echo/*` routes. 0 disables the limiter; negative
	// values are treated as 0. Defaults to 30 when the field is unset
	// (see `echo.DefaultRateLimitPerMin`). Verified deliveries that hit
	// the idempotency cache do NOT consume the bucket — they are
	// already a no-op upstream of the per-handler verify path.
	RateLimitPerMin int `yaml:"rate_limit_per_min,omitempty" json:"rate_limit_per_min,omitempty"`
}

// IntegrationsConfig is the root document persisted at
// `~/.watchfire/integrations.yaml`. All four adapter types fan out from
// here; each subset can be empty. v8.0 Echo adds `Inbound` for the
// inbound HTTP listener — purely additive, defaults to disabled.
type IntegrationsConfig struct {
	Webhooks []WebhookEndpoint `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	Slack    []SlackEndpoint   `yaml:"slack,omitempty" json:"slack,omitempty"`
	Discord  []DiscordEndpoint `yaml:"discord,omitempty" json:"discord,omitempty"`
	GitHub   GitHubConfig      `yaml:"github" json:"github"`
	Inbound  InboundConfig     `yaml:"inbound,omitempty" json:"inbound,omitempty"`
}

// NewIntegrationsConfig returns a zero-value config. Used by the loader
// when the YAML file does not yet exist.
func NewIntegrationsConfig() *IntegrationsConfig {
	return &IntegrationsConfig{}
}
