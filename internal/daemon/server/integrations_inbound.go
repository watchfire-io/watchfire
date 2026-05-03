// Package server — v8.0 Echo inbound integrations RPCs.
//
// The two RPCs in this file (`GetInboundStatus` / `SaveInboundConfig`)
// extend `IntegrationsService` so the v7.0 Relay outbound code stays
// untouched. Per-provider secrets ride a write-only field convention
// mirroring v7.0's webhook secret handling: Save accepts the plaintext
// (and pushes it to the keyring), List / Save responses scrub the
// plaintext and return only the `*_set: bool` companion.
package server

import (
	"context"
	"fmt"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// inboundSecretKeyGitHub / Slack / Discord are the canonical keyring
// keys for the v8.0 Echo per-provider secrets. Single-instance per
// daemon (one GitHub webhook secret, one Slack signing secret, one
// Discord application public key + bot token) so we don't need a
// per-integration ID suffix the v7.0 outbound flow uses.
const (
	inboundSecretKeyGitHub          = "watchfire.echo.github_secret"
	inboundSecretKeySlack           = "watchfire.echo.slack_secret"
	inboundSecretKeyDiscordPubKey   = "watchfire.echo.discord_public_key"
	inboundSecretKeyDiscordBotToken = "watchfire.echo.discord_bot_token"
)

// GetInboundStatus returns the live status of the v8.0 Echo HTTP
// listener — bind state, last delivery timestamps per provider, and the
// scrubbed config (no plaintext secrets) for the settings UI to render.
func (s *integrationsService) GetInboundStatus(_ context.Context, _ *pb.GetInboundStatusRequest) (*pb.InboundStatus, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}
	return s.buildInboundStatus(cfg.Inbound), nil
}

// SaveInboundConfig persists the InboundConfig to integrations.yaml,
// pushes any non-empty secret fields to the keyring, then triggers an
// Echo-server restart so the new bind address / disabled flag takes
// effect immediately. Returns the post-restart status (so the UI sees
// the new "listening" pill state in the same round-trip).
func (s *integrationsService) SaveInboundConfig(_ context.Context, req *pb.SaveInboundConfigRequest) (*pb.InboundStatus, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}

	in := req.GetConfig()
	if in == nil {
		return nil, fmt.Errorf("save inbound: missing config")
	}

	// Carry over existing keyring refs; only overwrite when the caller
	// supplied a non-empty plaintext secret. Empty plaintext = "leave
	// keyring entry alone" (matches the v7.0 Slack / Discord URL
	// upsert convention).
	merged := cfg.Inbound
	merged.ListenAddr = in.GetListenAddr()
	merged.PublicURL = in.GetPublicUrl()
	merged.DiscordAppID = in.GetDiscordAppId()
	merged.Disabled = in.GetDisabled()

	if v := in.GetGithubSecret(); v != "" {
		if putErr := config.PutIntegrationSecret(inboundSecretKeyGitHub, v); putErr != nil {
			return nil, fmt.Errorf("put github secret: %w", putErr)
		}
		merged.GitHubSecretRef = inboundSecretKeyGitHub
	}
	if v := in.GetSlackSecret(); v != "" {
		if putErr := config.PutIntegrationSecret(inboundSecretKeySlack, v); putErr != nil {
			return nil, fmt.Errorf("put slack secret: %w", putErr)
		}
		merged.SlackSecretRef = inboundSecretKeySlack
	}
	if v := in.GetDiscordPublicKey(); v != "" {
		if putErr := config.PutIntegrationSecret(inboundSecretKeyDiscordPubKey, v); putErr != nil {
			return nil, fmt.Errorf("put discord public key: %w", putErr)
		}
		merged.DiscordPublicKeyRef = inboundSecretKeyDiscordPubKey
	}
	if v := in.GetDiscordBotToken(); v != "" {
		if putErr := config.PutIntegrationSecret(inboundSecretKeyDiscordBotToken, v); putErr != nil {
			return nil, fmt.Errorf("put discord bot token: %w", putErr)
		}
		merged.DiscordBotTokenRef = inboundSecretKeyDiscordBotToken
	}

	cfg.Inbound = merged
	if saveErr := config.SaveIntegrations(cfg); saveErr != nil {
		return nil, fmt.Errorf("save integrations: %w", saveErr)
	}

	// Restart the Echo server so the new ListenAddr / Disabled state
	// takes effect in the next poll of the status pill.
	if s.server != nil {
		s.server.restartEchoServer()
	}

	return s.buildInboundStatus(merged), nil
}

// buildInboundStatus assembles the wire response for both RPCs. Pulls
// the live bind state from the Echo server (when wired), falling back
// to "not listening" when the daemon hasn't bound it yet (e.g. first
// boot before SaveInboundConfig has fired).
func (s *integrationsService) buildInboundStatus(in models.InboundConfig) *pb.InboundStatus {
	addr := in.ListenAddr
	if addr == "" {
		addr = echo.DefaultListenAddr
	}
	out := &pb.InboundStatus{
		Listening:  false,
		ListenAddr: addr,
		PublicUrl:  in.PublicURL,
		Version:    buildinfo.Version,
		Config:     scrubInboundConfigToProto(in),
	}
	if s.server != nil {
		if srv := s.server.EchoServer(); srv != nil {
			out.Listening = srv.Listening()
			out.BindError = srv.BindError()
			if t := srv.LastDelivery("github"); !t.IsZero() {
				out.LastGithubDeliveryUnix = t.Unix()
			}
			if t := srv.LastDelivery("slack"); !t.IsZero() {
				out.LastSlackDeliveryUnix = t.Unix()
			}
			if t := srv.LastDelivery("discord"); !t.IsZero() {
				out.LastDiscordDeliveryUnix = t.Unix()
			}
		}
		if reg := s.server.DiscordRegistrar(); reg != nil {
			for _, g := range reg.Statuses() {
				gr := &pb.DiscordGuildRegistration{
					GuildId:    g.GuildID,
					GuildName:  g.GuildName,
					Registered: g.Registered,
					Error:      g.Error,
				}
				if !g.RegisteredAt.IsZero() {
					gr.RegisteredAtUnix = g.RegisteredAt.Unix()
				}
				out.DiscordGuilds = append(out.DiscordGuilds, gr)
			}
		}
	}
	return out
}

// scrubInboundConfigToProto returns the wire-shape of InboundConfig with
// every plaintext secret stripped and the corresponding `*_set` companion
// reflecting whether the keyring lookup resolves. Plaintext secret
// fields on the response are always empty strings.
func scrubInboundConfigToProto(in models.InboundConfig) *pb.InboundConfig {
	return &pb.InboundConfig{
		ListenAddr:           in.ListenAddr,
		PublicUrl:            in.PublicURL,
		GithubSecretSet:      keyringHas(in.GitHubSecretRef),
		SlackSecretSet:       keyringHas(in.SlackSecretRef),
		DiscordPublicKeySet:  keyringHas(in.DiscordPublicKeyRef),
		DiscordAppId:         in.DiscordAppID,
		DiscordBotTokenSet:   keyringHas(in.DiscordBotTokenRef),
		Disabled:             in.Disabled,
	}
}

func keyringHas(ref string) bool {
	if ref == "" {
		return false
	}
	_, ok := config.LookupIntegrationSecret(ref)
	return ok
}
