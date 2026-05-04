// Package server — v8.x OAuth bot-token install RPCs.
//
// These handlers extend `IntegrationsService` with the four-step
// browser-driven install flow:
//
//   BeginOAuth      → returns the upstream authorize URL + spins up
//                     a one-shot loopback callback server that races
//                     to receive the user's redirect.
//   GetOAuthStatus  → polled by the GUI / TUI to surface
//                     "in_progress" → "connected" → "error" pills.
//   CancelOAuth     → tears the in-flight callback server down so the
//                     user can restart or pick a different provider.
//   PostOAuthHello  → sends a "hello" through the captured bot token
//                     so the user can confirm the install end-to-end.
//
// All persistence flows through `internal/config/integrations.go` —
// bot tokens land in the OS keyring, non-secret metadata
// (team id, bot username, etc.) lands in `integrations.yaml`. The
// signing-secret path the v8.0 inbound handlers verify against is
// untouched: OAuth is purely additive.
package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/discordbot"
	"github.com/watchfire-io/watchfire/internal/daemon/oauth"
	"github.com/watchfire-io/watchfire/internal/daemon/slackbot"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// oauthFlow holds the per-provider in-flight install. The integrations
// service owns one flow per provider — kicking off a new BeginOAuth
// while a flow is in_progress cancels the prior attempt rather than
// queuing.
type oauthFlow struct {
	server *oauth.Server
	cancel context.CancelFunc
	state  pb.OAuthState
	errMsg string
	connectedAs string
	channel string
	completedAt time.Time
}

// oauthCoordinator tracks per-provider flow state. Lives on the
// integrationsService; persists across BeginOAuth / GetOAuthStatus
// calls. Concurrent-safe.
type oauthCoordinator struct {
	mu     sync.Mutex
	store  *oauth.StateStore
	flows  map[pb.OAuthProvider]*oauthFlow
}

func newOAuthCoordinator() *oauthCoordinator {
	return &oauthCoordinator{
		store: oauth.NewStateStore(),
		flows: make(map[pb.OAuthProvider]*oauthFlow),
	}
}

// BeginOAuth kicks off a fresh install flow. Returns the authorize
// URL the GUI / TUI should open in the user's default browser; the
// daemon also fires `oauth.OpenBrowser` opportunistically so a
// well-behaved client doesn't have to.
func (s *integrationsService) BeginOAuth(_ context.Context, req *pb.BeginOAuthRequest) (*pb.BeginOAuthResponse, error) {
	if s.oauth == nil {
		s.oauth = newOAuthCoordinator()
	}
	provider := req.GetProvider()
	if provider == pb.OAuthProvider_OAUTH_PROVIDER_UNSET {
		return nil, fmt.Errorf("BeginOAuth: provider required")
	}

	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}
	clientID, clientSecret, resolveErr := resolveOAuthCreds(provider, cfg.Inbound)
	if resolveErr != nil {
		return nil, resolveErr
	}

	s.oauth.mu.Lock()
	// Cancel any prior in-flight flow for this provider.
	if prev, ok := s.oauth.flows[provider]; ok && prev.cancel != nil {
		prev.cancel()
	}
	s.oauth.mu.Unlock()

	cb, err := oauth.NewServer(s.oauth.store, s.httpClient, log.Default())
	if err != nil {
		return nil, fmt.Errorf("oauth: callback server: %w", err)
	}

	providerName := providerToString(provider)
	redirectURI := cb.RedirectURI(providerName)
	state, err := s.oauth.store.Begin(providerName, clientID, clientSecret, redirectURI, req.GetDefaultChannel())
	if err != nil {
		return nil, err
	}

	var authorizeURL string
	switch provider {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		authorizeURL = oauth.BuildSlackAuthURL(clientID, redirectURI, state)
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		authorizeURL = oauth.BuildDiscordAuthURL(clientID, redirectURI, state, oauth.DiscordPermissions)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	flow := &oauthFlow{
		server: cb,
		cancel: cancel,
		state:  pb.OAuthState_OAUTH_STATE_IN_PROGRESS,
		channel: req.GetDefaultChannel(),
	}
	s.oauth.mu.Lock()
	s.oauth.flows[provider] = flow
	s.oauth.mu.Unlock()

	// Run the callback server + watcher goroutine. The watcher records
	// the install on success + persists secrets / metadata; the
	// listener tears down after the first delivery.
	go cb.Run(ctx)
	go s.awaitOAuthCallback(ctx, provider, cb, flow)

	// Best-effort: launch the user's default browser. Failure is non-
	// fatal — clients can use authorize_url from the response instead.
	if openErr := oauth.BrowserOpener(authorizeURL); openErr != nil {
		log.Printf("INFO: oauth: browser launch failed (caller will use authorize_url): %v", openErr)
	}

	return &pb.BeginOAuthResponse{
		AuthorizeUrl: authorizeURL,
		RedirectUri:  redirectURI,
		State:        state,
	}, nil
}

// awaitOAuthCallback blocks on the callback server's result channel
// and persists the captured install. Runs in its own goroutine.
func (s *integrationsService) awaitOAuthCallback(ctx context.Context, provider pb.OAuthProvider, cb *oauth.Server, flow *oauthFlow) {
	res, waitErr := cb.Wait(ctx)
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	if waitErr != nil {
		flow.state = pb.OAuthState_OAUTH_STATE_ERROR
		flow.errMsg = waitErr.Error()
		return
	}
	if res.Err != nil {
		flow.state = pb.OAuthState_OAUTH_STATE_ERROR
		flow.errMsg = res.Err.Error()
		return
	}

	persistErr := s.persistOAuthInstall(provider, res)
	if persistErr != nil {
		flow.state = pb.OAuthState_OAUTH_STATE_ERROR
		flow.errMsg = persistErr.Error()
		return
	}
	flow.state = pb.OAuthState_OAUTH_STATE_CONNECTED
	flow.completedAt = time.Now().UTC()
	switch provider {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		if res.SlackInstall != nil {
			flow.connectedAs = formatSlackPill(res.SlackInstall)
		}
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		if res.DiscordInstall != nil {
			flow.connectedAs = formatDiscordPill(res.DiscordInstall)
		}
	}
}

// persistOAuthInstall writes the captured bot token + metadata back
// to integrations.yaml + keyring. Called from the callback goroutine
// once the upstream exchange succeeds.
func (s *integrationsService) persistOAuthInstall(provider pb.OAuthProvider, res oauth.CallbackResult) error {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return fmt.Errorf("load integrations: %w", err)
	}
	in := cfg.Inbound

	switch provider {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		install := res.SlackInstall
		if install == nil {
			return fmt.Errorf("oauth: slack callback delivered nil install")
		}
		if putErr := config.PutIntegrationSecret(inboundSecretKeySlackBotToken, install.BotToken); putErr != nil {
			return fmt.Errorf("put slack bot token: %w", putErr)
		}
		in.SlackBotTokenRef = inboundSecretKeySlackBotToken
		in.SlackTeamID = install.TeamID
		in.SlackTeamName = install.TeamName
		in.SlackBotUserID = install.BotUserID
		in.SlackBotUsername = install.BotUsername
		if res.DefaultChannel != "" {
			in.SlackDefaultChannel = res.DefaultChannel
		}
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		install := res.DiscordInstall
		if install == nil {
			return fmt.Errorf("oauth: discord callback delivered nil install")
		}
		// For Discord, the OAuth flow may or may not surface a usable
		// bot token directly. When it does (some flows include it),
		// store it; otherwise the user has already pasted the bot
		// token via the v8.0 path and we just record the metadata.
		if install.BotToken != "" {
			if putErr := config.PutIntegrationSecret(inboundSecretKeyDiscordBotToken, install.BotToken); putErr != nil {
				return fmt.Errorf("put discord bot token: %w", putErr)
			}
			in.DiscordBotTokenRef = inboundSecretKeyDiscordBotToken
		}
		in.DiscordBotUsername = install.Username
		in.DiscordBotDiscriminator = install.Discriminator
		if res.DefaultChannel != "" {
			in.DiscordDefaultChannel = res.DefaultChannel
		}
	}

	cfg.Inbound = in
	if err := config.SaveIntegrations(cfg); err != nil {
		return fmt.Errorf("save integrations: %w", err)
	}

	// Trigger Echo restart so any handlers that consult the OAuth-
	// captured fields pick up the new config.
	if s.server != nil {
		s.server.restartEchoServer()
	}
	return nil
}

// GetOAuthStatus returns the current per-provider flow state. Idle
// (no flow ever started, or last flow cancelled) collapses to
// `OAUTH_STATE_CONNECTED` when a bot token already lives in the
// keyring — the UI then renders the pill without prompting for a
// fresh install.
func (s *integrationsService) GetOAuthStatus(_ context.Context, req *pb.GetOAuthStatusRequest) (*pb.OAuthStatus, error) {
	if s.oauth == nil {
		s.oauth = newOAuthCoordinator()
	}
	provider := req.GetProvider()
	if provider == pb.OAuthProvider_OAUTH_PROVIDER_UNSET {
		return nil, fmt.Errorf("GetOAuthStatus: provider required")
	}

	s.oauth.mu.Lock()
	flow, hasFlow := s.oauth.flows[provider]
	s.oauth.mu.Unlock()

	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, err
	}
	out := &pb.OAuthStatus{Provider: provider}

	if hasFlow {
		out.State = flow.state
		out.Error = flow.errMsg
		out.ConnectedAs = flow.connectedAs
		out.DefaultChannel = flow.channel
	}

	// Check the keyring — a connected install survives daemon restart
	// (which clears the in-memory flows map).
	switch provider {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		if cfg.Inbound.SlackBotTokenRef != "" {
			if _, ok := config.LookupIntegrationSecret(cfg.Inbound.SlackBotTokenRef); ok {
				if out.State == pb.OAuthState_OAUTH_STATE_IDLE {
					out.State = pb.OAuthState_OAUTH_STATE_CONNECTED
				}
				if out.ConnectedAs == "" {
					out.ConnectedAs = formatSlackPillFromConfig(cfg.Inbound)
				}
				if out.DefaultChannel == "" {
					out.DefaultChannel = cfg.Inbound.SlackDefaultChannel
				}
			}
		}
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		if cfg.Inbound.DiscordBotTokenRef != "" {
			if _, ok := config.LookupIntegrationSecret(cfg.Inbound.DiscordBotTokenRef); ok {
				if out.State == pb.OAuthState_OAUTH_STATE_IDLE {
					out.State = pb.OAuthState_OAUTH_STATE_CONNECTED
				}
				if out.ConnectedAs == "" {
					out.ConnectedAs = formatDiscordPillFromConfig(cfg.Inbound)
				}
				if out.DefaultChannel == "" {
					out.DefaultChannel = cfg.Inbound.DiscordDefaultChannel
				}
			}
		}
	}
	return out, nil
}

// CancelOAuth tears the in-flight callback server down. Idempotent.
func (s *integrationsService) CancelOAuth(_ context.Context, req *pb.CancelOAuthRequest) (*pb.OAuthStatus, error) {
	if s.oauth == nil {
		s.oauth = newOAuthCoordinator()
	}
	provider := req.GetProvider()
	if provider == pb.OAuthProvider_OAUTH_PROVIDER_UNSET {
		return nil, fmt.Errorf("CancelOAuth: provider required")
	}
	s.oauth.mu.Lock()
	if flow, ok := s.oauth.flows[provider]; ok && flow.cancel != nil {
		flow.cancel()
		flow.state = pb.OAuthState_OAUTH_STATE_IDLE
		flow.errMsg = ""
	}
	s.oauth.mu.Unlock()
	return s.GetOAuthStatus(context.Background(), &pb.GetOAuthStatusRequest{Provider: provider})
}

// PostOAuthHello sends a one-shot "hello" message through the captured
// bot token. Returns ok=false when the bot token is missing / the
// channel is invalid / the upstream call fails.
func (s *integrationsService) PostOAuthHello(ctx context.Context, req *pb.PostOAuthHelloRequest) (*pb.PostOAuthHelloResponse, error) {
	provider := req.GetProvider()
	if provider == pb.OAuthProvider_OAUTH_PROVIDER_UNSET {
		return &pb.PostOAuthHelloResponse{Ok: false, Message: "provider required"}, nil
	}
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return &pb.PostOAuthHelloResponse{Ok: false, Message: err.Error()}, nil
	}
	text := req.GetText()
	if text == "" {
		text = "Hello from Watchfire — your bot token is connected and working."
	}

	switch provider {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		token, ok := config.LookupIntegrationSecret(cfg.Inbound.SlackBotTokenRef)
		if !ok || token == "" {
			return &pb.PostOAuthHelloResponse{Ok: false, Message: "slack bot token not connected"}, nil
		}
		channel := req.GetChannel()
		if channel == "" {
			channel = cfg.Inbound.SlackDefaultChannel
		}
		if channel == "" {
			return &pb.PostOAuthHelloResponse{Ok: false, Message: "slack channel not configured"}, nil
		}
		client := slackbot.New()
		if sendErr := client.PostMessage(ctx, token, channel, text); sendErr != nil {
			return &pb.PostOAuthHelloResponse{Ok: false, Message: sendErr.Error()}, nil
		}
		return &pb.PostOAuthHelloResponse{Ok: true, Message: fmt.Sprintf("posted to %s", channel)}, nil
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		token, ok := config.LookupIntegrationSecret(cfg.Inbound.DiscordBotTokenRef)
		if !ok || token == "" {
			return &pb.PostOAuthHelloResponse{Ok: false, Message: "discord bot token not connected"}, nil
		}
		channel := req.GetChannel()
		if channel == "" {
			channel = cfg.Inbound.DiscordDefaultChannel
		}
		if channel == "" {
			return &pb.PostOAuthHelloResponse{Ok: false, Message: "discord channel not configured"}, nil
		}
		client := discordbot.New()
		if sendErr := client.PostMessage(ctx, token, channel, text); sendErr != nil {
			return &pb.PostOAuthHelloResponse{Ok: false, Message: sendErr.Error()}, nil
		}
		return &pb.PostOAuthHelloResponse{Ok: true, Message: fmt.Sprintf("posted to %s", channel)}, nil
	default:
		return &pb.PostOAuthHelloResponse{Ok: false, Message: "unknown provider"}, nil
	}
}

func resolveOAuthCreds(provider pb.OAuthProvider, in models.InboundConfig) (clientID, clientSecret string, err error) {
	switch provider {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		if in.SlackClientID == "" {
			return "", "", fmt.Errorf("slack client id not configured — set inbound.slack_client_id first")
		}
		if in.SlackClientSecretRef == "" {
			return "", "", fmt.Errorf("slack client secret not configured — set inbound.slack_client_secret first")
		}
		secret, ok := config.LookupIntegrationSecret(in.SlackClientSecretRef)
		if !ok || secret == "" {
			return "", "", fmt.Errorf("slack client secret not in keyring")
		}
		return in.SlackClientID, secret, nil
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		if in.DiscordClientID == "" {
			return "", "", fmt.Errorf("discord client id not configured — set inbound.discord_client_id first")
		}
		if in.DiscordClientSecretRef == "" {
			return "", "", fmt.Errorf("discord client secret not configured — set inbound.discord_client_secret first")
		}
		secret, ok := config.LookupIntegrationSecret(in.DiscordClientSecretRef)
		if !ok || secret == "" {
			return "", "", fmt.Errorf("discord client secret not in keyring")
		}
		return in.DiscordClientID, secret, nil
	}
	return "", "", fmt.Errorf("unknown oauth provider")
}

func providerToString(p pb.OAuthProvider) string {
	switch p {
	case pb.OAuthProvider_OAUTH_PROVIDER_SLACK:
		return "slack"
	case pb.OAuthProvider_OAUTH_PROVIDER_DISCORD:
		return "discord"
	}
	return "unknown"
}

func formatSlackPill(install *oauth.SlackInstall) string {
	who := install.BotUsername
	if who == "" {
		who = "watchfire"
	}
	if install.TeamName != "" {
		return fmt.Sprintf("@%s in %s", who, install.TeamName)
	}
	return "@" + who
}

func formatSlackPillFromConfig(in models.InboundConfig) string {
	who := in.SlackBotUsername
	if who == "" {
		who = "watchfire"
	}
	if in.SlackTeamName != "" {
		return fmt.Sprintf("@%s in %s", who, in.SlackTeamName)
	}
	return "@" + who
}

func formatDiscordPill(install *oauth.DiscordInstall) string {
	who := install.Username
	if who == "" {
		who = "Watchfire"
	}
	if install.Discriminator != "" && install.Discriminator != "0" {
		who = fmt.Sprintf("%s#%s", who, install.Discriminator)
	}
	if install.GuildName != "" {
		return fmt.Sprintf("%s in %s", who, install.GuildName)
	}
	return who
}

func formatDiscordPillFromConfig(in models.InboundConfig) string {
	who := in.DiscordBotUsername
	if who == "" {
		who = "Watchfire"
	}
	if in.DiscordBotDiscriminator != "" && in.DiscordBotDiscriminator != "0" {
		who = fmt.Sprintf("%s#%s", who, in.DiscordBotDiscriminator)
	}
	return who
}
