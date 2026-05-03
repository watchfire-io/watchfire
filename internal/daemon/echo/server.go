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
// body cap, per-IP rate limit, common log format, panic recovery.
type Server struct {
	cfg     models.InboundConfig
	logger  *log.Logger
	mux     *http.ServeMux
	limiter *RateLimiter

	mu             sync.Mutex
	listener       net.Listener
	httpSrv        *http.Server
	listening      bool
	bindErr        string
	lastDeliveries map[string]time.Time // provider → last RecordDelivery time
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
	perMin := cfg.RateLimitPerMin
	if perMin == 0 {
		perMin = DefaultRateLimitPerMin
	}
	mux := http.NewServeMux()
	s := &Server{
		cfg:            cfg,
		logger:         logger,
		mux:            mux,
		limiter:        NewRateLimiter(perMin),
		lastDeliveries: make(map[string]time.Time),
	}

	mux.HandleFunc("GET /echo/health", s.health)
	return s
}

// Limiter exposes the per-IP rate limiter for tests + handlers that
// need to refund a token after detecting an idempotent replay. Returns
// nil when the limiter is disabled (RateLimitPerMin <= 0).
func (s *Server) Limiter() *RateLimiter { return s.limiter }

// RecordDelivery stamps a per-provider last-delivery timestamp. Echo
// handlers call this after successfully verifying + processing an inbound
// request so the IntegrationsService.GetInboundStatus RPC can surface
// the freshness of each provider in the GUI / TUI without needing to
// thread a counter through every handler. Provider names are the route
// segment ("github" / "slack" / "discord") so the status RPC can map
// them by string match.
func (s *Server) RecordDelivery(provider string) {
	if provider == "" {
		return
	}
	s.mu.Lock()
	if s.lastDeliveries == nil {
		s.lastDeliveries = make(map[string]time.Time)
	}
	s.lastDeliveries[provider] = time.Now().UTC()
	s.mu.Unlock()
}

// LastDelivery returns the last successful-delivery timestamp for a
// provider, or zero time if none has been seen this process lifetime.
func (s *Server) LastDelivery(provider string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastDeliveries == nil {
		return time.Time{}
	}
	return s.lastDeliveries[provider]
}

// BindError returns the most recent bind-failure message (or empty
// string if the server is currently listening / has not been started).
// Used by the IntegrationsService.GetInboundStatus RPC to surface the
// reason a "listening: false" state is sticky.
func (s *Server) BindError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindErr
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
		s.mu.Lock()
		s.bindErr = err.Error()
		s.mu.Unlock()
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
	s.bindErr = ""
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
// per-IP rate limit, body-size cap, panic recovery. Per-handler
// concerns (signature verification, idempotency) stay inside the
// handlers themselves.
//
// The rate-limit gate runs before the body is buffered and before the
// per-handler verify path so a malformed-signature flood from one IP
// can't pin the daemon's CPU. The IP is threaded into r.Context() so
// handlers can call `s.RefundRateLimit(r)` from the idempotency-cache
// hit branch — see commentary on `RateLimiter.Refund`.
func (s *Server) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Printf("ERROR: echo: handler panic on %s: %v", r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		ip := clientIP(r)
		if allowed, warn := s.limiter.Allow(ip); !allowed {
			if warn {
				s.logger.Printf("WARN: echo: rate limit exceeded for %s on %s (further 429s suppressed for ~1m)", ip, r.URL.Path)
			}
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		r = withRateLimitIP(r, ip)
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
