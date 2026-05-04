package echo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newBitbucketHandler(t *testing.T, secret []byte, opts ...func(*BitbucketHandlerConfig)) *bitbucketHandler {
	t.Helper()
	cfg := BitbucketHandlerConfig{
		ResolveSecret: func() ([]byte, error) { return secret, nil },
		Idempotency:   NewCache(0, 0),
		FlushTask: func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			return TaskFlushResult{Outcome: TaskFlushedSuccess, ProjectID: "p-1", TaskNumber: 42}, nil
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewBitbucketHandler(cfg).(*bitbucketHandler)
}

func bitbucketPRBody(t *testing.T, branch, fullName string) []byte {
	t.Helper()
	body := map[string]any{
		"pullrequest": map[string]any{
			"state": "MERGED",
			"title": "Add inbound parity",
			"source": map[string]any{
				"branch": map[string]any{"name": branch},
			},
		},
		"repository": map[string]any{
			"full_name": fullName,
			"links": map[string]any{
				"html": map[string]any{"href": "https://bitbucket.org/" + fullName},
			},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}

func signBitbucket(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestBitbucketHandlerFulfilledFlushes(t *testing.T) {
	secret := []byte("supersecret")
	body := bitbucketPRBody(t, "watchfire/0042", "team/repo")

	var captured atomic.Pointer[TaskFlushRequest]
	h := newBitbucketHandler(t, secret, func(cfg *BitbucketHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			captured.Store(&req)
			return TaskFlushResult{
				Outcome:    TaskFlushedSuccess,
				ProjectID:  "p-1",
				TaskNumber: 42,
			}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
	req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRFulfilled)
	req.Header.Set(bitbucketHeaderSignature, signBitbucket(secret, body))
	req.Header.Set(bitbucketHeaderUUID, "{deadbeef-aaaa}")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	got := captured.Load()
	if got == nil {
		t.Fatalf("expected FlushTask to fire")
	}
	if got.SourceBranch != "watchfire/0042" {
		t.Errorf("source branch = %q, want watchfire/0042", got.SourceBranch)
	}
	if !got.Merged {
		t.Errorf("expected Merged=true on pullrequest:fulfilled")
	}
}

func TestBitbucketHandlerRejectedFlushesAsFailure(t *testing.T) {
	secret := []byte("supersecret")
	body := bitbucketPRBody(t, "watchfire/0042", "team/repo")

	var capturedReason string
	h := newBitbucketHandler(t, secret, func(cfg *BitbucketHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			capturedReason = req.FailureReason
			if req.Merged {
				t.Errorf("expected Merged=false on pullrequest:rejected")
			}
			return TaskFlushResult{Outcome: TaskFlushedFailure}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
	req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRRejected)
	req.Header.Set(bitbucketHeaderSignature, signBitbucket(secret, body))
	req.Header.Set(bitbucketHeaderUUID, "{deadbeef-bbbb}")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if capturedReason == "" {
		t.Errorf("expected FailureReason populated on rejected event")
	}
}

func TestBitbucketHandlerWrongSignatureRejected(t *testing.T) {
	secret := []byte("supersecret")
	body := bitbucketPRBody(t, "watchfire/0042", "team/repo")

	h := newBitbucketHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
	req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRFulfilled)
	req.Header.Set(bitbucketHeaderSignature, signBitbucket([]byte("wrongkey"), body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBitbucketHandlerMissingSignatureRejected(t *testing.T) {
	secret := []byte("supersecret")
	body := bitbucketPRBody(t, "watchfire/0042", "team/repo")

	h := newBitbucketHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
	req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRFulfilled)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBitbucketHandlerNonPRFulfilledIgnored(t *testing.T) {
	secret := []byte("supersecret")
	body := bitbucketPRBody(t, "watchfire/0042", "team/repo")

	h := newBitbucketHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
	req.Header.Set(bitbucketHeaderEvent, "pullrequest:created")
	req.Header.Set(bitbucketHeaderSignature, signBitbucket(secret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ignored ack, got %d", w.Code)
	}
}

func TestBitbucketHandlerReplayAcksWithoutFlushing(t *testing.T) {
	secret := []byte("supersecret")
	body := bitbucketPRBody(t, "watchfire/0042", "team/repo")

	var calls atomic.Int32
	h := newBitbucketHandler(t, secret, func(cfg *BitbucketHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			calls.Add(1)
			return TaskFlushResult{Outcome: TaskFlushedSuccess}, nil
		}
	})

	send := func(uuid string) int {
		req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
		req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRFulfilled)
		req.Header.Set(bitbucketHeaderSignature, signBitbucket(secret, body))
		req.Header.Set(bitbucketHeaderUUID, uuid)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if c := send("{abcd}"); c != http.StatusOK {
		t.Fatalf("first send: %d", c)
	}
	if c := send("{abcd}"); c != http.StatusOK {
		t.Fatalf("replay send: %d", c)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("flush calls = %d, want 1", got)
	}
}

func TestBitbucketHandlerSecretNotConfigured(t *testing.T) {
	cfg := BitbucketHandlerConfig{
		ResolveSecret: func() ([]byte, error) { return nil, errBadConfig{} },
		Idempotency:   NewCache(0, 0),
		FlushTask:     func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) { return TaskFlushResult{}, nil },
	}
	h := NewBitbucketHandler(cfg)
	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader("{}"))
	req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRFulfilled)
	req.Header.Set(bitbucketHeaderSignature, "sha256=00")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestBitbucketHandlerSynthesizesURLFromFullName(t *testing.T) {
	secret := []byte("supersecret")
	// Body without `links.html.href` to exercise the fallback path.
	body, err := json.Marshal(map[string]any{
		"pullrequest": map[string]any{
			"state":  "MERGED",
			"title":  "Add inbound parity",
			"source": map[string]any{"branch": map[string]any{"name": "watchfire/0042"}},
		},
		"repository": map[string]any{
			"full_name": "team/repo",
			"links":     map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var capturedRepo string
	h := newBitbucketHandler(t, secret, func(cfg *BitbucketHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			capturedRepo = req.RepoURL
			return TaskFlushResult{Outcome: TaskFlushedSuccess}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/bitbucket/webhook", strings.NewReader(string(body)))
	req.Header.Set(bitbucketHeaderEvent, bitbucketEventPRFulfilled)
	req.Header.Set(bitbucketHeaderSignature, signBitbucket(secret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if capturedRepo != "https://bitbucket.org/team/repo" {
		t.Errorf("captured repo = %q, want https://bitbucket.org/team/repo", capturedRepo)
	}
}
