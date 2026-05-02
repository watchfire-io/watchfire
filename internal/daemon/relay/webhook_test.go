package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// newWebhookEndpointForTest returns a fully-populated WebhookEndpoint
// with all three event bits set so callers can drive any kind through
// the adapter without per-test plumbing.
func newWebhookEndpointForTest(url string) models.WebhookEndpoint {
	return models.WebhookEndpoint{
		ID:        "wh-test",
		Label:     "test webhook",
		URL:       url,
		SecretRef: "watchfire.integration.wh-test.secret",
		EnabledEvents: models.EventBitmask{
			TaskFailed:   true,
			RunComplete:  true,
			WeeklyDigest: true,
		},
	}
}

func samplePayload() Payload {
	return Payload{
		Version:           1,
		Kind:              string(notify.KindTaskFailed),
		EmittedAt:         time.Unix(1_700_000_000, 0).UTC(),
		ProjectID:         "proj-1",
		ProjectName:       "Watchfire",
		ProjectColor:      "#3b82f6",
		TaskNumber:        7,
		TaskTitle:         "Boom",
		TaskFailureReason: "synthetic",
		DeepLink:          "watchfire://project/proj-1/task/0007",
	}
}

func TestWebhookHappyPathSignsAndPosts(t *testing.T) {
	secret := []byte("hunter2")
	var receivedBody []byte
	var receivedSig, receivedEvent, receivedUA, receivedCT string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		receivedSig = r.Header.Get(SignatureHeader)
		receivedEvent = r.Header.Get(EventHeader)
		receivedUA = r.Header.Get("User-Agent")
		receivedCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), secret, srv.Client(), nil)
	if err := a.Send(context.Background(), samplePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !VerifyHMAC(secret, receivedBody, receivedSig) {
		t.Fatalf("server-side signature verification failed: sig=%s", receivedSig)
	}
	if receivedEvent != string(notify.KindTaskFailed) {
		t.Fatalf("X-Watchfire-Event: got %q want %q", receivedEvent, string(notify.KindTaskFailed))
	}
	if !strings.HasPrefix(receivedUA, "watchfire/") {
		t.Fatalf("User-Agent: got %q (want watchfire/<version>)", receivedUA)
	}
	if receivedCT != "application/json" {
		t.Fatalf("Content-Type: got %q", receivedCT)
	}

	var got Payload
	if err := json.Unmarshal(receivedBody, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.Kind != string(notify.KindTaskFailed) || got.TaskNumber != 7 {
		t.Fatalf("body shape mismatch: %+v", got)
	}
}

func TestWebhookUnsignedWhenNoSecretRef(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SignatureHeader) != "" {
			t.Errorf("unexpected signature header for unsigned endpoint: %s", r.Header.Get(SignatureHeader))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ep := newWebhookEndpointForTest(srv.URL)
	ep.SecretRef = "" // no signing
	a := NewWebhookAdapter(ep, nil, srv.Client(), nil)
	if err := a.Send(context.Background(), samplePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestWebhookMissingSecretFailsLoudly(t *testing.T) {
	// SecretRef is set but no resolved secret was passed → adapter must
	// refuse to send rather than emit an unsigned request.
	a := NewWebhookAdapter(newWebhookEndpointForTest("http://example.invalid"), nil, http.DefaultClient, nil)
	err := a.Send(context.Background(), samplePayload())
	if err == nil || !strings.Contains(err.Error(), "secret not resolved") {
		t.Fatalf("expected secret-not-resolved error, got %v", err)
	}
}

func TestWebhookHTTP4xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), []byte("k"), srv.Client(), nil)
	err := a.Send(context.Background(), samplePayload())
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP 400 error, got %v", err)
	}
}

func TestWebhookHTTP5xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), []byte("k"), srv.Client(), nil)
	err := a.Send(context.Background(), samplePayload())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected HTTP 500 error, got %v", err)
	}
}

func TestWebhookContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block long enough for the context deadline to fire.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), []byte("k"), srv.Client(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := a.Send(ctx, samplePayload())
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
}

// TestWebhookSendWithRetryThroughDispatcher exercises the retry +
// circuit-breaker policy against a flapping receiver. First two sends
// fail with 5xx, third succeeds — dispatcher retries 2× total (3
// attempts), so the third succeeds and no failure is recorded.
func TestWebhookSendWithRetryThroughDispatcher(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits := atomic.AddInt32(&n, 1)
		if hits < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), []byte("k"), srv.Client(), nil)

	// Hand-roll a tiny dispatcher just for retry + cb wiring.
	d := NewDispatcher(
		nil, // no bus — we drive sendWithRetry directly
		func(notify.Notification) (Payload, error) { return samplePayload(), nil },
		func() ([]Adapter, error) { return []Adapter{a}, nil },
		WithRetryDelays([]time.Duration{1 * time.Millisecond, 1 * time.Millisecond}),
		WithCircuitBreaker(time.Minute, 3),
	)

	d.sendWithRetry(context.Background(), a, samplePayload())
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Fatalf("expected 3 attempts before success, got %d", got)
	}
}

// TestWebhookRetryExhaustionRecordsBreakerFailure exercises the path
// where every retry attempt fails — the breaker should record one
// hard failure and the next send increments past the threshold.
func TestWebhookRetryExhaustionRecordsBreakerFailure(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), []byte("k"), srv.Client(), nil)
	d := NewDispatcher(
		nil,
		func(notify.Notification) (Payload, error) { return samplePayload(), nil },
		func() ([]Adapter, error) { return []Adapter{a}, nil },
		WithRetryDelays([]time.Duration{1 * time.Millisecond, 1 * time.Millisecond}),
		WithCircuitBreaker(time.Minute, 3),
	)

	d.sendWithRetry(context.Background(), a, samplePayload())
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Fatalf("expected 3 attempts on retry exhaustion, got %d", got)
	}
	d.mu.RLock()
	state, ok := d.cbState[a.ID()]
	d.mu.RUnlock()
	if !ok || len(state.failures) != 1 {
		t.Fatalf("expected one breaker failure recorded, got %v / %v", ok, state)
	}
}

// TestWebhookCircuitBreakerOpensOnFourthFailure asserts the spec's
// 4-strikes-and-you're-out semantics: 3 hard failures inside the
// window record breaker hits, and the 4th attempt short-circuits
// without the adapter ever being called.
func TestWebhookCircuitBreakerOpensOnFourthFailure(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := NewWebhookAdapter(newWebhookEndpointForTest(srv.URL), []byte("k"), srv.Client(), nil)
	d := NewDispatcher(
		nil,
		func(notify.Notification) (Payload, error) { return samplePayload(), nil },
		func() ([]Adapter, error) { return []Adapter{a}, nil },
		WithRetryDelays([]time.Duration{1 * time.Millisecond, 1 * time.Millisecond}),
		WithCircuitBreaker(5*time.Minute, 3),
	)

	// Three hard failures = 3 attempts × 3 sends = 9 total.
	for i := 0; i < 3; i++ {
		d.sendWithRetry(context.Background(), a, samplePayload())
	}
	if got := atomic.LoadInt32(&n); got != 9 {
		t.Fatalf("expected 9 receiver hits across 3 send rounds, got %d", got)
	}

	// Fourth send must short-circuit — no further hits at the receiver.
	d.sendWithRetry(context.Background(), a, samplePayload())
	if got := atomic.LoadInt32(&n); got != 9 {
		t.Fatalf("breaker open: expected hits to stay at 9, got %d", got)
	}
}
