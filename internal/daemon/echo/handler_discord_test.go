package echo

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// signedRequest builds a Discord-shaped signed POST against the
// handler. body is the raw JSON the test wants the handler to see;
// the helper signs `timestamp || body` with priv and attaches the
// hex-encoded signature + timestamp headers Discord expects.
func signedRequest(t *testing.T, priv ed25519.PrivateKey, body []byte, ts string) *http.Request {
	t.Helper()
	sig := ed25519.Sign(priv, append([]byte(ts), body...))
	req := httptest.NewRequest(http.MethodPost, "/echo/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newTestHandler(t *testing.T, pub ed25519.PublicKey) (*discordHandler, *Cache) {
	t.Helper()
	cache := NewCache(0, 0)
	cfg := DiscordHandlerConfig{
		ResolvePublicKey: func() (ed25519.PublicKey, error) { return pub, nil },
		Idempotency:      cache,
		CommandContextFor: func(guildID, userID string) CommandContext {
			return testCommandContext(guildID, userID)
		},
	}
	h := NewDiscordHandler(cfg).(*discordHandler)
	return h, cache
}

func testCommandContext(guildID, userID string) CommandContext {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	startedAgo := now.Add(-3 * time.Minute)
	return CommandContext{
		GuildID: guildID,
		UserID:  userID,
		Now:     func() time.Time { return now },
		FindProjects: func(ctx context.Context) ([]ProjectInfo, error) {
			return []ProjectInfo{{ID: "proj-a", Name: "alpha", Color: "#ff0000"}}, nil
		},
		LookupTask: func(ctx context.Context, ref string) (*models.Task, ProjectInfo, error) {
			n, _, _ := ParseTaskRef(ref)
			if n == 0 {
				return nil, ProjectInfo{}, ErrTaskNotFound
			}
			return &models.Task{TaskNumber: n, Title: "build the thing", Status: models.TaskStatusReady}, ProjectInfo{ID: "proj-a", Name: "alpha"}, nil
		},
		ListTopActiveTasks: func(ctx context.Context, projectID string, limit int) ([]*models.Task, error) {
			return []*models.Task{{TaskNumber: 7, Title: "wire it up", StartedAt: &startedAgo, Status: models.TaskStatusReady}}, nil
		},
		Retry:  func(ctx context.Context, projectID string, taskNumber int) error { return nil },
		Cancel: func(ctx context.Context, projectID string, taskNumber int, reason string) error { return nil },
	}
}

func TestDiscordHandlerPing(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	h, _ := newTestHandler(t, pub)

	body := []byte(`{"id":"100","application_id":"app","type":1}`)
	req := signedRequest(t, priv, body, strconv.FormatInt(now.Unix(), 10))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("malformed response: %v", err)
	}
	if doc["type"].(float64) != 1 {
		t.Fatalf("expected pong type 1, got %v", doc["type"])
	}
}

func TestDiscordHandlerStatusCommand(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	h, _ := newTestHandler(t, pub)

	body := []byte(`{
		"id":"int-1",
		"application_id":"app",
		"type":2,
		"guild_id":"guild-1",
		"data":{"id":"cmd","name":"status","options":[]}
	}`)
	req := signedRequest(t, priv, body, strconv.FormatInt(now.Unix(), 10))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("malformed response: %v", err)
	}
	if doc["type"].(float64) != 4 {
		t.Fatalf("expected type 4 channel message, got %v", doc["type"])
	}
	embeds := doc["data"].(map[string]any)["embeds"].([]any)
	if len(embeds) == 0 {
		t.Fatalf("expected at least one embed")
	}
}

func TestDiscordHandlerRetryCommand(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	var called atomic.Bool
	cfg := DiscordHandlerConfig{
		ResolvePublicKey: func() (ed25519.PublicKey, error) { return pub, nil },
		Idempotency:      NewCache(0, 0),
		CommandContextFor: func(guildID, userID string) CommandContext {
			cc := testCommandContext(guildID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				called.Store(true)
				if taskNumber != 5 {
					return fmt.Errorf("expected taskNumber 5, got %d", taskNumber)
				}
				return nil
			}
			return cc
		},
	}
	h := NewDiscordHandler(cfg)

	body := []byte(`{
		"id":"int-2",
		"type":2,
		"guild_id":"g",
		"data":{"name":"retry","options":[{"name":"task","type":3,"value":"5"}]}
	}`)
	req := signedRequest(t, priv, body, strconv.FormatInt(now.Unix(), 10))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !called.Load() {
		t.Fatalf("expected Retry callback to fire")
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	content, _ := doc["data"].(map[string]any)["content"].(string)
	if !strings.Contains(content, "Retrying task") {
		t.Fatalf("expected confirmation in content, got %q", content)
	}
}

func TestDiscordHandlerCancelCommand(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	var called atomic.Bool
	cfg := DiscordHandlerConfig{
		ResolvePublicKey: func() (ed25519.PublicKey, error) { return pub, nil },
		Idempotency:      NewCache(0, 0),
		CommandContextFor: func(guildID, userID string) CommandContext {
			cc := testCommandContext(guildID, userID)
			cc.Cancel = func(ctx context.Context, projectID string, taskNumber int, reason string) error {
				called.Store(true)
				return nil
			}
			return cc
		},
	}
	h := NewDiscordHandler(cfg)

	body := []byte(`{
		"id":"int-3",
		"type":2,
		"guild_id":"g",
		"data":{"name":"cancel","options":[{"name":"task","type":3,"value":"7"}]}
	}`)
	req := signedRequest(t, priv, body, strconv.FormatInt(now.Unix(), 10))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !called.Load() {
		t.Fatalf("expected Cancel callback to fire")
	}
}

func TestDiscordHandlerBadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	h, _ := newTestHandler(t, pub)
	body := []byte(`{"id":"x","type":1}`)
	req := signedRequest(t, otherPriv, body, strconv.FormatInt(now.Unix(), 10)) // signed by different key
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestDiscordHandlerMissingHeaders(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	h, _ := newTestHandler(t, pub)
	req := httptest.NewRequest(http.MethodPost, "/echo/discord/interactions", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing headers, got %d", w.Code)
	}
}

func TestDiscordHandlerReplay(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCount atomic.Int32
	cfg := DiscordHandlerConfig{
		ResolvePublicKey: func() (ed25519.PublicKey, error) { return pub, nil },
		Idempotency:      NewCache(0, 0),
		CommandContextFor: func(guildID, userID string) CommandContext {
			cc := testCommandContext(guildID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCount.Add(1)
				return nil
			}
			return cc
		},
	}
	h := NewDiscordHandler(cfg)

	body := []byte(`{
		"id":"replay-1",
		"type":2,
		"guild_id":"g",
		"data":{"name":"retry","options":[{"name":"task","type":3,"value":"5"}]}
	}`)

	for i := 0; i < 2; i++ {
		req := signedRequest(t, priv, body, strconv.FormatInt(now.Unix(), 10))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d (%s)", i, w.Code, w.Body.String())
		}
	}
	if got := retryCount.Load(); got != 1 {
		t.Fatalf("expected Retry to fire exactly once on replay, got %d", got)
	}
}

func TestDiscordHandlerUnsupportedType(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	h, _ := newTestHandler(t, pub)
	body := []byte(`{"id":"comp-1","type":3}`) // MESSAGE_COMPONENT
	req := signedRequest(t, priv, body, strconv.FormatInt(now.Unix(), 10))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (polite ack), got %d", w.Code)
	}
	got, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(got), "Not supported") {
		t.Fatalf("expected polite ack content, got %s", got)
	}
}

func TestDiscordHandlerKeyNotConfigured(t *testing.T) {
	cfg := DiscordHandlerConfig{
		ResolvePublicKey: func() (ed25519.PublicKey, error) { return nil, fmt.Errorf("not set") },
		Idempotency:      NewCache(0, 0),
		CommandContextFor: func(guildID, userID string) CommandContext {
			return testCommandContext(guildID, userID)
		},
	}
	h := NewDiscordHandler(cfg)
	req := httptest.NewRequest(http.MethodPost, "/echo/discord/interactions", strings.NewReader(`{}`))
	req.Header.Set("X-Signature-Ed25519", "00")
	req.Header.Set("X-Signature-Timestamp", "0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when public key missing, got %d", w.Code)
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	srv := New(models.InboundConfig{ListenAddr: "127.0.0.1:0"}, nil)
	req := httptest.NewRequest(http.MethodGet, "/echo/health", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	if listening, _ := doc["listening"].(bool); !listening {
		t.Fatalf("expected listening:true in health response")
	}
}

func TestServerUnknownRoute(t *testing.T) {
	srv := New(models.InboundConfig{ListenAddr: "127.0.0.1:0"}, nil)
	req := httptest.NewRequest(http.MethodPost, "/echo/unknown", strings.NewReader(""))
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
