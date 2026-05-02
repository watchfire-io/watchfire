package relay

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
)

// stubAdapter is a test-only Adapter that records every Send call and
// returns whatever sendErr is set. It implements IsProjectMuted to
// exercise the dispatcher's mute short-circuit.
type stubAdapter struct {
	id        string
	supports  map[notify.Kind]bool
	mutedIDs  map[string]bool
	sendErr   error
	mu        sync.Mutex
	calls     []Payload
}

func (s *stubAdapter) ID() string                    { return s.id }
func (s *stubAdapter) Kind() string                  { return "stub" }
func (s *stubAdapter) Supports(k notify.Kind) bool   { return s.supports[k] }
func (s *stubAdapter) IsProjectMuted(p string) bool  { return s.mutedIDs[p] }
func (s *stubAdapter) Send(_ context.Context, p Payload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, p)
	return s.sendErr
}
func (s *stubAdapter) Calls() []Payload {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Payload, len(s.calls))
	copy(out, s.calls)
	return out
}

func passthroughResolver(n notify.Notification) (Payload, error) {
	return Payload{
		Version:    1,
		Kind:       string(n.Kind),
		EmittedAt:  n.EmittedAt,
		ProjectID:  n.ProjectID,
		TaskNumber: int(n.TaskNumber),
	}, nil
}

func TestDispatcherFanOutOnlyToSupportingAdapters(t *testing.T) {
	a := &stubAdapter{id: "a", supports: map[notify.Kind]bool{notify.KindTaskFailed: true}}
	b := &stubAdapter{id: "b", supports: map[notify.Kind]bool{notify.KindRunComplete: true}}
	c := &stubAdapter{id: "c", supports: map[notify.Kind]bool{notify.KindTaskFailed: true, notify.KindRunComplete: true}}

	bus := notify.NewBus()
	d := NewDispatcher(
		bus,
		passthroughResolver,
		func() ([]Adapter, error) { return []Adapter{a, b, c}, nil },
		WithRetryDelays(nil),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	defer d.Stop()

	bus.Emit(notify.Notification{
		Kind:      notify.KindTaskFailed,
		ProjectID: "p1",
		EmittedAt: time.Now(),
	})

	// Allow the dispatcher goroutine to drain.
	if !waitFor(t, 250*time.Millisecond, func() bool {
		return len(a.Calls()) == 1 && len(b.Calls()) == 0 && len(c.Calls()) == 1
	}) {
		t.Fatalf("fan-out mismatch: a=%d b=%d c=%d", len(a.Calls()), len(b.Calls()), len(c.Calls()))
	}
}

func TestDispatcherSkipsMutedProject(t *testing.T) {
	a := &stubAdapter{
		id:       "a",
		supports: map[notify.Kind]bool{notify.KindTaskFailed: true},
		mutedIDs: map[string]bool{"muted-project": true},
	}
	bus := notify.NewBus()
	d := NewDispatcher(
		bus, passthroughResolver,
		func() ([]Adapter, error) { return []Adapter{a}, nil },
		WithRetryDelays(nil),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	defer d.Stop()

	bus.Emit(notify.Notification{Kind: notify.KindTaskFailed, ProjectID: "muted-project"})
	bus.Emit(notify.Notification{Kind: notify.KindTaskFailed, ProjectID: "other-project"})

	if !waitFor(t, 250*time.Millisecond, func() bool { return len(a.Calls()) == 1 }) {
		t.Fatalf("expected 1 send (muted project skipped), got %d", len(a.Calls()))
	}
	if got := a.Calls()[0].ProjectID; got != "other-project" {
		t.Fatalf("expected only other-project to land, got %q", got)
	}
}

func TestDispatcherReloadSwapsAdapters(t *testing.T) {
	first := &stubAdapter{id: "first", supports: map[notify.Kind]bool{notify.KindTaskFailed: true}}
	second := &stubAdapter{id: "second", supports: map[notify.Kind]bool{notify.KindTaskFailed: true}}

	var which atomic.Int32
	which.Store(0)
	factory := func() ([]Adapter, error) {
		if which.Load() == 0 {
			return []Adapter{first}, nil
		}
		return []Adapter{second}, nil
	}

	bus := notify.NewBus()
	d := NewDispatcher(bus, passthroughResolver, factory, WithRetryDelays(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	defer d.Stop()

	bus.Emit(notify.Notification{Kind: notify.KindTaskFailed, ProjectID: "p"})
	if !waitFor(t, 250*time.Millisecond, func() bool { return len(first.Calls()) == 1 }) {
		t.Fatalf("first adapter not invoked: %d", len(first.Calls()))
	}

	which.Store(1)
	d.Reload()

	bus.Emit(notify.Notification{Kind: notify.KindTaskFailed, ProjectID: "p"})
	if !waitFor(t, 250*time.Millisecond, func() bool { return len(second.Calls()) == 1 }) {
		t.Fatalf("post-reload: second adapter not invoked: %d", len(second.Calls()))
	}
	if got := len(first.Calls()); got != 1 {
		t.Fatalf("first should still have only one call, got %d", got)
	}
}

// TestDispatcherCircuitBreakerWindowSlides asserts that failures aging
// out of the rolling window stop counting toward the breaker — a
// quiet hour reopens a previously-tripped breaker.
func TestDispatcherCircuitBreakerWindowSlides(t *testing.T) {
	a := &stubAdapter{
		id:       "flaky",
		supports: map[notify.Kind]bool{notify.KindTaskFailed: true},
		sendErr:  errors.New("server down"),
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	clock := &testClock{t: now}

	d := NewDispatcher(
		nil,
		passthroughResolver,
		func() ([]Adapter, error) { return []Adapter{a}, nil },
		WithRetryDelays([]time.Duration{1 * time.Millisecond, 1 * time.Millisecond}),
		WithCircuitBreaker(5*time.Minute, 3),
		WithClock(clock.Now),
	)

	for i := 0; i < 3; i++ {
		d.sendWithRetry(context.Background(), a, samplePayload())
	}
	if !d.breakerOpen("flaky") {
		t.Fatalf("expected breaker open after 3 failures")
	}

	clock.t = now.Add(6 * time.Minute)
	if d.breakerOpen("flaky") {
		t.Fatalf("expected breaker closed after window slides past")
	}
}

// TestDispatcherStopsOnContextCancel asserts the goroutine returns when
// its parent context is cancelled.
func TestDispatcherStopsOnContextCancel(t *testing.T) {
	bus := notify.NewBus()
	d := NewDispatcher(
		bus, passthroughResolver,
		func() ([]Adapter, error) { return nil, nil },
	)
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	cancel()

	select {
	case <-d.done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not exit after context cancel")
	}
}

// testClock is a deterministic clock for breaker-window tests.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// waitFor polls cond until it returns true or timeout elapses; returns
// true on success. Used to wait on the dispatcher goroutine's
// asynchronous fan-out without a fixed sleep.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}
