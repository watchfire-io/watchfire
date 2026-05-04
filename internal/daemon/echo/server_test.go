package echo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// startTestServer boots an Echo server on a free loopback port and
// returns the live base URL plus a cancel func. The caller is
// responsible for invoking cancel — the server's Run goroutine drains
// gracefully when ctx is cancelled.
func startTestServer(t *testing.T, register func(s *Server)) (string, context.CancelFunc) {
	t.Helper()

	// Reserve a free loopback port. Closing the listener immediately is
	// safe: the OS keeps the port bindable for the brief window before
	// Run reopens it. A 0-port direct bind in Run would also work but
	// then the test couldn't read the chosen port until after Run had
	// already started listening.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}

	srv := New(models.InboundConfig{ListenAddr: addr, RateLimitPerMin: -1}, log.New(io.Discard, "", 0))
	if register != nil {
		register(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Wait until the server reports listening or Run errors out. The
	// loop is bounded so a stuck bind surfaces as a test failure rather
	// than a hang.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Listening() {
			break
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("server exited before listening: %v", err)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !srv.Listening() {
		cancel()
		t.Fatalf("server did not start listening on %s", addr)
	}

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("server did not shut down within drain window")
		}
	})

	return "http://" + addr, cancel
}

// TestHealthEndpoint: GET /echo/health returns 200 with JSON
// {listening: true, version: <buildinfo.Version>}. The version string
// is read from buildinfo at request time — the test asserts the field
// is present + non-empty rather than hard-coding a value (the version
// is user-gated, see CLAUDE.md / project memory).
func TestHealthEndpoint(t *testing.T) {
	base, _ := startTestServer(t, nil)

	resp, err := http.Get(base + "/echo/health")
	if err != nil {
		t.Fatalf("GET /echo/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("expected application/json content-type, got %q", got)
	}

	var body struct {
		Listening bool   `json:"listening"`
		Version   string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Listening {
		t.Fatalf("expected listening:true, got false")
	}
	if body.Version == "" {
		t.Fatalf("expected non-empty version field")
	}
}

// TestUnknownRouteReturns404: any path that hasn't been registered
// returns 404 (the http.ServeMux default). Confirms the framework
// doesn't accidentally swallow unknown routes into the catch-all
// handler.
func TestUnknownRouteReturns404(t *testing.T) {
	base, _ := startTestServer(t, nil)

	resp, err := http.Get(base + "/echo/no-such-provider")
	if err != nil {
		t.Fatalf("GET unknown: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// TestSizeLimitReturns413: a registered POST handler that tries to
// read a body > MaxBodyBytes gets a *http.MaxBytesError, which the
// `IsTooLarge` helper recognises so handlers can translate into 413.
// The wrap middleware hands the request through MaxBytesReader
// regardless of whether the handler reads — but the actual 413 is the
// handler's responsibility, mirroring how concrete provider handlers
// in this package behave.
func TestSizeLimitReturns413(t *testing.T) {
	base, _ := startTestServer(t, func(s *Server) {
		s.RegisterProvider(http.MethodPost, "/echo/big", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(r.Body); err != nil {
				if IsTooLarge(err) {
					http.Error(w, "too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
	})

	// 2 MiB > 1 MiB cap
	body := bytes.Repeat([]byte("x"), int(MaxBodyBytes)+1<<10)
	resp, err := http.Post(base+"/echo/big", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST oversized: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

// TestSmallBodyPasses: a body well under the cap reaches the handler.
// Sanity check that the cap is selective, not blanket-blocking.
func TestSmallBodyPasses(t *testing.T) {
	base, _ := startTestServer(t, func(s *Server) {
		s.RegisterProvider(http.MethodPost, "/echo/small", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
		}))
	})

	resp, err := http.Post(base+"/echo/small", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("POST small: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hi" {
		t.Fatalf("expected echoed body, got %q", body)
	}
}

// TestDisabledServerSkipsBind: cfg.Disabled = true short-circuits Run
// without attempting a listen. Confirms the daemon-wiring contract
// that "disabled" is honoured even when a stale ListenAddr is set.
func TestDisabledServerSkipsBind(t *testing.T) {
	srv := New(models.InboundConfig{ListenAddr: "127.0.0.1:0", Disabled: true}, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Run(ctx); err != nil {
		t.Fatalf("disabled Run returned error: %v", err)
	}
	if srv.Listening() {
		t.Fatalf("disabled server should never listen")
	}
}

// TestBindFailureSurfacesError: binding to an address already in use
// returns a non-nil error from Run + populates BindError so the
// status RPC can surface "listening: false, bind_error: ...".
func TestBindFailureSurfacesError(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold listen: %v", err)
	}
	defer hold.Close()

	srv := New(models.InboundConfig{ListenAddr: hold.Addr().String()}, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := srv.Run(ctx)
	if runErr == nil {
		t.Fatalf("expected bind error, got nil")
	}
	if srv.BindError() == "" {
		t.Fatalf("expected BindError to be populated after failed bind")
	}
	if srv.Listening() {
		t.Fatalf("server should not report listening after bind failure")
	}
}

// TestAddrAndPublicURLAccessors: trivial getters round-trip the
// configured values so the IntegrationsService.GetInboundStatus RPC
// has a stable surface.
func TestAddrAndPublicURLAccessors(t *testing.T) {
	cfg := models.InboundConfig{ListenAddr: "127.0.0.1:9999", PublicURL: "https://watchfire.example.com"}
	srv := New(cfg, log.New(io.Discard, "", 0))
	if got := srv.Addr(); got != cfg.ListenAddr {
		t.Fatalf("Addr: want %q, got %q", cfg.ListenAddr, got)
	}
	if got := srv.PublicURL(); got != cfg.PublicURL {
		t.Fatalf("PublicURL: want %q, got %q", cfg.PublicURL, got)
	}

	// Empty ListenAddr defaults to the loopback constant.
	def := New(models.InboundConfig{}, log.New(io.Discard, "", 0))
	if got := def.Addr(); got != DefaultListenAddr {
		t.Fatalf("default Addr: want %q, got %q", DefaultListenAddr, got)
	}
}

// TestIsTooLarge: the helper recognises *http.MaxBytesError but not
// generic errors. Per-provider handlers rely on this discrimination
// to translate the read error into 413.
func TestIsTooLarge(t *testing.T) {
	if IsTooLarge(nil) {
		t.Fatalf("nil error should not be too-large")
	}
	if IsTooLarge(errors.New("boom")) {
		t.Fatalf("generic error should not be too-large")
	}
	if !IsTooLarge(&http.MaxBytesError{Limit: MaxBodyBytes}) {
		t.Fatalf("MaxBytesError should be too-large")
	}
}
