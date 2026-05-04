package server

import (
	"context"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/discord"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	pb "github.com/watchfire-io/watchfire/proto"
)

// fakeInboundProvider is a no-op provider for tests that don't need a
// live Echo server. The status RPC degrades gracefully when EchoServer
// returns nil — no listening, no last-delivery timestamps.
type fakeInboundProvider struct {
	srv         *echo.Server
	registrar   *discord.Registrar
	restartHits int
}

func (f *fakeInboundProvider) EchoServer() *echo.Server               { return f.srv }
func (f *fakeInboundProvider) DiscordRegistrar() *discord.Registrar   { return f.registrar }
func (f *fakeInboundProvider) restartEchoServer()                     { f.restartHits++ }

// TestSaveInboundConfigRoundTrip: SaveInboundConfig persists the listen
// address + public URL + per-provider secrets, scrubs plaintext secrets
// from the response, and triggers an Echo restart.
func TestSaveInboundConfigRoundTrip(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	provider := &fakeInboundProvider{}
	svc.bindEchoServer(provider)
	ctx := context.Background()

	// Save with all per-provider secrets supplied.
	saveResp, err := svc.SaveInboundConfig(ctx, &pb.SaveInboundConfigRequest{
		Config: &pb.InboundConfig{
			ListenAddr:       "127.0.0.1:9999",
			PublicUrl:        "https://example.ngrok.app",
			GithubSecret:     "gh-shhh",
			SlackSecret:      "sl-shhh",
			DiscordPublicKey: "dc-pubkey",
			DiscordAppId:     "12345",
			DiscordBotToken:  "dc-bot-token",
			GitHost:          "github-enterprise",
			GitHostBaseUrl:   "https://github.example.com",
			GitlabSecret:     "gl-shhh",
			BitbucketSecret:  "bb-shhh",
		},
	})
	if err != nil {
		t.Fatalf("SaveInboundConfig: %v", err)
	}

	// Plaintext secrets are scrubbed from the response.
	if saveResp.GetConfig().GetGithubSecret() != "" {
		t.Fatalf("github secret leaked in response: %q", saveResp.GetConfig().GetGithubSecret())
	}
	if saveResp.GetConfig().GetSlackSecret() != "" {
		t.Fatalf("slack secret leaked: %q", saveResp.GetConfig().GetSlackSecret())
	}
	if saveResp.GetConfig().GetDiscordPublicKey() != "" {
		t.Fatalf("discord public key leaked: %q", saveResp.GetConfig().GetDiscordPublicKey())
	}
	if saveResp.GetConfig().GetDiscordBotToken() != "" {
		t.Fatalf("discord bot token leaked: %q", saveResp.GetConfig().GetDiscordBotToken())
	}

	// `*_set` companions reflect that the keyring entries were written.
	if !saveResp.GetConfig().GetGithubSecretSet() {
		t.Fatal("expected github_secret_set=true after save")
	}
	if !saveResp.GetConfig().GetSlackSecretSet() {
		t.Fatal("expected slack_secret_set=true after save")
	}
	if !saveResp.GetConfig().GetDiscordPublicKeySet() {
		t.Fatal("expected discord_public_key_set=true after save")
	}
	if !saveResp.GetConfig().GetDiscordBotTokenSet() {
		t.Fatal("expected discord_bot_token_set=true after save")
	}
	if !saveResp.GetConfig().GetGitlabSecretSet() {
		t.Fatal("expected gitlab_secret_set=true after save")
	}
	if !saveResp.GetConfig().GetBitbucketSecretSet() {
		t.Fatal("expected bitbucket_secret_set=true after save")
	}
	if saveResp.GetConfig().GetGitHost() != "github-enterprise" {
		t.Fatalf("git_host round-trip: %q", saveResp.GetConfig().GetGitHost())
	}
	if saveResp.GetConfig().GetGitHostBaseUrl() != "https://github.example.com" {
		t.Fatalf("git_host_base_url round-trip: %q", saveResp.GetConfig().GetGitHostBaseUrl())
	}

	// Non-secret fields round-trip verbatim.
	if saveResp.GetConfig().GetListenAddr() != "127.0.0.1:9999" {
		t.Fatalf("listen_addr round-trip: %q", saveResp.GetConfig().GetListenAddr())
	}
	if saveResp.GetConfig().GetPublicUrl() != "https://example.ngrok.app" {
		t.Fatalf("public_url round-trip: %q", saveResp.GetConfig().GetPublicUrl())
	}
	if saveResp.GetConfig().GetDiscordAppId() != "12345" {
		t.Fatalf("discord_app_id round-trip: %q", saveResp.GetConfig().GetDiscordAppId())
	}

	// The restart was triggered.
	if provider.restartHits != 1 {
		t.Fatalf("expected 1 restartEchoServer call, got %d", provider.restartHits)
	}

	// Subsequent GetInboundStatus reflects the persisted config.
	status, err := svc.GetInboundStatus(ctx, &pb.GetInboundStatusRequest{})
	if err != nil {
		t.Fatalf("GetInboundStatus: %v", err)
	}
	if status.GetListenAddr() != "127.0.0.1:9999" {
		t.Fatalf("status listen_addr: %q", status.GetListenAddr())
	}
	if status.GetConfig().GetGithubSecret() != "" {
		t.Fatal("status leaked github plaintext")
	}
	if !status.GetConfig().GetGithubSecretSet() {
		t.Fatal("status github_secret_set should be true after save")
	}

	// Empty secret on update preserves the existing keyring entry.
	_, err = svc.SaveInboundConfig(ctx, &pb.SaveInboundConfigRequest{
		Config: &pb.InboundConfig{
			ListenAddr:   "127.0.0.1:9999",
			PublicUrl:    "https://example.ngrok.app",
			GithubSecret: "", // empty — should NOT clear
		},
	})
	if err != nil {
		t.Fatalf("second SaveInboundConfig: %v", err)
	}
	postUpdate, _ := svc.GetInboundStatus(ctx, &pb.GetInboundStatusRequest{})
	if !postUpdate.GetConfig().GetGithubSecretSet() {
		t.Fatal("github secret was cleared by empty-string update; expected to preserve")
	}
}

// TestGetInboundStatusEmpty: with no prior save, GetInboundStatus returns
// the default listen address and all `*_set` flags false.
func TestGetInboundStatusEmpty(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	svc.bindEchoServer(&fakeInboundProvider{})

	status, err := svc.GetInboundStatus(context.Background(), &pb.GetInboundStatusRequest{})
	if err != nil {
		t.Fatalf("GetInboundStatus: %v", err)
	}
	if status.GetListenAddr() != echo.DefaultListenAddr {
		t.Fatalf("default listen_addr expected %q, got %q", echo.DefaultListenAddr, status.GetListenAddr())
	}
	if status.GetConfig().GetGithubSecretSet() ||
		status.GetConfig().GetSlackSecretSet() ||
		status.GetConfig().GetDiscordPublicKeySet() ||
		status.GetConfig().GetDiscordBotTokenSet() {
		t.Fatal("expected all *_set flags false on fresh install")
	}
	if status.GetListening() {
		t.Fatal("Listening should be false when EchoServer is nil")
	}
}
