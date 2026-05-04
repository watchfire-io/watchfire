package oauth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// CallbackResult is the structured outcome of a completed OAuth flow.
// Exactly one of `SlackInstall` / `DiscordInstall` is non-nil on
// success; `Err` is set on any failure (CSRF reject, denied consent,
// token exchange error). Either way, the result is delivered through
// the channel returned by `Server.Wait`.
type CallbackResult struct {
	Provider       string
	SlackInstall   *SlackInstall
	DiscordInstall *DiscordInstall
	DefaultChannel string
	Err            error
}

// Server is a one-shot loopback HTTP listener that catches the OAuth
// callback redirect, exchanges the code for a token, and surfaces the
// result through `Wait`. The listener tears itself down after the
// first delivery (or after `ctx` cancels — whichever comes first).
//
// Loopback-only: bind address is always `127.0.0.1:<port>`. Browser
// redirects from slack.com / discord.com to a local URL is supported
// because the user is the one visiting the link; no cross-origin
// concerns.
type Server struct {
	store     *StateStore
	logger    *log.Logger
	mu        sync.Mutex
	listener  net.Listener
	httpSrv   *http.Server
	resultCh  chan CallbackResult
	closed    bool
	port      int
	httpClient *http.Client
}

// NewServer returns a callback server bound to a free loopback port.
// Caller must call `Run(ctx)` to start serving and `Wait` to receive
// the result.
func NewServer(store *StateStore, httpClient *http.Client, logger *log.Logger) (*Server, error) {
	if logger == nil {
		logger = log.Default()
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("oauth: bind callback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return &Server{
		store:     store,
		logger:    logger,
		listener:  listener,
		resultCh:  make(chan CallbackResult, 1),
		port:      port,
		httpClient: httpClient,
	}, nil
}

// Port is the TCP port the server is bound to. Used to construct the
// `redirect_uri` the caller passes to the upstream provider.
func (s *Server) Port() int { return s.port }

// RedirectURI returns the `http://127.0.0.1:<port>/oauth/<provider>/callback`
// URL the upstream provider should redirect the user to.
func (s *Server) RedirectURI(provider string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/oauth/%s/callback", s.port, provider)
}

// Run starts serving and blocks until ctx cancels OR the first
// callback completes. After a callback completes the listener tears
// down so the next OAuth attempt gets a fresh port (and a fresh
// resultCh).
func (s *Server) Run(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/slack/callback", func(w http.ResponseWriter, r *http.Request) {
		s.handleCallback(r.Context(), w, r, "slack")
	})
	mux.HandleFunc("/oauth/discord/callback", func(w http.ResponseWriter, r *http.Request) {
		s.handleCallback(r.Context(), w, r, "discord")
	})
	mux.HandleFunc("/oauth/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(s.listener) }()

	select {
	case <-ctx.Done():
		s.shutdown()
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Printf("WARN: oauth: callback server stopped: %v", err)
		}
	}
}

// Wait blocks until the callback fires or ctx cancels. Returns the
// result of the first callback delivery; subsequent calls return the
// same result (the channel is buffered, capacity 1, and never re-used).
func (s *Server) Wait(ctx context.Context) (CallbackResult, error) {
	select {
	case res := <-s.resultCh:
		return res, nil
	case <-ctx.Done():
		return CallbackResult{}, ctx.Err()
	}
}

// shutdown is idempotent.
func (s *Server) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.httpSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.httpSrv.Shutdown(shutdownCtx)
		cancel()
	}
}

func (s *Server) handleCallback(ctx context.Context, w http.ResponseWriter, r *http.Request, provider string) {
	q := r.URL.Query()
	if errStr := q.Get("error"); errStr != "" {
		var resErr error = ErrUserDenied
		if errStr != "access_denied" {
			resErr = fmt.Errorf("oauth: %s callback error: %s", provider, errStr)
		}
		s.deliver(CallbackResult{Provider: provider, Err: resErr})
		writeCallbackPage(w, false, fmt.Sprintf("Authorization rejected: %s", errStr))
		return
	}
	state := q.Get("state")
	code := q.Get("code")
	if state == "" || code == "" {
		s.deliver(CallbackResult{Provider: provider, Err: ErrMissingCode})
		writeCallbackPage(w, false, "Missing state or code parameter.")
		return
	}
	clientID, clientSecret, redirectURI, defaultChannel, err := s.store.Consume(provider, state)
	if err != nil {
		s.deliver(CallbackResult{Provider: provider, Err: err})
		writeCallbackPage(w, false, fmt.Sprintf("State validation failed: %v", err))
		return
	}

	switch provider {
	case "slack":
		install, exErr := ExchangeSlackCode(ctx, s.httpClient, clientID, clientSecret, code, redirectURI)
		if exErr != nil {
			s.deliver(CallbackResult{Provider: provider, Err: exErr})
			writeCallbackPage(w, false, exErr.Error())
			return
		}
		s.deliver(CallbackResult{Provider: provider, SlackInstall: install, DefaultChannel: defaultChannel})
		writeCallbackPage(w, true, fmt.Sprintf("Connected to %s as @%s", install.TeamName, install.BotUsername))
	case "discord":
		install, exErr := ExchangeDiscordCode(ctx, s.httpClient, clientID, clientSecret, code, redirectURI)
		if exErr != nil {
			s.deliver(CallbackResult{Provider: provider, Err: exErr})
			writeCallbackPage(w, false, exErr.Error())
			return
		}
		s.deliver(CallbackResult{Provider: provider, DiscordInstall: install, DefaultChannel: defaultChannel})
		who := install.Username
		if install.Discriminator != "" && install.Discriminator != "0" {
			who = fmt.Sprintf("%s#%s", install.Username, install.Discriminator)
		}
		writeCallbackPage(w, true, fmt.Sprintf("Connected to %s as %s", install.GuildName, who))
	default:
		s.deliver(CallbackResult{Provider: provider, Err: fmt.Errorf("unknown provider %q", provider)})
		writeCallbackPage(w, false, "Unknown provider.")
	}
}

// deliver pushes a result onto the channel non-blockingly. After the
// first delivery the channel is full; subsequent attempts to deliver
// from a duplicate callback get dropped on the floor.
func (s *Server) deliver(r CallbackResult) {
	select {
	case s.resultCh <- r:
	default:
	}
	go s.shutdown()
}

func writeCallbackPage(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	status := "Success"
	color := "#16a34a"
	if !ok {
		status = "Error"
		color = "#dc2626"
	}
	page := fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><title>Watchfire OAuth %s</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #0b1120; color: #f8fafc; margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
  .card { max-width: 420px; padding: 32px 28px; border-radius: 14px; background: #111827; box-shadow: 0 12px 32px rgba(0,0,0,0.4); }
  h1 { color: %s; margin: 0 0 12px; font-size: 22px; }
  p { margin: 8px 0; line-height: 1.45; color: #cbd5e1; }
  small { color: #64748b; }
</style></head>
<body><div class="card"><h1>%s</h1><p>%s</p><small>You can close this tab and return to Watchfire.</small></div></body></html>`,
		status, color, status, htmlEscape(msg))
	_, _ = w.Write([]byte(page))
}

func htmlEscape(s string) string {
	r := []byte{}
	for _, b := range []byte(s) {
		switch b {
		case '<':
			r = append(r, []byte("&lt;")...)
		case '>':
			r = append(r, []byte("&gt;")...)
		case '&':
			r = append(r, []byte("&amp;")...)
		case '"':
			r = append(r, []byte("&quot;")...)
		default:
			r = append(r, b)
		}
	}
	return string(r)
}
