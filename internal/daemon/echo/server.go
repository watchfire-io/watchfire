package echo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/models"
)

// DefaultListenAddr is the loopback bind v8.0 Echo uses when
// `InboundConfig.ListenAddr` is empty. Loopback-only by default keeps
// the surface invisible to LAN scans on a fresh install; users who
// need public reach paste an ngrok / Cloudflare-tunnel URL into
// `InboundConfig.PublicURL` and point the upstream provider at it.
const DefaultListenAddr = "127.0.0.1:8765"

// MaxBodyBytes is the per-request payload cap. 1 MiB is comfortably
// over the largest legitimate inbound (Discord interaction payloads
// top out around a few KB even with full guild context attached;
// GitHub `pull_request.closed` deliveries are similar). Anything
// larger is treated as a memory-exhaustion attempt and rejected with
// 413 before the body is buffered.
const MaxBodyBytes int64 = 1 << 20

// shutdownDrain is the graceful-stop deadline applied when `Run`'s
// context cancels. Any in-flight handler past this gets a hard close.
const shutdownDrain = 5 * time.Second

// Server wraps an http.Server with per-provider handler registration
// and the cross-cutting middlewares every Echo route shares: 1 MiB
// body cap, common log format, panic recovery.
type Server struct {
	cfg    models.InboundConfig
	logger *log.Logger
	mux    *http.ServeMux

	mu        sync.Mutex
	listener  net.Listener
	httpSrv   *http.Server
	listening bool
}

// New builds an Echo server with the configured listen address. The
// server does not bind until Run() is called. Handlers register via
// `RegisterProvider` so concrete provider tasks (Discord, Slack,
// GitHub) plug in without modifying server.go.
func New(cfg models.InboundConfig, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	mux := http.NewServeMux()
	s := &Server{cfg: cfg, logger: logger, mux: mux}

	mux.HandleFunc("GET /echo/health", s.health)
	return s
}

// RegisterProvider attaches a per-provider HTTP handler at the given
// route. Routes are expected to be `POST /echo/<provider>/...` —
// callers from the same package own that convention. The server
// wraps the handler with the size cap + recovery middleware.
func (s *Server) RegisterProvider(method, path string, h http.Handler) {
	pattern := method + " " + path
	s.mux.Handle(pattern, s.wrap(h))
}

// Listening reports whether the server is currently accepting
// connections. Used by the IntegrationsService.GetInboundStatus RPC.
func (s *Server) Listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listening
}

// Addr returns the configured listen address. Stable across restarts;
// when Run's underlying Listen fails the value is still meaningful for
// log lines and the status RPC.
func (s *Server) Addr() string { return s.cfg.ListenAddr }

// PublicURL returns the user-supplied display URL (e.g. an ngrok
// tunnel) for the status RPC.
func (s *Server) PublicURL() string { return s.cfg.PublicURL }

// Run binds and serves until ctx cancels. A bind failure surfaces
// immediately; once bound the function blocks until the underlying
// http.Server returns. Graceful shutdown drains in-flight requests
// for up to `shutdownDrain` before tearing the listener down.
//
// If `cfg.Disabled` is true, Run returns nil immediately without
// binding — callers don't need to gate the call themselves.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.Disabled {
		s.logger.Printf("INFO: echo: inbound listener disabled (config)")
		return nil
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("echo: bind %s: %w", s.cfg.ListenAddr, err)
	}

	s.mu.Lock()
	s.listener = listener
	s.httpSrv = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          s.logger,
	}
	s.listening = true
	s.mu.Unlock()

	s.logger.Printf("INFO: echo: listening on %s", s.cfg.ListenAddr)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.httpSrv.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownDrain)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
		s.mu.Lock()
		s.listening = false
		s.mu.Unlock()
		return nil
	case err := <-serveErr:
		s.mu.Lock()
		s.listening = false
		s.mu.Unlock()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// wrap applies the cross-cutting middlewares Echo handlers share:
// body-size cap, panic recovery. Per-handler concerns (signature
// verification, idempotency) stay inside the handlers themselves.
func (s *Server) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Printf("ERROR: echo: handler panic on %s: %v", r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		h.ServeHTTP(w, r)
	})
}

// health is the unauthenticated health endpoint. Returns enough info
// for upstream proxies (ngrok, Cloudflare tunnel) to confirm the
// daemon is up without exposing any secrets.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"listening": true,
		"version":   buildinfo.Version,
	})
}

// IsTooLarge reports whether err came from `http.MaxBytesReader`. Used
// by per-provider handlers to translate the read error into 413.
func IsTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
