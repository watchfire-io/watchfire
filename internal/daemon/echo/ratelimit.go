package echo

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultRateLimitPerMin is the per-IP token-bucket budget applied
// across all `/echo/*` routes when `InboundConfig.RateLimitPerMin`
// is unset. 30/min is comfortably above the highest-volume legitimate
// inbound (Slack interactivity batches a handful per click; GitHub
// `pull_request.closed` fires once per merge) while still gating a
// flood of malformed-signature junk before it reaches the verify
// path.
const DefaultRateLimitPerMin = 30

// rateLimitWarnInterval is the per-IP minimum gap between 429 WARN
// log lines. A flooder hitting `/echo/*` from a single IP otherwise
// fills the daemon's log at the request rate; one line per minute is
// enough to surface "this IP is hot" without drowning real signal.
const rateLimitWarnInterval = time.Minute

// RateLimiter is a per-IP token-bucket gate. Each IP gets an
// independent bucket of size `perMinute`, refilled continuously at
// `perMinute` tokens per minute. `Allow` consumes a token (returning
// false if the bucket is empty) and `Refund` returns one — the latter
// is called from inside per-handler idempotency-cache hits so legit
// retries are not penalised (see commentary in `server.go:wrap`).
//
// A zero-value or nil RateLimiter is a no-op (Allow always returns
// true) so callers don't need to gate the call themselves; this is
// what makes `RateLimitPerMin: 0` mean "disable the limiter".
type RateLimiter struct {
	mu        sync.Mutex
	perMinute int
	buckets   map[string]*ipBucket
	now       func() time.Time
}

type ipBucket struct {
	tokens   float64
	lastFill time.Time
	lastWarn time.Time
}

// NewRateLimiter returns a per-IP token bucket sized at `perMinute`
// requests / minute. perMinute <= 0 disables the limiter (Allow is a
// no-op true). The clock seam is exposed via `SetClockForTest`.
func NewRateLimiter(perMinute int) *RateLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &RateLimiter{
		perMinute: perMinute,
		buckets:   make(map[string]*ipBucket),
		now:       time.Now,
	}
}

// SetClockForTest swaps the wall-clock for deterministic tests. Pass
// nil to reset to time.Now.
func (l *RateLimiter) SetClockForTest(fn func() time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if fn == nil {
		l.now = time.Now
		return
	}
	l.now = fn
}

// Allow consumes a token from the bucket associated with `ip`. The
// returned `warn` is true when this is the first 429 for that IP
// inside the last `rateLimitWarnInterval` — callers use it to emit a
// single WARN log per minute per IP rather than one per blocked
// request. A nil RateLimiter is a permissive no-op.
func (l *RateLimiter) Allow(ip string) (allowed bool, warn bool) {
	if l == nil || l.perMinute <= 0 {
		return true, false
	}
	if ip == "" {
		// Unknown remote — treat as allowed; the alternative (single
		// shared bucket for all "unknown" callers) creates a stampede
		// pathology when a misconfigured upstream reverse-proxy strips
		// the address.
		return true, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{
			tokens:   float64(l.perMinute),
			lastFill: now,
		}
		l.buckets[ip] = b
	}
	l.refillLocked(b, now)

	if b.tokens >= 1 {
		b.tokens--
		return true, false
	}

	if now.Sub(b.lastWarn) >= rateLimitWarnInterval {
		b.lastWarn = now
		return false, true
	}
	return false, false
}

// Refund returns one token to the bucket for `ip`, capped at the
// bucket's capacity. Used by per-provider handlers when an idempotent
// re-delivery hit the LRU cache and produced no work — the upstream
// provider's retry policy should not consume the budget reserved for
// novel deliveries. A nil RateLimiter / unknown IP is a no-op.
func (l *RateLimiter) Refund(ip string) {
	if l == nil || l.perMinute <= 0 || ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		return
	}
	l.refillLocked(b, l.now())
	limit := float64(l.perMinute)
	b.tokens++
	if b.tokens > limit {
		b.tokens = limit
	}
}

// Tokens reports the current token count for `ip` after refill. Used
// only by tests; production callers go through Allow / Refund.
func (l *RateLimiter) Tokens(ip string) float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		return float64(l.perMinute)
	}
	l.refillLocked(b, l.now())
	return b.tokens
}

func (l *RateLimiter) refillLocked(b *ipBucket, now time.Time) {
	if !b.lastFill.IsZero() && now.After(b.lastFill) {
		elapsed := now.Sub(b.lastFill).Seconds()
		add := elapsed * (float64(l.perMinute) / 60.0)
		b.tokens += add
		if limit := float64(l.perMinute); b.tokens > limit {
			b.tokens = limit
		}
	}
	b.lastFill = now
}

// rateLimitCtxKey is the private context key used to thread the
// caller IP from the rate-limit middleware down to per-provider
// handlers, so handlers can call `RefundRateLimit` after detecting
// an idempotent replay without needing direct access to the limiter.
type rateLimitCtxKey struct{}

// withRateLimitIP returns a child request whose context carries the
// extracted IP under the package-private key consumed by
// `RefundRateLimit`. Empty IPs are not stamped (keeps the absence
// case identical to the no-limiter case).
func withRateLimitIP(r *http.Request, ip string) *http.Request {
	if ip == "" {
		return r
	}
	ctx := context.WithValue(r.Context(), rateLimitCtxKey{}, ip)
	return r.WithContext(ctx)
}

// RefundRateLimit returns one token to the calling IP's bucket if a
// rate limiter is wired and the middleware stamped the IP into the
// request context. Per-provider handlers call this after a successful
// `Cache.Seen() == true` branch so legitimate retries do not consume
// budget. Safe to call from any context — a missing limiter / IP is
// a no-op.
func (s *Server) RefundRateLimit(r *http.Request) {
	if s == nil || s.limiter == nil {
		return
	}
	ip, _ := r.Context().Value(rateLimitCtxKey{}).(string)
	if ip == "" {
		return
	}
	s.limiter.Refund(ip)
}

// clientIP extracts the caller address from the request. Loopback
// deployments resolve to `RemoteAddr`; tunnel deployments (ngrok,
// Cloudflare Tunnel, reverse proxy) fan out into `X-Forwarded-For`,
// where we honour the leftmost (origin-most) entry. The first byte
// after a port colon is stripped so the bucket key is the host alone.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			xff = xff[:comma]
		}
		xff = strings.TrimSpace(xff)
		if xff != "" {
			return xff
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
