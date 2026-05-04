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

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
)

func newGitHubHandler(t *testing.T, secret []byte, opts ...func(*GitHubHandlerConfig)) *githubHandler {
	t.Helper()
	cfg := GitHubHandlerConfig{
		ResolveSecret: func() ([]byte, error) { return secret, nil },
		Idempotency:   NewCache(0, 0),
		FlushTask: func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			return TaskFlushResult{
				Outcome:     TaskFlushedSuccess,
				ProjectID:   "p-1",
				ProjectName: "Watchfire",
				TaskNumber:  42,
				TaskTitle:   "Wire GitHub PR merge handler",
			}, nil
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewGitHubHandler(cfg).(*githubHandler)
}

func githubPRBody(t *testing.T, action string, merged bool, branch, fullName string) []byte {
	t.Helper()
	body := map[string]any{
		"action": action,
		"number": 7,
		"pull_request": map[string]any{
			"number":   7,
			"merged":   merged,
			"title":    "v5.0 — handler_github.go",
			"html_url": "https://github.com/" + fullName + "/pull/7",
			"head": map[string]any{
				"ref": branch,
			},
			"base": map[string]any{
				"ref": "main",
				"repo": map[string]any{
					"html_url":  "https://github.com/" + fullName,
					"clone_url": "https://github.com/" + fullName + ".git",
				},
			},
		},
		"repository": map[string]any{
			"full_name": fullName,
			"html_url":  "https://github.com/" + fullName,
			"clone_url": "https://github.com/" + fullName + ".git",
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}

func signGitHub(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubHandlerMergedPRFlushesAndEmits(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	var captured atomic.Pointer[TaskFlushRequest]
	var emitted atomic.Pointer[notify.Notification]
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			captured.Store(&req)
			return TaskFlushResult{
				Outcome:     TaskFlushedSuccess,
				ProjectID:   "p-1",
				ProjectName: "Watchfire",
				TaskNumber:  42,
				TaskTitle:   "Wire GitHub PR merge handler",
			}, nil
		}
		cfg.EmitRunComplete = func(n notify.Notification) error {
			emitted.Store(&n)
			return nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	req.Header.Set(githubHeaderDelivery, "deliv-1")
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
		t.Errorf("expected Merged=true on closed+merged")
	}
	if got.RepoURL == "" {
		t.Errorf("expected RepoURL populated")
	}

	gotN := emitted.Load()
	if gotN == nil {
		t.Fatalf("expected RUN_COMPLETE notification to fire on TaskFlushedSuccess")
	}
	if gotN.Kind != notify.KindRunComplete {
		t.Errorf("notification kind = %q, want %q", gotN.Kind, notify.KindRunComplete)
	}
	if gotN.ProjectID != "p-1" {
		t.Errorf("notification project_id = %q, want p-1", gotN.ProjectID)
	}
	if gotN.TaskNumber != 42 {
		t.Errorf("notification task_number = %d, want 42", gotN.TaskNumber)
	}
	wantTitle := "Watchfire — PR #7 merged"
	if gotN.Title != wantTitle {
		t.Errorf("notification title = %q, want %q", gotN.Title, wantTitle)
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("malformed response body: %v", err)
	}
	if doc["outcome"] != "flushed-success" {
		t.Errorf("outcome = %v, want flushed-success", doc["outcome"])
	}
}

func TestGitHubHandlerClosedWithoutMergeIsNoOp(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", false, "watchfire/0042", "watchfire-io/watchfire")

	var fired atomic.Bool
	var emitted atomic.Bool
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			fired.Store(true)
			return TaskFlushResult{Outcome: TaskFlushedSuccess}, nil
		}
		cfg.EmitRunComplete = func(n notify.Notification) error {
			emitted.Store(true)
			return nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	req.Header.Set(githubHeaderDelivery, "deliv-noop")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if fired.Load() {
		t.Errorf("FlushTask should not fire on closed-without-merge")
	}
	if emitted.Load() {
		t.Errorf("RUN_COMPLETE should not emit on closed-without-merge")
	}
}

func TestGitHubHandlerOpenedActionIgnored(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "opened", false, "watchfire/0042", "watchfire-io/watchfire")

	var fired atomic.Bool
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			fired.Store(true)
			return TaskFlushResult{Outcome: TaskFlushNoMatch}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ignored ack, got %d", w.Code)
	}
	if fired.Load() {
		t.Errorf("FlushTask should not fire on opened action")
	}
}

func TestGitHubHandlerNonPullRequestEventIgnored(t *testing.T) {
	secret := []byte("supersecret")
	body := []byte(`{}`)

	h := newGitHubHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, "push")
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ignored ack on non-PR event, got %d", w.Code)
	}
}

func TestGitHubHandlerWrongSignatureRejected(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	var fired atomic.Bool
	var emitted atomic.Bool
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			fired.Store(true)
			return TaskFlushResult{Outcome: TaskFlushedSuccess}, nil
		}
		cfg.EmitRunComplete = func(n notify.Notification) error {
			emitted.Store(true)
			return nil
		}
	})
	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub([]byte("wrongkey"), body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if fired.Load() {
		t.Errorf("FlushTask must not fire when signature is invalid")
	}
	if emitted.Load() {
		t.Errorf("RUN_COMPLETE must not emit when signature is invalid")
	}
}

func TestGitHubHandlerMissingSignatureRejected(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	h := newGitHubHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGitHubHandlerMalformedSignatureBadRequest(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	h := newGitHubHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, "sha256=zz") // not hex
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed signature, got %d", w.Code)
	}
}

func TestGitHubHandlerSecretNotConfigured(t *testing.T) {
	cfg := GitHubHandlerConfig{
		ResolveSecret: func() ([]byte, error) { return nil, errBadConfig{} },
		Idempotency:   NewCache(0, 0),
		FlushTask:     func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) { return TaskFlushResult{}, nil },
	}
	h := NewGitHubHandler(cfg)
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, "sha256=00")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGitHubHandlerReplayAcksWithoutDoubleEmit(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	var flushCalls atomic.Int32
	var emitCalls atomic.Int32
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			flushCalls.Add(1)
			return TaskFlushResult{
				Outcome:     TaskFlushedSuccess,
				ProjectID:   "p-1",
				ProjectName: "Watchfire",
				TaskNumber:  42,
			}, nil
		}
		cfg.EmitRunComplete = func(n notify.Notification) error {
			emitCalls.Add(1)
			return nil
		}
	})

	send := func(deliveryID string) int {
		req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
		req.Header.Set(githubHeaderEvent, githubEventPullRequest)
		req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
		req.Header.Set(githubHeaderDelivery, deliveryID)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if c := send("deliv-replay"); c != http.StatusOK {
		t.Fatalf("first send: expected 200, got %d", c)
	}
	if c := send("deliv-replay"); c != http.StatusOK {
		t.Fatalf("replay send: expected 200, got %d", c)
	}
	if got := flushCalls.Load(); got != 1 {
		t.Errorf("FlushTask calls = %d, want 1", got)
	}
	if got := emitCalls.Load(); got != 1 {
		t.Errorf("RUN_COMPLETE emits = %d, want 1", got)
	}
}

func TestGitHubHandlerAlreadyDoneNoEmit(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	var emitCalls atomic.Int32
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			return TaskFlushResult{
				Outcome:     TaskFlushAlreadyDone,
				ProjectID:   "p-1",
				ProjectName: "Watchfire",
				TaskNumber:  42,
			}, nil
		}
		cfg.EmitRunComplete = func(n notify.Notification) error {
			emitCalls.Add(1)
			return nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	req.Header.Set(githubHeaderDelivery, "deliv-already-done")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on already-done, got %d", w.Code)
	}
	if got := emitCalls.Load(); got != 0 {
		t.Errorf("RUN_COMPLETE emits = %d, want 0 on already-done", got)
	}
}

func TestGitHubHandlerNoBranchMatchNoEmit(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "feature/not-a-task", "watchfire-io/watchfire")

	var emitCalls atomic.Int32
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			return TaskFlushResult{Outcome: TaskFlushNoMatch}, nil
		}
		cfg.EmitRunComplete = func(n notify.Notification) error {
			emitCalls.Add(1)
			return nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	req.Header.Set(githubHeaderDelivery, "deliv-no-match")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on no-match, got %d", w.Code)
	}
	if got := emitCalls.Load(); got != 0 {
		t.Errorf("RUN_COMPLETE emits = %d, want 0 on no-match", got)
	}
}

func TestGitHubHandlerMalformedJSONRejected(t *testing.T) {
	secret := []byte("supersecret")
	body := []byte(`{not json`)

	h := newGitHubHandler(t, secret)
	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed JSON, got %d", w.Code)
	}
}

func TestGitHubHandlerSynthesizesURLFromFullName(t *testing.T) {
	secret := []byte("supersecret")
	// Body without `repository.html_url` / `clone_url` — exercises the
	// `https://github.com/<full_name>` fallback synthesis.
	body, err := json.Marshal(map[string]any{
		"action": "closed",
		"number": 12,
		"pull_request": map[string]any{
			"number": 12,
			"merged": true,
			"head":   map[string]any{"ref": "watchfire/0042"},
			"base":   map[string]any{"ref": "main"},
		},
		"repository": map[string]any{
			"full_name": "watchfire-io/watchfire",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var capturedRepo string
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			capturedRepo = req.RepoURL
			return TaskFlushResult{Outcome: TaskFlushedSuccess}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if capturedRepo != "https://github.com/watchfire-io/watchfire" {
		t.Errorf("captured repo = %q, want https://github.com/watchfire-io/watchfire", capturedRepo)
	}
}

func TestGitHubHandlerRecordDeliveryFiresOnSuccess(t *testing.T) {
	secret := []byte("supersecret")
	body := githubPRBody(t, "closed", true, "watchfire/0042", "watchfire-io/watchfire")

	var recorded atomic.Bool
	h := newGitHubHandler(t, secret, func(cfg *GitHubHandlerConfig) {
		cfg.RecordDelivery = func() { recorded.Store(true) }
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(githubHeaderEvent, githubEventPullRequest)
	req.Header.Set(githubHeaderSignature, signGitHub(secret, body))
	req.Header.Set(githubHeaderDelivery, "deliv-rec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !recorded.Load() {
		t.Errorf("expected RecordDelivery to fire on successful flush")
	}
}

func TestTitleForRunComplete(t *testing.T) {
	tests := []struct {
		name    string
		project string
		pr      int
		want    string
	}{
		{"with project", "Watchfire", 7, "Watchfire — PR #7 merged"},
		{"empty project falls back to bare", "", 7, "PR #7 merged"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := titleForRunComplete(tc.project, tc.pr)
			if got != tc.want {
				t.Errorf("titleForRunComplete(%q, %d) = %q, want %q", tc.project, tc.pr, got, tc.want)
			}
		})
	}
}
