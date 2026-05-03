package echo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// signSlack mirrors Slack's `v0` signing surface — the helper signs the
// raw urlencoded body the test sends, returning the headers Slack would
// emit alongside the request.
func signSlack(secret, body []byte, ts string) (sig, header string) {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil)), ts
}

// signedSlackRequest builds a Slack interactivity POST against the
// handler. The body is the form-encoded `payload=<JSON>` shape Slack
// emits for both block_actions and view_submission.
func signedSlackRequest(t *testing.T, secret []byte, payloadJSON string, ts string) *http.Request {
	t.Helper()
	form := url.Values{}
	form.Set("payload", payloadJSON)
	body := []byte(form.Encode())
	sig, _ := signSlack(secret, body, ts)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/interactivity", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	return req
}

func slackTestCommandContext(teamID, userID string) CommandContext {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
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
			return &models.Task{TaskNumber: n, Title: "Build the Discord adapter", Status: models.TaskStatusReady},
				ProjectInfo{ID: "proj-abc", Name: "Watchfire"}, nil
		},
		ListTopActiveTasks: func(ctx context.Context, projectID string, limit int) ([]*models.Task, error) {
			return nil, nil
		},
		Retry:  func(ctx context.Context, projectID string, taskNumber int) error { return nil },
		Cancel: func(ctx context.Context, projectID string, taskNumber int, reason string) error { return nil },
	}
}

func newSlackHandler(t *testing.T, secret []byte, opts ...func(*SlackHandlerConfig)) *slackHandler {
	t.Helper()
	cfg := SlackHandlerConfig{
		ResolveSigningSecret: func() ([]byte, error) { return secret, nil },
		Idempotency:          NewCache(0, 0),
		CommandContextFor: func(teamID, userID string) CommandContext {
			return slackTestCommandContext(teamID, userID)
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewSlackInteractivityHandler(cfg).(*slackHandler)
}

func nowSlackTS(t *testing.T) string {
	t.Helper()
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	return strconv.FormatInt(now.Unix(), 10)
}

// ---- block_actions: retry button --------------------------------------

func TestSlackHandlerRetryButton(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCalled atomic.Bool
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackTestCommandContext(teamID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCalled.Store(true)
				if projectID != "proj-abc" {
					return fmt.Errorf("expected project proj-abc, got %s", projectID)
				}
				if taskNumber != 42 {
					return fmt.Errorf("expected task 42, got %d", taskNumber)
				}
				return nil
			}
			return cc
		}
	})

	payload := `{
		"type":"block_actions",
		"team":{"id":"T123"},
		"user":{"id":"U456","username":"alice"},
		"trigger_id":"trig-1",
		"actions":[{"action_id":"watchfire_retry","value":"proj-abc|42","type":"button","block_id":"blk"}]
	}`

	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !retryCalled.Load() {
		t.Fatalf("expected Retry callback to fire")
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("malformed response: %v\n%s", err, w.Body.String())
	}
	text, _ := doc["text"].(string)
	if !strings.Contains(text, "Retrying task") {
		t.Errorf("expected 'Retrying task' in text, got %q", text)
	}
}

// ---- block_actions: cancel button (no modal) --------------------------

func TestSlackHandlerCancelButtonWithoutModalCancelsImmediately(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var cancelReason string
	var cancelCalled atomic.Bool
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackTestCommandContext(teamID, userID)
			cc.Cancel = func(ctx context.Context, projectID string, taskNumber int, reason string) error {
				cancelCalled.Store(true)
				cancelReason = reason
				return nil
			}
			return cc
		}
	})

	payload := `{
		"type":"block_actions",
		"team":{"id":"T123"},
		"user":{"id":"U456"},
		"trigger_id":"trig-2",
		"actions":[{"action_id":"watchfire_cancel","value":"proj-abc|42","type":"button"}]
	}`

	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !cancelCalled.Load() {
		t.Fatalf("expected Cancel callback to fire when OpenModal is nil")
	}
	if cancelReason != "" {
		t.Errorf("expected empty reason on no-modal cancel, got %q", cancelReason)
	}
}

// ---- block_actions: cancel button (with modal) ------------------------

func TestSlackHandlerCancelButtonOpensModalWhenWired(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var openTriggerID string
	var openView map[string]any
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.OpenModal = func(ctx context.Context, triggerID string, view map[string]any) error {
			openTriggerID = triggerID
			openView = view
			return nil
		}
	})

	payload := `{
		"type":"block_actions",
		"team":{"id":"T123"},
		"user":{"id":"U456"},
		"trigger_id":"trig-3",
		"actions":[{"action_id":"watchfire_cancel","value":"proj-abc|42","type":"button"}]
	}`

	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if openTriggerID != "trig-3" {
		t.Errorf("OpenModal triggerID = %q, want trig-3", openTriggerID)
	}
	if openView["callback_id"] != cancelModalCallbackID {
		t.Errorf("OpenModal view callback_id = %v, want %s", openView["callback_id"], cancelModalCallbackID)
	}
	if openView["private_metadata"] != "proj-abc|42" {
		t.Errorf("OpenModal view private_metadata = %v", openView["private_metadata"])
	}
}

// ---- view_submission: modal submit cancels with reason ---------------

func TestSlackHandlerViewSubmissionCancelsWithReason(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var capturedReason string
	var capturedProject string
	var capturedTask int
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackTestCommandContext(teamID, userID)
			cc.Cancel = func(ctx context.Context, projectID string, taskNumber int, reason string) error {
				capturedReason = reason
				capturedProject = projectID
				capturedTask = taskNumber
				return nil
			}
			return cc
		}
	})

	payload := `{
		"type":"view_submission",
		"team":{"id":"T123"},
		"user":{"id":"U456"},
		"view":{
			"id":"V_001",
			"callback_id":"watchfire_cancel_reason",
			"private_metadata":"proj-abc|42",
			"state":{
				"values":{
					"reason_block":{
						"reason_input":{"type":"plain_text_input","value":"flaky network in CI"}
					}
				}
			}
		}
	}`

	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if capturedProject != "proj-abc" || capturedTask != 42 {
		t.Errorf("capture project=%s task=%d, want proj-abc/42", capturedProject, capturedTask)
	}
	if capturedReason != "flaky network in CI" {
		t.Errorf("captured reason = %q", capturedReason)
	}
}

func TestSlackHandlerViewSubmissionMissingMetadataReturnsError(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackHandler(t, secret)
	payload := `{
		"type":"view_submission",
		"team":{"id":"T"},
		"user":{"id":"U"},
		"view":{
			"id":"V_002",
			"callback_id":"watchfire_cancel_reason",
			"private_metadata":"",
			"state":{"values":{}}
		}
	}`
	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Slack expects 200 + a `response_action: errors` body for bad input
	// rather than 4xx (4xx surfaces a generic "submission failed" toast).
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	if doc["response_action"] != "errors" {
		t.Errorf("response_action = %v, want errors", doc["response_action"])
	}
}

// ---- signature verification -------------------------------------------

func TestSlackHandlerBadSignature(t *testing.T) {
	secret := []byte("supersecret")
	wrongSecret := []byte("definitelynotit")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackHandler(t, secret)

	form := url.Values{}
	form.Set("payload", `{"type":"block_actions"}`)
	body := []byte(form.Encode())
	sig, _ := signSlack(wrongSecret, body, ts) // signed with wrong key

	req := httptest.NewRequest(http.MethodPost, "/echo/slack/interactivity", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad signature, got %d", w.Code)
	}
}

func TestSlackHandlerMissingHeaders(t *testing.T) {
	secret := []byte("supersecret")
	h := newSlackHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/interactivity", strings.NewReader("payload="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing headers, got %d", w.Code)
	}
}

func TestSlackHandlerSecretNotConfigured(t *testing.T) {
	cfg := SlackHandlerConfig{
		ResolveSigningSecret: func() ([]byte, error) { return nil, fmt.Errorf("not set") },
		Idempotency:          NewCache(0, 0),
		CommandContextFor: func(teamID, userID string) CommandContext {
			return slackTestCommandContext(teamID, userID)
		},
	}
	h := NewSlackInteractivityHandler(cfg)
	req := httptest.NewRequest(http.MethodPost, "/echo/slack/interactivity", strings.NewReader("payload=%7B%7D"))
	req.Header.Set("X-Slack-Signature", "v0=00")
	req.Header.Set("X-Slack-Request-Timestamp", "0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when secret missing, got %d", w.Code)
	}
}

// ---- idempotency -------------------------------------------------------

func TestSlackHandlerReplayDoesNotDoubleAct(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCount atomic.Int32
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackTestCommandContext(teamID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCount.Add(1)
				return nil
			}
			return cc
		}
	})

	payload := `{
		"type":"block_actions",
		"team":{"id":"T"},
		"user":{"id":"U"},
		"trigger_id":"replay-1",
		"actions":[{"action_id":"watchfire_retry","value":"proj-abc|42","type":"button"}]
	}`

	for i := 0; i < 2; i++ {
		req := signedSlackRequest(t, secret, payload, ts)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i, w.Code)
		}
	}
	if got := retryCount.Load(); got != 1 {
		t.Fatalf("expected Retry to fire exactly once, got %d", got)
	}
}

// ---- view button is no-op ---------------------------------------------

func TestSlackHandlerViewButtonIsNoop(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var retryCalled, cancelCalled atomic.Bool
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackTestCommandContext(teamID, userID)
			cc.Retry = func(ctx context.Context, projectID string, taskNumber int) error {
				retryCalled.Store(true)
				return nil
			}
			cc.Cancel = func(ctx context.Context, projectID string, taskNumber int, reason string) error {
				cancelCalled.Store(true)
				return nil
			}
			return cc
		}
	})

	payload := `{
		"type":"block_actions",
		"team":{"id":"T"},
		"user":{"id":"U"},
		"trigger_id":"trig-view",
		"actions":[{"action_id":"watchfire_view","value":"x","type":"button"}]
	}`
	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if retryCalled.Load() || cancelCalled.Load() {
		t.Fatalf("view button must not invoke Retry/Cancel")
	}
}

// ---- modal open failure falls back to direct cancel -------------------

func TestSlackHandlerCancelModalOpenFailureFallsBackToDirectCancel(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	var cancelCalled atomic.Bool
	h := newSlackHandler(t, secret, func(cfg *SlackHandlerConfig) {
		cfg.OpenModal = func(ctx context.Context, triggerID string, view map[string]any) error {
			return fmt.Errorf("slack API down")
		}
		cfg.CommandContextFor = func(teamID, userID string) CommandContext {
			cc := slackTestCommandContext(teamID, userID)
			cc.Cancel = func(ctx context.Context, projectID string, taskNumber int, reason string) error {
				cancelCalled.Store(true)
				return nil
			}
			return cc
		}
	})

	payload := `{
		"type":"block_actions",
		"team":{"id":"T"},
		"user":{"id":"U"},
		"trigger_id":"trig-fb",
		"actions":[{"action_id":"watchfire_cancel","value":"proj-abc|42","type":"button"}]
	}`
	req := signedSlackRequest(t, secret, payload, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !cancelCalled.Load() {
		t.Fatalf("expected fallback Cancel to fire when OpenModal errors")
	}
}

// ---- response_url helper -----------------------------------------------

func TestPostToResponseURL(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := []byte(`{"replace_original":true,"text":"retrying"}`)
	if err := PostToResponseURL(context.Background(), nil, srv.URL, body); err != nil {
		t.Fatalf("PostToResponseURL: %v", err)
	}
	if string(captured) != string(body) {
		t.Errorf("captured = %q, want %q", captured, body)
	}
}

func TestPostToResponseURLEmptyURLIsError(t *testing.T) {
	if err := PostToResponseURL(context.Background(), nil, "", []byte(`{}`)); err == nil {
		t.Fatal("expected error on empty response_url")
	}
}

// ---- malformed body ----------------------------------------------------

func TestSlackHandlerMalformedJSON(t *testing.T) {
	secret := []byte("supersecret")
	ts := nowSlackTS(t)
	t.Cleanup(func() { SetClockForTest(nil) })

	h := newSlackHandler(t, secret)
	req := signedSlackRequest(t, secret, `{not valid json`, ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed JSON, got %d", w.Code)
	}
}

func TestResolveSigningSecretFromKeyring(t *testing.T) {
	resolver := ResolveSigningSecretFromKeyring(func() (string, error) {
		return "shh", nil
	})
	v, err := resolver()
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if string(v) != "shh" {
		t.Errorf("got %q, want shh", v)
	}

	missing := ResolveSigningSecretFromKeyring(func() (string, error) { return "", nil })
	if _, err := missing(); err == nil {
		t.Error("expected error on empty secret")
	}
}
