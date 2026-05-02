package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

// memSecretStore is the test-injected secret backend. The real binary
// uses ~/.watchfire/.secrets; tests use this in-memory shim so they
// don't pollute the developer's home dir.
type memSecretStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemSecretStore() *memSecretStore {
	return &memSecretStore{m: make(map[string]string)}
}

func (s *memSecretStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

func (s *memSecretStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *memSecretStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// memSecretStoreAdapter satisfies the unexported `secretStore` interface
// in the config package via the exported SetSecretStoreForTest hook.
// Compiles only because the hook accepts the local interface shape.
type memSecretStoreAdapter struct{ inner *memSecretStore }

func (m *memSecretStoreAdapter) Get(key string) (string, bool) { return m.inner.Get(key) }
func (m *memSecretStoreAdapter) Set(key, value string) error    { return m.inner.Set(key, value) }
func (m *memSecretStoreAdapter) Delete(key string) error        { return m.inner.Delete(key) }

// withTempHomeIntegrations redirects $HOME so config paths resolve to a
// per-test directory; restores the original on teardown. Named to avoid
// colliding with the same helper in task_failed_test.go.
func withTempHomeIntegrations(t *testing.T) {
	t.Helper()
	orig := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", orig) })
	dir := t.TempDir()
	if err := os.Setenv("HOME", dir); err != nil {
		t.Fatalf("setenv HOME: %v", err)
	}
}

// TestSaveListRoundTripScrubsSecrets verifies that List → Save → List
// preserves all fields except the secret/URL field, which is scrubbed
// in the response (replaced with secret_set / url_set + masked label).
func TestSaveListRoundTripScrubsSecrets(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	ctx := context.Background()

	// Save a webhook with a secret + a Slack endpoint with a URL.
	_, err := svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Webhook{
			Webhook: &pb.WebhookIntegration{
				Id:    "hook-1",
				Label: "ops",
				Url:   "https://example.com/incoming",
				Secret: "topsecret",
				EnabledEvents: &pb.IntegrationEvents{
					TaskFailed: true, RunComplete: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("save webhook: %v", err)
	}

	_, err = svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Slack{
			Slack: &pb.SlackIntegration{
				Id:    "slack-1",
				Label: "alerts",
				Url:   "https://hooks.slack.com/services/T0/B0/abcdef",
				EnabledEvents: &pb.IntegrationEvents{
					RunComplete: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("save slack: %v", err)
	}

	// List + verify scrubbing.
	out, err := svc.ListIntegrations(ctx, &pb.ListIntegrationsRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Webhooks) != 1 {
		t.Fatalf("want 1 webhook, got %d", len(out.Webhooks))
	}
	w := out.Webhooks[0]
	if w.GetSecret() != "" {
		t.Errorf("webhook.secret should be scrubbed, got %q", w.GetSecret())
	}
	if !w.GetSecretSet() {
		t.Error("webhook.secret_set should be true")
	}
	if w.GetUrlLabel() == "" || w.GetUrlLabel() == w.GetUrl() {
		t.Errorf("webhook.url_label should be a masked variant, got %q", w.GetUrlLabel())
	}

	if len(out.Slack) != 1 {
		t.Fatalf("want 1 slack, got %d", len(out.Slack))
	}
	s := out.Slack[0]
	if s.GetUrl() != "" {
		t.Errorf("slack.url should be scrubbed, got %q", s.GetUrl())
	}
	if !s.GetUrlSet() {
		t.Error("slack.url_set should be true")
	}

	// Verify the secret is still resolvable through the keyring shim.
	if got, ok := mem.Get(config.SecretKeyForIntegration("hook-1", "secret")); !ok || got != "topsecret" {
		t.Errorf("expected webhook secret in store, got %q ok=%v", got, ok)
	}
	if got, ok := mem.Get(config.SecretKeyForIntegration("slack-1", "url")); !ok || got != "https://hooks.slack.com/services/T0/B0/abcdef" {
		t.Errorf("expected slack URL in store, got %q ok=%v", got, ok)
	}
}

// TestSaveSecretRotation ensures that saving with an empty secret on
// update preserves the existing keyring entry, while saving with a new
// secret rotates it.
func TestSaveSecretRotation(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	ctx := context.Background()

	// Initial save with secret v1.
	_, err := svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Webhook{
			Webhook: &pb.WebhookIntegration{
				Id:     "h",
				Url:    "https://e.x/",
				Secret: "v1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := mem.Get(config.SecretKeyForIntegration("h", "secret")); got != "v1" {
		t.Fatalf("expected v1 in store, got %q", got)
	}

	// Save with empty secret — preserves v1.
	_, err = svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Webhook{
			Webhook: &pb.WebhookIntegration{
				Id:    "h",
				Url:   "https://e.x/v2",
				Label: "renamed",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := mem.Get(config.SecretKeyForIntegration("h", "secret")); got != "v1" {
		t.Errorf("empty-secret save should preserve v1, got %q", got)
	}

	// Save with new secret — rotates.
	_, err = svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Webhook{
			Webhook: &pb.WebhookIntegration{
				Id:     "h",
				Url:    "https://e.x/v2",
				Secret: "v2",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := mem.Get(config.SecretKeyForIntegration("h", "secret")); got != "v2" {
		t.Errorf("new-secret save should rotate to v2, got %q", got)
	}
}

// TestDeleteRemovesSecret verifies that Delete clears the YAML entry +
// the keyring entry so we don't leak secrets after the user removes an
// integration.
func TestDeleteRemovesSecret(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	svc := newIntegrationsService()
	ctx := context.Background()

	if _, err := svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Slack{
			Slack: &pb.SlackIntegration{
				Id:  "s",
				Url: "https://hooks.slack.com/services/T/B/x",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := mem.Get(config.SecretKeyForIntegration("s", "url")); !ok {
		t.Fatal("URL not stored")
	}

	out, err := svc.DeleteIntegration(ctx, &pb.DeleteIntegrationRequest{
		Kind: pb.IntegrationKind_SLACK,
		Id:   "s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Slack) != 0 {
		t.Errorf("slack list should be empty after delete, got %d", len(out.Slack))
	}
	if _, ok := mem.Get(config.SecretKeyForIntegration("s", "url")); ok {
		t.Error("URL should be removed from store after delete")
	}
}

// TestTestIntegrationDelivers verifies that Test against an httptest
// server returns the success/failure shape correctly.
func TestTestIntegrationDelivers(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	// Success server.
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(okSrv.Close)

	// Failure server.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(failSrv.Close)

	svc := newIntegrationsService()
	ctx := context.Background()

	if _, err := svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Webhook{
			Webhook: &pb.WebhookIntegration{Id: "ok", Url: okSrv.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Webhook{
			Webhook: &pb.WebhookIntegration{Id: "fail", Url: failSrv.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.TestIntegration(ctx, &pb.TestIntegrationRequest{
		Kind: pb.IntegrationKind_WEBHOOK, Id: "ok",
	})
	if err != nil {
		t.Fatalf("test ok: %v", err)
	}
	if !resp.GetOk() || resp.GetStatusCode() != http.StatusOK {
		t.Errorf("expected success on 200, got ok=%v status=%d", resp.GetOk(), resp.GetStatusCode())
	}

	resp, err = svc.TestIntegration(ctx, &pb.TestIntegrationRequest{
		Kind: pb.IntegrationKind_WEBHOOK, Id: "fail",
	})
	if err != nil {
		t.Fatalf("test fail: %v", err)
	}
	if resp.GetOk() || resp.GetStatusCode() != http.StatusBadRequest {
		t.Errorf("expected failure on 400, got ok=%v status=%d", resp.GetOk(), resp.GetStatusCode())
	}
}

// TestTestIntegrationDiscordPostsAllKinds asserts that calling
// TestIntegration with a Discord endpoint POSTs three payloads (one per
// notification kind) — each parses as a Discord webhook envelope — and
// the response message names every kind. Pinned by the v7.0 task 0064
// acceptance criterion: "POSTs each notification kind through with a
// valid Discord webhook payload".
func TestTestIntegrationDiscordPostsAllKinds(t *testing.T) {
	withTempHomeIntegrations(t)
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	type captured struct {
		Embeds []map[string]any `json:"embeds"`
	}
	var (
		mu    sync.Mutex
		calls []captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c captured
		if err := json.Unmarshal(body, &c); err != nil {
			t.Errorf("server received non-JSON body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	svc := newIntegrationsService()
	ctx := context.Background()

	if _, err := svc.SaveIntegration(ctx, &pb.SaveIntegrationRequest{
		Payload: &pb.SaveIntegrationRequest_Discord{
			Discord: &pb.DiscordIntegration{Id: "d1", Url: srv.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.TestIntegration(ctx, &pb.TestIntegrationRequest{
		Kind: pb.IntegrationKind_DISCORD, Id: "d1",
	})
	if err != nil {
		t.Fatalf("test discord: %v", err)
	}
	if !resp.GetOk() {
		t.Errorf("expected ok=true, got %v with msg=%q", resp.GetOk(), resp.GetMessage())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("want 3 POSTs (one per kind), got %d", len(calls))
	}
	gotTitles := make([]string, 0, 3)
	for _, c := range calls {
		if len(c.Embeds) != 1 {
			t.Errorf("each POST should carry exactly one embed, got %d", len(c.Embeds))
			continue
		}
		title, _ := c.Embeds[0]["title"].(string)
		gotTitles = append(gotTitles, title)
	}
	sort.Strings(gotTitles)
	wantTitles := []string{
		"Run complete — Watchfire test",
		"Task failed — Watchfire test",
		"Watchfire — your week",
	}
	if len(gotTitles) != len(wantTitles) {
		t.Fatalf("titles len = %d, want %d (got %v)", len(gotTitles), len(wantTitles), gotTitles)
	}
	for i, want := range wantTitles {
		if gotTitles[i] != want {
			t.Errorf("titles[%d] = %q, want %q", i, gotTitles[i], want)
		}
	}
}
