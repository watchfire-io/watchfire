package echo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// signedSlackCommandRequest builds a Slack slash-command POST against
// the handler. fields are the form fields Slack POSTs (`command`,
// `text`, `team_id`, …); the helper urlencodes them, signs the body,
// and attaches the v0 headers.
func signedSlackCommandRequest(t *testing.T, secret []byte, fields map[string]string, ts string) *http.Request {
	t.Helper()
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	body := []byte(form.Encode())
	sig, _ := signSlack(secret, body, ts)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/commands", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	return req
}

func slackCommandsTestCommandContext(teamID, userID string) CommandContext {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	startedAgo := now.Add(-3 * time.Minute)
	return CommandContext{
		TeamID: teamID,
		UserID: userID,
		Now:    func() time.Time { return now },
		FindProjects: func(ctx context.Context) ([]ProjectInfo, error) {
			return []ProjectInfo{{ID: "proj-abc", Name: "Watchfire", Color: "#3b82f6"}}, nil
		},
		LookupTask: func(ctx context.Context, ref string) (*models.Task, ProjectInfo, error) {
			n, _, _ := ParseTaskRef(ref)
			if n == 0 {
				return nil, ProjectInfo{}, ErrTaskNotFound
			}
			return &models.Task{TaskNumber: n, Title: "wire it up", Status: models.TaskStatusReady},
				ProjectInfo{ID: "proj-abc", Name: "Watchfire"}, nil
		},
		ListTopActiveTasks: func(ctx context.Context, projectID string, limit int) ([]*models.Task, error) {
			return []*models.Task{{TaskNumber: 7, Title: "wire it up", StartedAt: &startedAgo, Status: models.TaskStatusReady}}, nil
		},
		Retry:  func(ctx context.Context, projectID string, taskNumber int) error { return nil },
		Cancel: func(ctx context.Context, projectID string, taskNumber int, reason string) error { return nil },
	}
}

func newSlackCommandsHandler(t *testing.T, secret []byte, opts ...func(*SlackCommandsHandlerConfig)) *slackCommandsHandler {
	t.Helper()
	cfg := SlackCommandsHandlerConfig{
		ResolveSigningSecret: func() ([]byte, error) { return secret, nil },
		Idempotency:          NewCache(0, 0),
		CommandContextFor: func(teamID, userID string) CommandContext {
			return slackCommandsTestCommandContext(teamID, userID)
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewSlackCommandsHandler(cfg).(*slackCommandsHandler)
}

func slackCommandsTS(t *testing.T) string {
	t.Helper()
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	return strconv.FormatInt(now.Unix(), 10)
}

// ---- /watchfire status -----------------------------------------------

func TestSlackCommandsStatus(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var deliveries atomic.Int32
	h := newSlackCommandsHandler(t, secret, func(cfg *SlackCommandsHandlerConfig) {
		cfg.RecordDelivery = func() { deliveries.Add(1) }
	})

	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "/watchfire",
		"text":       "status",
		"team_id":    "T123",
		"user_id":    "U456",
		"trigger_id": "trig-status-1",
		"channel_id": "C789",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("malformed response: %v\n%s", err, w.Body.String())
	}
	if doc["response_type"] != "ephemeral" {
		t.Errorf("expected response_type=ephemeral for status, got %v", doc["response_type"])
	}
	blocks, ok := doc["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("expected blocks array, got %v", doc["blocks"])
	}
	header, _ := blocks[0].(map[string]any)
	if header["type"] != "header" {
		t.Errorf("expected first block to be header, got %v", header["type"])
	}
	if got := deliveries.Load(); got != 1 {
		t.Errorf("expected RecordDelivery to fire once, got %d", got)
	}
}

// ---- /watchfire retry <task> -----------------------------------------

func TestSlackCommandsRetry(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCalled atomic.Bool
	var capturedTask int
	h := newSlackCommandsHandler(t, secret, func(cfg *SlackCommandsHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackCommandsTestCommandContext(teamID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCalled.Store(true)
				capturedTask = taskNumber
				return nil
			}
			return cc
		}
	})

	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "/watchfire",
		"text":       "retry 42",
		"team_id":    "T123",
		"user_id":    "U456",
		"trigger_id": "trig-retry-1",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !retryCalled.Load() {
		t.Fatalf("expected Retry callback to fire")
	}
	if capturedTask != 42 {
		t.Errorf("captured taskNumber = %d, want 42", capturedTask)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	if doc["response_type"] != "in_channel" {
		t.Errorf("expected in_channel for retry confirmation, got %v", doc["response_type"])
	}
	text, _ := doc["text"].(string)
	if !strings.Contains(text, "Retrying task") {
		t.Errorf("expected confirmation in text, got %q", text)
	}
}

// ---- /watchfire cancel <task> ----------------------------------------

func TestSlackCommandsCancel(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var cancelCalled atomic.Bool
	h := newSlackCommandsHandler(t, secret, func(cfg *SlackCommandsHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackCommandsTestCommandContext(teamID, userID)
			cc.Cancel = func(ctx context.Context, projectID string, taskNumber int, reason string) error {
				cancelCalled.Store(true)
				if reason != "" {
					return fmt.Errorf("expected empty reason from slash command, got %q", reason)
				}
				return nil
			}
			return cc
		}
	})

	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "/watchfire",
		"text":       "cancel 7",
		"team_id":    "T123",
		"user_id":    "U456",
		"trigger_id": "trig-cancel-1",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !cancelCalled.Load() {
		t.Fatalf("expected Cancel callback to fire")
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	if doc["response_type"] != "in_channel" {
		t.Errorf("expected in_channel for cancel confirmation, got %v", doc["response_type"])
	}
}

// ---- bare `/watchfire` (no subcommand) defaults to status -----------

func TestSlackCommandsBareInvocationDefaultsToStatus(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackCommandsHandler(t, secret)
	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "/watchfire",
		"text":       "",
		"team_id":    "T",
		"user_id":    "U",
		"trigger_id": "trig-default",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	blocks, _ := doc["blocks"].([]any)
	if len(blocks) == 0 {
		t.Fatalf("expected status blocks for bare invocation, got nothing")
	}
}

// ---- unknown subcommand surfaces help text --------------------------

func TestSlackCommandsUnknownSubcommandReturnsHelp(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackCommandsHandler(t, secret)
	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "/watchfire",
		"text":       "frobnicate",
		"team_id":    "T",
		"user_id":    "U",
		"trigger_id": "trig-help",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	blocks, _ := doc["blocks"].([]any)
	if len(blocks) == 0 {
		t.Fatalf("expected help blocks, got nothing")
	}
	first, _ := blocks[0].(map[string]any)
	textObj, _ := first["text"].(map[string]any)
	headerText, _ := textObj["text"].(string)
	if !strings.Contains(headerText, "Unknown") {
		t.Errorf("expected Unknown command header, got %q", headerText)
	}
}

// ---- bad signature returns 401 --------------------------------------

func TestSlackCommandsBadSignature(t *testing.T) {
	secret := []byte("supersecret")
	wrong := []byte("nope")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackCommandsHandler(t, secret)
	form := url.Values{}
	form.Set("command", "/watchfire")
	form.Set("text", "status")
	body := []byte(form.Encode())
	sig, _ := signSlack(wrong, body, ts) // signed with wrong secret

	req := httptest.NewRequest(http.MethodPost, "/echo/slack/commands", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad signature, got %d", w.Code)
	}
}

// ---- missing signature headers --------------------------------------

func TestSlackCommandsMissingHeaders(t *testing.T) {
	secret := []byte("supersecret")
	h := newSlackCommandsHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/commands",
		strings.NewReader("command=%2Fwatchfire&text=status"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing headers, got %d", w.Code)
	}
}

// ---- missing secret returns 503 -------------------------------------

func TestSlackCommandsSecretNotConfigured(t *testing.T) {
	cfg := SlackCommandsHandlerConfig{
		ResolveSigningSecret: func() ([]byte, error) { return nil, fmt.Errorf("not set") },
		Idempotency:          NewCache(0, 0),
		CommandContextFor: func(teamID, userID string) CommandContext {
			return slackCommandsTestCommandContext(teamID, userID)
		},
	}
	h := NewSlackCommandsHandler(cfg)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/commands",
		strings.NewReader("command=%2Fwatchfire&text=status"))
	req.Header.Set("X-Slack-Signature", "v0=00")
	req.Header.Set("X-Slack-Request-Timestamp", "0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when secret missing, got %d", w.Code)
	}
}

// ---- replay returns 200 + no double-act -----------------------------

func TestSlackCommandsReplayDoesNotDoubleAct(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCount atomic.Int32
	h := newSlackCommandsHandler(t, secret, func(cfg *SlackCommandsHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackCommandsTestCommandContext(teamID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCount.Add(1)
				return nil
			}
			return cc
		}
	})

	for i := 0; i < 2; i++ {
		req := signedSlackCommandRequest(t, secret, map[string]string{
			"command":    "/watchfire",
			"text":       "retry 5",
			"team_id":    "T",
			"user_id":    "U",
			"trigger_id": "replay-1",
		}, ts)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i, w.Code)
		}
	}
	if got := retryCount.Load(); got != 1 {
		t.Fatalf("expected Retry to fire exactly once on replay, got %d", got)
	}
}

// ---- replay refunds rate-limit token --------------------------------

func TestSlackCommandsReplayRefundsRateLimit(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var refunds atomic.Int32
	h := newSlackCommandsHandler(t, secret, func(cfg *SlackCommandsHandlerConfig) {
		cfg.RefundOnReplay = func(r *http.Request) { refunds.Add(1) }
	})

	for i := 0; i < 2; i++ {
		req := signedSlackCommandRequest(t, secret, map[string]string{
			"command":    "/watchfire",
			"text":       "status",
			"team_id":    "T",
			"user_id":    "U",
			"trigger_id": "replay-refund",
		}, ts)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d", i, w.Code)
		}
	}
	if got := refunds.Load(); got != 1 {
		t.Errorf("expected exactly one refund on replay, got %d", got)
	}
}

// ---- malformed body -------------------------------------------------

func TestSlackCommandsMalformedBody(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackCommandsHandler(t, secret)
	body := []byte("%ZZnot-form-encoded")
	sig, _ := signSlack(secret, body, ts)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/commands", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed body, got %d", w.Code)
	}
}

// ---- missing command field ------------------------------------------

func TestSlackCommandsMissingCommandField(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackCommandsHandler(t, secret)
	req := signedSlackCommandRequest(t, secret, map[string]string{
		"text":       "status",
		"team_id":    "T",
		"user_id":    "U",
		"trigger_id": "trig-missing",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing command field, got %d", w.Code)
	}
}

// ---- command without leading slash ----------------------------------

func TestSlackCommandsCommandMissingSlash(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackCommandsHandler(t, secret)
	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "watchfire", // missing leading /
		"text":       "status",
		"team_id":    "T",
		"user_id":    "U",
		"trigger_id": "trig-noslash",
	}, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on command missing leading slash, got %d", w.Code)
	}
}

// ---- timestamp drift returns 401 ------------------------------------

func TestSlackCommandsStaleTimestampRejected(t *testing.T) {
	secret := []byte("supersecret")
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	staleTS := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	h := newSlackCommandsHandler(t, secret)
	req := signedSlackCommandRequest(t, secret, map[string]string{
		"command":    "/watchfire",
		"text":       "status",
		"team_id":    "T",
		"user_id":    "U",
		"trigger_id": "trig-stale",
	}, staleTS)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on stale timestamp, got %d", w.Code)
	}
}

// ---- splitSlackCommandText helper -----------------------------------

func TestSplitSlackCommandText(t *testing.T) {
	cases := []struct {
		in           string
		wantSubcmd   string
		wantRest     string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{"status", "status", ""},
		{"retry 42", "retry", "42"},
		{"cancel  7  ", "cancel", "7"},
		{"cancel\t7", "cancel", "7"},
		{"status proj-abc more", "status", "proj-abc more"},
	}
	for _, c := range cases {
		gotSubcmd, gotRest := splitSlackCommandText(c.in)
		if gotSubcmd != c.wantSubcmd || gotRest != c.wantRest {
			t.Errorf("splitSlackCommandText(%q) = (%q,%q), want (%q,%q)",
				c.in, gotSubcmd, gotRest, c.wantSubcmd, c.wantRest)
		}
	}
}

// ---- client_msg_id fallback for events ------------------------------

func TestSlackCommandsClientMsgIDFallbackForIdempotency(t *testing.T) {
	secret := []byte("supersecret")
	ts := slackCommandsTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCount atomic.Int32
	h := newSlackCommandsHandler(t, secret, func(cfg *SlackCommandsHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackCommandsTestCommandContext(teamID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCount.Add(1)
				return nil
			}
			return cc
		}
	})

	// no trigger_id, but client_msg_id present — replay should still
	// short-circuit.
	for i := 0; i < 2; i++ {
		req := signedSlackCommandRequest(t, secret, map[string]string{
			"command":       "/watchfire",
			"text":          "retry 9",
			"team_id":       "T",
			"user_id":       "U",
			"client_msg_id": "msg-fallback",
		}, ts)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i, w.Code)
		}
	}
	if got := retryCount.Load(); got != 1 {
		t.Fatalf("expected Retry to fire exactly once via client_msg_id fallback, got %d", got)
	}
}
