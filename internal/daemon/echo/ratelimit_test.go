package echo

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// TestRateLimiterBucketDrains: a single IP that bursts past the
// bucket capacity gets the first `perMinute` requests and then 429s
// the rest. Backs the synthetic-flood acceptance criterion.
func TestRateLimiterBucketDrains(t *testing.T) {
	l := NewRateLimiter(5)
	l.SetClockForTest(func() time.Time { return time.Unix(1_000_000, 0).UTC() })

	for i := 0; i < 5; i++ {
		ok, _ := l.Allow("1.2.3.4")
		if !ok {
			t.Fatalf("allow #%d: expected allow within bucket capacity", i+1)
		}
	}

	ok, warn := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("6th request should be blocked once the bucket drains")
	}
	if !warn {
		t.Fatal("first 429 for an IP should signal warn=true so caller emits a single WARN log")
	}

	ok, warn = l.Allow("1.2.3.4")
	if ok {
		t.Fatal("7th request still blocked")
	}
	if warn {
		t.Fatal("subsequent 429s in the same minute should suppress the warn flag (log dedup)")
	}
}

// TestRateLimiterRefill: tokens regenerate proportionally to elapsed
// time. After draining the bucket, advancing the clock by the full
// minute restores capacity.
func TestRateLimiterRefill(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	l := NewRateLimiter(60) // 1 token/sec
	l.SetClockForTest(func() time.Time { return now })

	// Drain the bucket.
	for i := 0; i < 60; i++ {
		ok, _ := l.Allow("1.1.1.1")
		if !ok {
			t.Fatalf("drain #%d should pass", i+1)
		}
	}
	if ok, _ := l.Allow("1.1.1.1"); ok {
		t.Fatal("drained bucket should block")
	}

	// 30s elapsed → 30 tokens regenerated at 1/s.
	now = now.Add(30 * time.Second)
	for i := 0; i < 30; i++ {
		ok, _ := l.Allow("1.1.1.1")
		if !ok {
			t.Fatalf("refill #%d should pass after 30s", i+1)
		}
	}
	if ok, _ := l.Allow("1.1.1.1"); ok {
		t.Fatal("31st request after only 30s of refill should still block")
	}

	// Full minute → fully refilled to capacity.
	now = now.Add(60 * time.Second)
	if got := l.Tokens("1.1.1.1"); got < 60-0.0001 {
		t.Fatalf("expected ~60 tokens after full minute, got %.3f", got)
	}
}

// TestRateLimiterPerIPIsolation: a flood from one IP must not block
// requests from another. This is the per-IP-isolation acceptance
// criterion.
func TestRateLimiterPerIPIsolation(t *testing.T) {
	l := NewRateLimiter(3)
	l.SetClockForTest(func() time.Time { return time.Unix(1_000_000, 0).UTC() })

	// Drain IP A.
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("ip A request #%d should pass", i+1)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("ip A 4th request should be blocked")
	}

	// IP B's bucket is untouched.
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("b"); !ok {
			t.Fatalf("ip B request #%d should pass — A's flood leaked into B", i+1)
		}
	}
}

// TestRateLimiterRefund: refunding a token (e.g. on idempotent
// re-delivery) restores capacity, capped at the bucket limit.
func TestRateLimiterRefund(t *testing.T) {
	l := NewRateLimiter(5)
	now := time.Unix(1_000_000, 0).UTC()
	l.SetClockForTest(func() time.Time { return now })

	// Drain to the floor.
	for i := 0; i < 5; i++ {
		l.Allow("ip")
	}
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("expected drained bucket")
	}

	// Refund — next Allow should pass.
	l.Refund("ip")
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("refund should give back exactly one token")
	}

	// Refund cannot exceed capacity.
	for i := 0; i < 100; i++ {
		l.Refund("ip")
	}
	if got := l.Tokens("ip"); got > float64(5)+0.0001 {
		t.Fatalf("refund exceeded capacity: %.3f tokens (cap=5)", got)
	}
}

// TestRateLimiterDisabled: a nil limiter (perMinute <= 0) is a
// permissive no-op.
func TestRateLimiterDisabled(t *testing.T) {
	l := NewRateLimiter(0)
	if l != nil {
		t.Fatal("perMinute=0 should yield a nil limiter")
	}
	// Calling through nil receiver is safe.
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("nil limiter should always allow")
	}
	l.Refund("ip") // must not panic
}

// TestRateLimiterWarnInterval: after the per-IP warn cool-down
// elapses, the next 429 surfaces a fresh warn=true so a sustained
// flooder still gets one log line per minute.
func TestRateLimiterWarnInterval(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	l := NewRateLimiter(1)
	l.SetClockForTest(func() time.Time { return now })

	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("first request should pass")
	}

	// 2nd request — first 429 → warn.
	if ok, warn := l.Allow("ip"); ok || !warn {
		t.Fatalf("expected first 429 with warn=true, got ok=%v warn=%v", ok, warn)
	}

	// 3rd request — within cooldown → no warn.
	if ok, warn := l.Allow("ip"); ok || warn {
		t.Fatalf("expected 429 with warn=false inside cooldown, got ok=%v warn=%v", ok, warn)
	}

	// Advance past cooldown — cooldown elapses but the bucket has also
	// refilled. Burn the refilled token to drain again, then expect a
	// fresh warn.
	now = now.Add(rateLimitWarnInterval + 5*time.Second)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("post-cooldown burn (token refilled by then) should pass")
	}
	if ok, warn := l.Allow("ip"); ok || !warn {
		t.Fatalf("expected warn=true again past cooldown, got ok=%v warn=%v", ok, warn)
	}
}

// TestRateLimiterEmptyIP: an empty IP is treated as allowed (rather
// than being merged into a shared "unknown" bucket that creates a
// stampede pathology when an upstream proxy strips the address).
func TestRateLimiterEmptyIP(t *testing.T) {
	l := NewRateLimiter(1)
	for i := 0; i < 1000; i++ {
		if ok, _ := l.Allow(""); !ok {
			t.Fatal("empty IP should always be allowed")
		}
	}
}

// TestServerWrapBlocksFlood: end-to-end through the wrap middleware —
// a synthetic 50-request flood from a single IP fills the bucket and
// the rest 429. A different IP still 200s. Backs the spec's acceptance
// criterion: "Synthetic flood (50 req/sec from one IP) → 429 after the
// bucket fills, no daemon CPU pegging; legitimate traffic from a
// different IP still 200s."
func TestServerWrapBlocksFlood(t *testing.T) {
	logBuf := &bytes.Buffer{}
	logger := log.New(logBuf, "", 0)
	srv := New(models.InboundConfig{RateLimitPerMin: 5}, logger)

	var hits int
	srv.RegisterProvider("POST", "/echo/test", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))

	// 50 requests from the same IP.
	allowed := 0
	rejected := 0
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodPost, "/echo/test", strings.NewReader(""))
		req.RemoteAddr = "9.9.9.9:55555"
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		switch rec.Code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Fatalf("unexpected status %d for request %d", rec.Code, i)
		}
	}
	if allowed != 5 {
		t.Fatalf("flood should let 5 through, got %d", allowed)
	}
	if rejected != 45 {
		t.Fatalf("flood should reject 45, got %d", rejected)
	}
	if hits != 5 {
		t.Fatalf("downstream handler should run only 5 times, got %d", hits)
	}

	// A different IP still 200s.
	req := httptest.NewRequest(http.MethodPost, "/echo/test", strings.NewReader(""))
	req.RemoteAddr = "10.10.10.10:55556"
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("untouched IP got %d, expected 200", rec.Code)
	}

	// Retry-After header surfaces on 429.
	req = httptest.NewRequest(http.MethodPost, "/echo/test", strings.NewReader(""))
	req.RemoteAddr = "9.9.9.9:55557"
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("expected Retry-After header on 429")
	}

	// Single WARN line emitted across the whole flood (per-IP-per-minute dedup).
	warnings := strings.Count(logBuf.String(), "rate limit exceeded for 9.9.9.9")
	if warnings != 1 {
		t.Fatalf("expected exactly one WARN line for the flooding IP, got %d. log:\n%s", warnings, logBuf.String())
	}
}

// TestServerWrapDisabledLetsAllPass: with RateLimitPerMin=-1 the
// limiter is disabled and a sustained flood does not 429. Used to
// confirm the master-disable path works for users on a fully-tunneled
// install with their own upstream rate limiter.
func TestServerWrapDisabledLetsAllPass(t *testing.T) {
	srv := New(models.InboundConfig{RateLimitPerMin: -1}, log.New(&bytes.Buffer{}, "", 0))
	if srv.Limiter() != nil {
		t.Fatal("RateLimitPerMin=-1 should produce a nil limiter")
	}
	srv.RegisterProvider("POST", "/echo/test", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodPost, "/echo/test", strings.NewReader(""))
		req.RemoteAddr = "1.1.1.1:1000"
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request #%d got %d with limiter disabled", i+1, rec.Code)
		}
	}
}

// TestServerWrapDefaultsTo30: an empty RateLimitPerMin defaults to 30
// (DefaultRateLimitPerMin), matching the spec's default budget.
func TestServerWrapDefaultsTo30(t *testing.T) {
	srv := New(models.InboundConfig{}, log.New(&bytes.Buffer{}, "", 0))
	if srv.Limiter() == nil {
		t.Fatal("zero-value config should default-enable the limiter")
	}
	if srv.Limiter().perMinute != DefaultRateLimitPerMin {
		t.Fatalf("expected default %d, got %d", DefaultRateLimitPerMin, srv.Limiter().perMinute)
	}
}

// TestRateLimiterConcurrency exercises the limiter under concurrent
// access — a coarse race-detector smoke test, not a perf benchmark.
func TestRateLimiterConcurrency(t *testing.T) {
	l := NewRateLimiter(1000)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ip := "10.0.0." + itoaForTest(n%10)
			for j := 0; j < 100; j++ {
				l.Allow(ip)
				if j%5 == 0 {
					l.Refund(ip)
				}
			}
		}(i)
	}
	wg.Wait()
}

// itoaForTest avoids pulling strconv into the test for a single use.
func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestClientIPExtraction: X-Forwarded-For wins over RemoteAddr;
// leftmost entry wins on chained proxies; X-Real-IP is the second
// fallback before bare RemoteAddr parsing. Critical for tunneled
// deployments where every request shares the loopback RemoteAddr.
func TestClientIPExtraction(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		xRealIP    string
		remoteAddr string
		want       string
	}{
		{"plain remote", "", "", "192.0.2.1:12345", "192.0.2.1"},
		{"xff single", "203.0.113.5", "", "127.0.0.1:1", "203.0.113.5"},
		{"xff chained leftmost", "203.0.113.5, 198.51.100.1", "", "127.0.0.1:1", "203.0.113.5"},
		{"x-real-ip fallback", "", "203.0.113.9", "127.0.0.1:1", "203.0.113.9"},
		{"xff overrides x-real-ip", "203.0.113.5", "198.51.100.1", "127.0.0.1:1", "203.0.113.5"},
		{"remote no port", "", "", "192.0.2.7", "192.0.2.7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/echo/health", nil)
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.xRealIP != "" {
				r.Header.Set("X-Real-IP", c.xRealIP)
			}
			r.RemoteAddr = c.remoteAddr
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP got=%q want=%q", got, c.want)
			}
		})
	}
}

// TestRefundRateLimitNoOpWhenNotStamped: calling RefundRateLimit on a
// request whose context wasn't stamped by the middleware (e.g. a unit
// test that never went through wrap) is a no-op rather than a panic.
func TestRefundRateLimitNoOpWhenNotStamped(t *testing.T) {
	srv := New(models.InboundConfig{RateLimitPerMin: 5}, log.New(&bytes.Buffer{}, "", 0))
	r := httptest.NewRequest(http.MethodPost, "/echo/test", nil)
	srv.RefundRateLimit(r) // must not panic
	// Limiter should still hold full capacity (no spurious refund).
	if got := srv.Limiter().Tokens("anything"); got != 5 {
		t.Fatalf("expected fresh bucket cap=5, got %.3f", got)
	}
}
