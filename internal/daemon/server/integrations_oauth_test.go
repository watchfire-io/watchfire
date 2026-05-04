package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/oauth"
	pb "github.com/watchfire-io/watchfire/proto"
)

// TestPostOAuthHello_NoToken returns ok=false when no bot token is in
// the keyring — the user hasn't connected yet. The hello RPC should
// surface the missing-token error rather than panic.
func TestPostOAuthHello_NoToken(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	resp, err := svc.PostOAuthHello(context.Background(), &pb.PostOAuthHelloRequest{
		Provider: pb.OAuthProvider_OAUTH_PROVIDER_SLACK,
		Channel:  "#general",
	})
	if err != nil {
		t.Fatalf("PostOAuthHello: %v", err)
	}
	if resp.GetOk() {
		t.Fatal("expected ok=false when no bot token configured")
	}
	if !strings.Contains(resp.GetMessage(), "not connected") {
		t.Errorf("message: %q", resp.GetMessage())
	}
}

// TestGetOAuthStatus_ConnectedFromKeyring: a bot token in the keyring
// surfaces as OAUTH_STATE_CONNECTED even with no in-flight flow,
// because the token survives daemon restart.
func TestGetOAuthStatus_ConnectedFromKeyring(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	// Persist a slack bot token + metadata to integrations.yaml.
	cfg, _ := config.LoadIntegrations()
	cfg.Inbound.SlackBotTokenRef = inboundSecretKeySlackBotToken
	cfg.Inbound.SlackTeamID = "T1"
	cfg.Inbound.SlackTeamName = "Acme"
	cfg.Inbound.SlackBotUsername = "watchfire"
	cfg.Inbound.SlackDefaultChannel = "#alerts"
	if err := config.SaveIntegrations(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := config.PutIntegrationSecret(inboundSecretKeySlackBotToken, "xoxb-test"); err != nil {
		t.Fatalf("put secret: %v", err)
	}

	svc := newIntegrationsService()
	st, err := svc.GetOAuthStatus(context.Background(), &pb.GetOAuthStatusRequest{
		Provider: pb.OAuthProvider_OAUTH_PROVIDER_SLACK,
	})
	if err != nil {
		t.Fatalf("GetOAuthStatus: %v", err)
	}
	if st.GetState() != pb.OAuthState_OAUTH_STATE_CONNECTED {
		t.Errorf("state: %v", st.GetState())
	}
	if !strings.Contains(st.GetConnectedAs(), "watchfire") || !strings.Contains(st.GetConnectedAs(), "Acme") {
		t.Errorf("connected_as: %q", st.GetConnectedAs())
	}
	if st.GetDefaultChannel() != "#alerts" {
		t.Errorf("default_channel: %q", st.GetDefaultChannel())
	}
}

// TestBeginOAuth_RequiresClientCreds: BeginOAuth surfaces a clear
// error when client_id / client_secret are not configured. No
// callback server should be spawned.
func TestBeginOAuth_RequiresClientCreds(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	_, err := svc.BeginOAuth(context.Background(), &pb.BeginOAuthRequest{
		Provider: pb.OAuthProvider_OAUTH_PROVIDER_SLACK,
	})
	if err == nil {
		t.Fatal("expected error when slack client id not set")
	}
	if !strings.Contains(err.Error(), "slack client id") {
		t.Errorf("error: %v", err)
	}
}

// TestBeginOAuth_FullFlow: with client creds configured and the
// upstream Slack endpoints stubbed, BeginOAuth → callback → poll
// chain should land at OAUTH_STATE_CONNECTED with the captured
// metadata persisted.
func TestBeginOAuth_FullFlow(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	// Stub Slack token + auth.test endpoints.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"ok": true,
			"access_token": "xoxb-from-flow",
			"bot_user_id": "U7",
			"team": {"id": "T7", "name": "Test Team"}
		}`))
	}))
	defer tokenSrv.Close()
	authTestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"user":"watchfire-bot"}`))
	}))
	defer authTestSrv.Close()
	prevToken, prevAuth := oauth.SlackTokenURL, oauth.SlackAuthTestURL
	oauth.SlackTokenURL, oauth.SlackAuthTestURL = tokenSrv.URL, authTestSrv.URL
	t.Cleanup(func() { oauth.SlackTokenURL, oauth.SlackAuthTestURL = prevToken, prevAuth })

	// Stub browser launcher so the test doesn't try to spawn `open`.
	prevOpen := oauth.BrowserOpener
	oauth.BrowserOpener = func(string) error { return nil }
	t.Cleanup(func() { oauth.BrowserOpener = prevOpen })

	// Configure client creds.
	cfg, _ := config.LoadIntegrations()
	cfg.Inbound.SlackClientID = "CLIENT"
	cfg.Inbound.SlackClientSecretRef = inboundSecretKeySlackClientSecret
	if err := config.SaveIntegrations(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := config.PutIntegrationSecret(inboundSecretKeySlackClientSecret, "SECRET"); err != nil {
		t.Fatalf("put secret: %v", err)
	}

	svc := newIntegrationsService()
	provider := &fakeInboundProvider{}
	svc.bindEchoServer(provider)

	beginResp, err := svc.BeginOAuth(context.Background(), &pb.BeginOAuthRequest{
		Provider:       pb.OAuthProvider_OAUTH_PROVIDER_SLACK,
		DefaultChannel: "#general",
	})
	if err != nil {
		t.Fatalf("BeginOAuth: %v", err)
	}
	if !strings.Contains(beginResp.GetAuthorizeUrl(), "slack.com/oauth/v2/authorize") {
		t.Errorf("authorize_url: %s", beginResp.GetAuthorizeUrl())
	}
	if !strings.Contains(beginResp.GetRedirectUri(), "/oauth/slack/callback") {
		t.Errorf("redirect_uri: %s", beginResp.GetRedirectUri())
	}

	// Simulate the browser redirecting to the local callback.
	cbURL := beginResp.GetRedirectUri() + "?" + url.Values{
		"code":  []string{"AUTH_CODE"},
		"state": []string{beginResp.GetState()},
	}.Encode()
	resp, err := http.Get(cbURL)
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	_ = resp.Body.Close()

	// Poll status until connected (or timeout).
	deadline := time.Now().Add(3 * time.Second)
	var st *pb.OAuthStatus
	for time.Now().Before(deadline) {
		st, err = svc.GetOAuthStatus(context.Background(), &pb.GetOAuthStatusRequest{
			Provider: pb.OAuthProvider_OAUTH_PROVIDER_SLACK,
		})
		if err != nil {
			t.Fatalf("GetOAuthStatus: %v", err)
		}
		if st.GetState() == pb.OAuthState_OAUTH_STATE_CONNECTED {
			break
		}
		if st.GetState() == pb.OAuthState_OAUTH_STATE_ERROR {
			t.Fatalf("OAuth flow errored: %s", st.GetError())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st == nil || st.GetState() != pb.OAuthState_OAUTH_STATE_CONNECTED {
		t.Fatalf("did not reach CONNECTED in time; last=%+v", st)
	}
	if !strings.Contains(st.GetConnectedAs(), "watchfire-bot") {
		t.Errorf("connected_as: %q", st.GetConnectedAs())
	}

	// Verify persistence: bot token is in the keyring + metadata in
	// integrations.yaml.
	persisted, err := config.LoadIntegrations()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if persisted.Inbound.SlackBotTokenRef == "" {
		t.Fatal("SlackBotTokenRef not set after OAuth")
	}
	tok, ok := config.LookupIntegrationSecret(persisted.Inbound.SlackBotTokenRef)
	if !ok || tok != "xoxb-from-flow" {
		t.Errorf("keyring token: ok=%v val=%q", ok, tok)
	}
	if persisted.Inbound.SlackTeamName != "Test Team" {
		t.Errorf("team name: %q", persisted.Inbound.SlackTeamName)
	}
	if provider.restartHits == 0 {
		t.Errorf("expected at least one restartEchoServer call after OAuth")
	}
}
