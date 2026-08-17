package server

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

// minimalServer builds a Server literal with just the managers the
// inbound registration path touches — no listener, no watcher.
func minimalServer() *Server {
	return &Server{
		taskManager:  task.NewManager(),
		agentManager: agent.NewManager(),
		notifyBus:    notify.NewBus(),
	}
}

// startInboundEcho registers the provider handlers against a fresh Echo
// server bound to a probe-allocated loopback port and returns its base
// URL. Mirrors the harness in echo/server_test.go.
func startInboundEcho(t *testing.T, s *Server, in models.InboundConfig) string {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}
	in.ListenAddr = addr
	in.RateLimitPerMin = -1

	srv := echo.New(in, log.New(io.Discard, "", 0))
	s.registerInboundProviderHandlers(srv, in)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !srv.Listening() {
		select {
		case err := <-done:
			cancel()
			t.Fatalf("echo server exited before listening: %v", err)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !srv.Listening() {
		cancel()
		t.Fatalf("echo server did not start listening on %s", addr)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("echo server did not shut down within drain window")
		}
	})
	return "http://" + addr
}

// TestNoInboundConfigRegistersNoProviderRoutes is the regression gate
// for "registration ≠ activation": with a zero-value InboundConfig the
// echo mux must serve exactly the same routes as before this task —
// the health endpoint and nothing else. Every provider route,
// including the newly-wired Slack/Discord ones, must 404.
func TestNoInboundConfigRegistersNoProviderRoutes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := startInboundEcho(t, minimalServer(), models.InboundConfig{})

	for _, route := range []string{
		"/echo/github/webhook",
		"/echo/gitlab/webhook",
		"/echo/bitbucket/webhook",
		"/echo/slack/commands",
		"/echo/slack/interactivity",
		"/echo/discord/interactions",
	} {
		resp, err := http.Post(base+route, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST %s: %v", route, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d with empty inbound config, want 404", route, resp.StatusCode)
		}
	}

	resp, err := http.Get(base + "/echo/health")
	if err != nil {
		t.Fatalf("GET /echo/health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /echo/health = %d, want 200", resp.StatusCode)
	}
}

// TestConfiguredSlackDiscordRoutesAreServed proves the flip side of the
// gate: with the secret references present the new routes exist on the
// mux (an unsigned request reaches the handler and is rejected there
// with 401, not 404 by the mux).
func TestConfiguredSlackDiscordRoutesAreServed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := startInboundEcho(t, minimalServer(), models.InboundConfig{
		SlackSecretRef:      "test.slack.signing",
		DiscordPublicKeyRef: "test.discord.pubkey",
	})

	for _, route := range []string{
		"/echo/slack/commands",
		"/echo/slack/interactivity",
		"/echo/discord/interactions",
	} {
		resp, err := http.Post(base+route, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST %s: %v", route, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unsigned POST %s = %d, want 401 (route registered, signature rejected)", route, resp.StatusCode)
		}
	}
}

// signSlackTest computes Slack's v0 HMAC over the canonical basestring.
func signSlackTest(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// TestSlackSlashCommandEndToEnd drives a signed `/watchfire` slash
// command through the live echo HTTP server into echo.Route with the
// production CommandContext: status renders the mapped project, and
// retry flips a failed task back to ready on disk.
func TestSlackSlashCommandEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	const signingSecret = "slack-signing-s3cret"
	if err := mem.Set("test.slack.signing", signingSecret); err != nil {
		t.Fatalf("store secret: %v", err)
	}

	// A project opted into Slack via its per-project channel binding,
	// with one failed task on disk.
	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject("p-slack", "Chat Ops", projectPath)
	proj.Integrations.SlackChannel = "#eng"
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(proj.ProjectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	failed := false
	completed := time.Now().UTC()
	if err := config.SaveTask(projectPath, &models.Task{
		Version: 1, TaskID: "slack001", TaskNumber: 5, Title: "Fix flaky test",
		Status: models.TaskStatusDone, Success: &failed, FailureReason: "boom",
		CompletedAt: &completed, CreatedAt: completed, UpdatedAt: completed,
	}); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	base := startInboundEcho(t, minimalServer(), models.InboundConfig{
		SlackSecretRef: "test.slack.signing",
	})

	post := func(text, triggerID string) string {
		t.Helper()
		form := url.Values{}
		form.Set("command", "/watchfire")
		form.Set("text", text)
		form.Set("team_id", "T-1")
		form.Set("user_id", "U-1")
		form.Set("trigger_id", triggerID)
		body := form.Encode()
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		req, err := http.NewRequest(http.MethodPost, base+"/echo/slack/commands", strings.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Slack-Request-Timestamp", ts)
		req.Header.Set("X-Slack-Signature", signSlackTest(signingSecret, ts, []byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST slack command: %v", err)
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("slack command %q = %d: %s", text, resp.StatusCode, payload)
		}
		return string(payload)
	}

	// /watchfire status → the mapped project renders.
	statusBody := post("status", "trig-status-1")
	if !strings.Contains(statusBody, "Chat Ops") {
		t.Fatalf("status response missing project name: %s", statusBody)
	}

	// /watchfire retry 5 → routed through Retry, task flipped on disk.
	retryBody := post("retry 5", "trig-retry-1")
	if !strings.Contains(retryBody, "Retrying task #0005") || !strings.Contains(retryBody, "Fix flaky test") {
		t.Fatalf("retry response unexpected: %s", retryBody)
	}
	tk, err := config.LoadTask(projectPath, 5)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Status != models.TaskStatusReady || tk.Success != nil || tk.FailureReason != "" {
		t.Fatalf("task after retry = status %s success %v reason %q, want clean ready", tk.Status, tk.Success, tk.FailureReason)
	}
}

// TestDiscordInteractionEndToEnd drives a signed Discord application-
// command interaction through the live echo HTTP server into echo.Route
// with the production CommandContext and asserts the rendered type-4
// interaction response.
func TestDiscordInteractionEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem := newMemSecretStore()
	config.SetSecretStoreForTest(&memSecretStoreAdapter{inner: mem})
	t.Cleanup(func() { config.SetSecretStoreForTest(nil) })

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if err := mem.Set("test.discord.pubkey", hex.EncodeToString(pub)); err != nil {
		t.Fatalf("store pubkey: %v", err)
	}

	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject("p-discord", "Guild Ops", projectPath)
	proj.Integrations.DiscordGuildID = "guild-1"
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(proj.ProjectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	now := time.Now().UTC()
	if err := config.SaveTask(projectPath, &models.Task{
		Version: 1, TaskID: "disc0001", TaskNumber: 3, Title: "Ship the bridge",
		Status: models.TaskStatusReady, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	base := startInboundEcho(t, minimalServer(), models.InboundConfig{
		DiscordPublicKeyRef: "test.discord.pubkey",
	})

	// The registrar registers `status` as a top-level command
	// (internal/daemon/discord/commands.go), so `data.name` is the
	// subcommand the router dispatches on.
	body, err := json.Marshal(map[string]any{
		"id":       "interaction-1",
		"type":     2,
		"guild_id": "guild-1",
		"member":   map[string]any{"user": map[string]any{"id": "u-1", "username": "nuno"}},
		"data":     map[string]any{"name": "status"},
	})
	if err != nil {
		t.Fatalf("marshal interaction: %v", err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(priv, append([]byte(ts), body...))

	req, err := http.NewRequest(http.MethodPost, base+"/echo/discord/interactions", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST discord interaction: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discord interaction = %d: %s", resp.StatusCode, payload)
	}

	var rendered struct {
		Type int             `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &rendered); err != nil {
		t.Fatalf("unmarshal response %s: %v", payload, err)
	}
	if rendered.Type != 4 {
		t.Fatalf("response type = %d, want 4 (CHANNEL_MESSAGE_WITH_SOURCE): %s", rendered.Type, payload)
	}
	if !strings.Contains(string(rendered.Data), "Guild Ops") {
		t.Fatalf("status response missing mapped project: %s", payload)
	}
	if !strings.Contains(string(rendered.Data), fmt.Sprintf("#%04d", 3)) {
		t.Fatalf("status response missing active task number: %s", payload)
	}
}
