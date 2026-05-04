package echo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newGitLabHandler(t *testing.T, token string, opts ...func(*GitLabHandlerConfig)) *gitlabHandler {
	t.Helper()
	cfg := GitLabHandlerConfig{
		ResolveSharedToken: func() (string, error) { return token, nil },
		Idempotency:        NewCache(0, 0),
		FlushTask: func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			return TaskFlushResult{Outcome: TaskFlushedSuccess, ProjectID: "p-1", TaskNumber: 42}, nil
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewGitLabHandler(cfg).(*gitlabHandler)
}

func gitlabMRBody(t *testing.T, action, source string) []byte {
	t.Helper()
	body := map[string]any{
		"object_kind": "merge_request",
		"object_attributes": map[string]any{
			"action":        action,
			"state":         "merged",
			"source_branch": source,
			"target_branch": "main",
		},
		"project": map[string]any{
			"web_url":      "https://gitlab.com/team/repo",
			"git_http_url": "https://gitlab.com/team/repo.git",
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return buf
}

func TestGitLabHandlerMergeFlushes(t *testing.T) {
	const token = "shhhhh"
	body := gitlabMRBody(t, "merge", "watchfire/0042")

	var captured atomic.Pointer[TaskFlushRequest]
	h := newGitLabHandler(t, token, func(cfg *GitLabHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			captured.Store(&req)
			return TaskFlushResult{
				Outcome:    TaskFlushedSuccess,
				ProjectID:  "p-1",
				TaskNumber: 42,
				TaskTitle:  "Add inbound parity",
			}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(string(body)))
	req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
	req.Header.Set(gitlabHeaderToken, token)
	req.Header.Set(gitlabHeaderUUID, "deliv-1")
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
		t.Errorf("expected Merged=true")
	}
	if got.RepoURL == "" {
		t.Errorf("expected RepoURL populated")
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("malformed response body: %v", err)
	}
	if doc["outcome"] != "flushed-success" {
		t.Errorf("outcome = %v, want flushed-success", doc["outcome"])
	}
}

func TestGitLabHandlerCloseWithoutMerge(t *testing.T) {
	const token = "shhhhh"
	body := gitlabMRBody(t, "close", "watchfire/0042")
	// Override state so the close branch fires.
	body = []byte(strings.Replace(string(body), `"state":"merged"`, `"state":"closed"`, 1))

	var capturedReason string
	var captured atomic.Bool
	h := newGitLabHandler(t, token, func(cfg *GitLabHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			captured.Store(true)
			capturedReason = req.FailureReason
			if req.Merged {
				t.Errorf("expected Merged=false on close action")
			}
			return TaskFlushResult{Outcome: TaskFlushedFailure}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(string(body)))
	req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
	req.Header.Set(gitlabHeaderToken, token)
	req.Header.Set(gitlabHeaderUUID, "deliv-2")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !captured.Load() {
		t.Fatalf("expected FlushTask to fire on close")
	}
	if capturedReason == "" {
		t.Errorf("expected FailureReason populated on close")
	}
}

func TestGitLabHandlerIgnoresOpenAction(t *testing.T) {
	const token = "shhhhh"
	body := gitlabMRBody(t, "open", "watchfire/0042")
	body = []byte(strings.Replace(string(body), `"state":"merged"`, `"state":"opened"`, 1))

	var fired atomic.Bool
	h := newGitLabHandler(t, token, func(cfg *GitLabHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			fired.Store(true)
			return TaskFlushResult{Outcome: TaskFlushNoMatch}, nil
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(string(body)))
	req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
	req.Header.Set(gitlabHeaderToken, token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if fired.Load() {
		t.Errorf("expected FlushTask not to fire on open action")
	}
}

func TestGitLabHandlerWrongTokenRejected(t *testing.T) {
	const token = "shhhhh"
	body := gitlabMRBody(t, "merge", "watchfire/0042")

	h := newGitLabHandler(t, token)
	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(string(body)))
	req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
	req.Header.Set(gitlabHeaderToken, "wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGitLabHandlerMissingTokenRejected(t *testing.T) {
	const token = "shhhhh"
	body := gitlabMRBody(t, "merge", "watchfire/0042")

	h := newGitLabHandler(t, token)
	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(string(body)))
	req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing token, got %d", w.Code)
	}
}

func TestGitLabHandlerSecretNotConfigured(t *testing.T) {
	cfg := GitLabHandlerConfig{
		ResolveSharedToken: func() (string, error) { return "", errBadConfig{} },
		Idempotency:        NewCache(0, 0),
		FlushTask:          func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) { return TaskFlushResult{}, nil },
	}
	h := NewGitLabHandler(cfg)
	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader("{}"))
	req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
	req.Header.Set(gitlabHeaderToken, "any")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when secret unresolved, got %d", w.Code)
	}
}

func TestGitLabHandlerReplayAcksWithoutFlushing(t *testing.T) {
	const token = "shhhhh"
	body := gitlabMRBody(t, "merge", "watchfire/0042")

	var calls atomic.Int32
	h := newGitLabHandler(t, token, func(cfg *GitLabHandlerConfig) {
		cfg.FlushTask = func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error) {
			calls.Add(1)
			return TaskFlushResult{Outcome: TaskFlushedSuccess}, nil
		}
	})

	send := func(deliveryID string) int {
		req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(string(body)))
		req.Header.Set(gitlabHeaderEvent, gitlabEventMR)
		req.Header.Set(gitlabHeaderToken, token)
		req.Header.Set(gitlabHeaderUUID, deliveryID)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if c := send("deliv-3"); c != http.StatusOK {
		t.Fatalf("first send: expected 200, got %d", c)
	}
	if c := send("deliv-3"); c != http.StatusOK {
		t.Fatalf("second send (replay): expected 200, got %d", c)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("FlushTask called %d times, want 1", got)
	}
}

func TestGitLabHandlerNonMergeRequestEventIgnored(t *testing.T) {
	const token = "shhhhh"
	h := newGitLabHandler(t, token)
	req := httptest.NewRequest(http.MethodPost, "/echo/gitlab/webhook", strings.NewReader(`{}`))
	req.Header.Set(gitlabHeaderEvent, "Push Hook")
	req.Header.Set(gitlabHeaderToken, token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ignored ack, got %d", w.Code)
	}
}

type errBadConfig struct{}

func (errBadConfig) Error() string { return "bad config" }
