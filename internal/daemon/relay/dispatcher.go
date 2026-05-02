package relay

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
)

// Default retry policy for v7.0 Relay outbound delivery. The schedule
// gives a slow-start (500ms) first retry, a few seconds for the second,
// and a longer 8s for the final attempt — enough to ride out a brief
// upstream blip without queueing a backlog if the receiver is genuinely
// down. Three attempts total (initial Send + 2 retries) keeps the
// per-event upper bound at ~10s before the circuit breaker takes over.
var (
	DefaultRetryDelays = []time.Duration{
		500 * time.Millisecond,
		2 * time.Second,
		8 * time.Second,
	}
	// CircuitBreakerWindow is the rolling window used to count adapter
	// failures; once an adapter exhausts retries `CircuitBreakerThreshold`
	// times within this window the breaker opens and subsequent Sends
	// are short-circuited until the window expires.
	CircuitBreakerWindow = 5 * time.Minute
	// CircuitBreakerThreshold is the count of hard failures (retries
	// exhausted) tolerated inside `CircuitBreakerWindow` before the
	// breaker opens for that adapter.
	CircuitBreakerThreshold = 3
)

// PayloadResolver lifts a Notification into a fully-populated Payload.
// Implementations look up the project name + color and the per-task
// title + failure reason via whatever stores the daemon has on hand.
// The resolver runs on the dispatcher goroutine, so it must be cheap or
// non-blocking; the dispatcher does not await any per-adapter Send
// before invoking the next one.
type PayloadResolver func(notify.Notification) (Payload, error)

// AdapterFactory returns the current set of outbound adapters. The
// dispatcher invokes it at construction and again on every Reload so
// adapters can be rebuilt against fresh integrations.yaml + secrets.
type AdapterFactory func() ([]Adapter, error)

// Dispatcher owns the live fan-out from `notify.Bus` to every
// configured outbound adapter. It runs a single goroutine that pulls
// from a Subscribe channel and dispatches each notification to every
// adapter whose `Supports(kind)` returns true and whose per-project
// mute does not include the source project.
//
// Per-adapter retry: 3 attempts with `DefaultRetryDelays` between them.
// Per-adapter circuit breaker: 3 hard failures within 5 minutes opens
// the breaker so a misconfigured endpoint doesn't flood the daemon log.
type Dispatcher struct {
	bus      *notify.Bus
	resolve  PayloadResolver
	factory  AdapterFactory
	logger   *log.Logger
	retry    []time.Duration
	cbWindow time.Duration
	cbLimit  int
	now      func() time.Time

	mu       sync.RWMutex
	adapters []Adapter
	cbState  map[string]*breakerState

	subCh     <-chan notify.Notification
	subCancel func()

	stop chan struct{}
	done chan struct{}
}

// breakerState tracks recent hard-failure timestamps per adapter for
// the rolling-window circuit breaker.
type breakerState struct {
	failures []time.Time
}

// DispatcherOption customises a Dispatcher at construction. The
// production path uses no options; tests inject a fixed clock + tighter
// retry delays so the suite finishes in milliseconds rather than tens
// of seconds.
type DispatcherOption func(*Dispatcher)

// WithRetryDelays overrides the default per-attempt backoff. Pass an
// empty slice to disable retries (initial attempt only).
func WithRetryDelays(delays []time.Duration) DispatcherOption {
	return func(d *Dispatcher) { d.retry = append([]time.Duration(nil), delays...) }
}

// WithCircuitBreaker overrides the default 3-failures-in-5-minutes
// breaker policy. A `limit` of 0 disables the breaker entirely.
func WithCircuitBreaker(window time.Duration, limit int) DispatcherOption {
	return func(d *Dispatcher) {
		d.cbWindow = window
		d.cbLimit = limit
	}
}

// WithLogger swaps the default `log.Default()` for an injected logger.
// Tests pass a logger backed by a `bytes.Buffer` so they can assert
// exact log lines without leaking into stderr.
func WithLogger(logger *log.Logger) DispatcherOption {
	return func(d *Dispatcher) { d.logger = logger }
}

// WithClock injects a deterministic time source for tests.
func WithClock(now func() time.Time) DispatcherOption {
	return func(d *Dispatcher) { d.now = now }
}

// NewDispatcher builds a Dispatcher subscribing to bus with adapters
// resolved through factory. Call `Run(ctx)` to start the goroutine and
// `Stop()` to terminate.
func NewDispatcher(bus *notify.Bus, resolve PayloadResolver, factory AdapterFactory, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		bus:      bus,
		resolve:  resolve,
		factory:  factory,
		logger:   log.Default(),
		retry:    append([]time.Duration(nil), DefaultRetryDelays...),
		cbWindow: CircuitBreakerWindow,
		cbLimit:  CircuitBreakerThreshold,
		now:      time.Now,
		cbState:  make(map[string]*breakerState),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(d)
	}
	d.rebuildAdapters()
	// Subscribe up-front so any Emit between NewDispatcher and Run
	// lands in the buffered subscription channel rather than racing
	// against subscriber registration.
	if bus != nil {
		d.subCh, d.subCancel = bus.Subscribe()
	}
	return d
}

// Adapters returns a snapshot of the dispatcher's current adapter list.
// Used by introspection callers (CLI `integrations test`, GUI debug UI)
// and by tests; the slice is a copy so the caller cannot mutate the
// dispatcher's internal state.
func (d *Dispatcher) Adapters() []Adapter {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Adapter, len(d.adapters))
	copy(out, d.adapters)
	return out
}

// Reload rebuilds the adapter list from the factory. The server calls
// this on `EventIntegrationsChanged`. Failures inside the factory are
// logged and the previous adapter slice is preserved so a malformed
// edit doesn't drain the dispatcher mid-flight.
func (d *Dispatcher) Reload() {
	d.rebuildAdapters()
}

func (d *Dispatcher) rebuildAdapters() {
	if d.factory == nil {
		return
	}
	adapters, err := d.factory()
	if err != nil {
		d.logger.Printf("WARN: relay dispatcher: rebuild adapters: %v", err)
		return
	}
	d.mu.Lock()
	d.adapters = adapters
	// Reset breaker state for adapters that disappeared so a future
	// re-add starts with a clean slate.
	live := make(map[string]struct{}, len(adapters))
	for _, a := range adapters {
		live[a.ID()] = struct{}{}
	}
	for id := range d.cbState {
		if _, ok := live[id]; !ok {
			delete(d.cbState, id)
		}
	}
	d.mu.Unlock()
}

// Run drains the dispatcher's bus subscription (registered in
// NewDispatcher) and fans each notification out to every adapter that
// supports its kind. Returns when ctx is cancelled or Stop is called.
// It is safe to call Run at most once per Dispatcher.
func (d *Dispatcher) Run(ctx context.Context) {
	defer close(d.done)
	if d.subCancel != nil {
		defer d.subCancel()
	}
	if d.subCh == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case n, ok := <-d.subCh:
			if !ok {
				return
			}
			d.dispatch(ctx, n)
		}
	}
}

// Stop signals the Run goroutine to exit and blocks until it has.
func (d *Dispatcher) Stop() {
	select {
	case <-d.stop:
		// already stopped
	default:
		close(d.stop)
	}
	<-d.done
}

// dispatch fans n out to every adapter that supports the kind and is
// not muted for the project. Each adapter Send runs through the retry
// + circuit-breaker policy.
func (d *Dispatcher) dispatch(ctx context.Context, n notify.Notification) {
	payload, err := d.resolve(n)
	if err != nil {
		d.logger.Printf("WARN: relay dispatcher: resolve payload for %s/%s: %v", n.Kind, n.ProjectID, err)
		return
	}
	for _, a := range d.Adapters() {
		if !a.Supports(n.Kind) {
			continue
		}
		if mp, ok := a.(interface{ IsProjectMuted(string) bool }); ok && n.ProjectID != "" {
			if mp.IsProjectMuted(n.ProjectID) {
				continue
			}
		}
		d.sendWithRetry(ctx, a, payload)
	}
}

// sendWithRetry runs the per-adapter retry + circuit-breaker policy
// against a single Send. It is invoked synchronously from dispatch so
// adapters land in the order they were registered; for the retry
// budget (~10s worst case) this trades a small amount of dispatch
// latency for predictable ordering and a bounded goroutine count.
func (d *Dispatcher) sendWithRetry(ctx context.Context, a Adapter, p Payload) {
	if d.breakerOpen(a.ID()) {
		d.logger.Printf("WARN: relay dispatcher: adapter %q breaker open, skipping send", a.ID())
		return
	}

	attempts := 1 + len(d.retry)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := d.retry[attempt-1]
			select {
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			case <-time.After(delay):
			}
		}
		err := a.Send(ctx, p)
		if err == nil {
			return
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			d.logger.Printf("WARN: relay dispatcher: adapter %q send cancelled: %v", a.ID(), err)
			return
		}
		d.logger.Printf("WARN: relay dispatcher: adapter %q send attempt %d/%d failed: %v",
			a.ID(), attempt+1, attempts, err)
	}
	d.recordFailure(a.ID())
	d.logger.Printf("ERROR: relay dispatcher: relay_send_failed adapter=%q attempts=%d last_err=%v",
		a.ID(), attempts, lastErr)
}

// breakerOpen reports whether the named adapter has tripped its
// circuit breaker. A `cbLimit` of 0 disables the breaker entirely.
func (d *Dispatcher) breakerOpen(adapterID string) bool {
	if d.cbLimit <= 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.cbState[adapterID]
	if !ok {
		return false
	}
	cutoff := d.now().Add(-d.cbWindow)
	pruned := st.failures[:0]
	for _, ts := range st.failures {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	st.failures = pruned
	return len(st.failures) >= d.cbLimit
}

// recordFailure stamps a hard-failure timestamp for the named adapter.
// Called once per send that exhausted retries; the breaker opens on
// the (cbLimit)th failure inside cbWindow.
func (d *Dispatcher) recordFailure(adapterID string) {
	if d.cbLimit <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.cbState[adapterID]
	if !ok {
		st = &breakerState{}
		d.cbState[adapterID] = st
	}
	st.failures = append(st.failures, d.now())
	cutoff := d.now().Add(-d.cbWindow)
	pruned := st.failures[:0]
	for _, ts := range st.failures {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	st.failures = pruned
}
