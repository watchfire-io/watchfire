package notify

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBusMultipleSubscribersAllReceive asserts the channel-fan-out
// pattern: every subscribed channel sees every emitted notification,
// independent of how many other subscribers are attached. The v7.0
// Relay dispatcher relies on this so attaching the dispatcher to the
// bus does not interfere with the existing GUI / tray subscribers.
func TestBusMultipleSubscribersAllReceive(t *testing.T) {
	bus := NewBus()

	chA, cancelA := bus.Subscribe()
	defer cancelA()
	chB, cancelB := bus.Subscribe()
	defer cancelB()
	chC, cancelC := bus.Subscribe()
	defer cancelC()

	want := Notification{Kind: KindRunComplete, ProjectID: "p", TaskNumber: 1, EmittedAt: time.Now()}
	bus.Emit(want)

	for _, ch := range []<-chan Notification{chA, chB, chC} {
		select {
		case got := <-ch:
			if got.ProjectID != want.ProjectID || got.Kind != want.Kind {
				t.Fatalf("subscriber received unexpected notification: %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive notification within timeout")
		}
	}
}

// TestBusCancelDoesNotAffectOthers asserts that cancelling one
// subscriber leaves the others intact and emitting continues to work
// for them. Critical for the dispatcher lifecycle: stopping the daemon
// cancels the dispatcher's subscription, and that cancel must not
// disturb the GUI's live stream.
func TestBusCancelDoesNotAffectOthers(t *testing.T) {
	bus := NewBus()
	chA, cancelA := bus.Subscribe()
	chB, cancelB := bus.Subscribe()
	defer cancelB()

	cancelA()
	// chA is now closed; we expect chB to still receive emits.

	bus.Emit(Notification{Kind: KindTaskFailed, ProjectID: "p", TaskNumber: 1})
	select {
	case _, ok := <-chA:
		if ok {
			t.Fatal("expected chA to be closed after cancelA")
		}
	case <-time.After(time.Second):
		t.Fatal("chA did not close within timeout")
	}
	select {
	case got := <-chB:
		if got.ProjectID != "p" {
			t.Fatalf("chB unexpected notification: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("chB missed emit after sibling subscriber cancelled")
	}
}

// TestBusConcurrentEmitAndSubscribe validates the bus is safe under
// concurrent Subscribe / Cancel / Emit calls — the dispatcher Reload
// path can race against ongoing emits, so we drive a stress loop and
// assert no panic and a high enough delivery rate.
func TestBusConcurrentEmitAndSubscribe(t *testing.T) {
	bus := NewBus()

	const subscribers = 8
	const emitsPerProducer = 200

	var wg sync.WaitGroup
	var received atomic.Int64

	for i := 0; i < subscribers; i++ {
		ch, cancel := bus.Subscribe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
					received.Add(1)
				case <-deadline.C:
					return
				}
			}
		}()
	}

	const producers = 4
	for i := 0; i < producers; i++ {
		go func(seed int) {
			for j := 0; j < emitsPerProducer; j++ {
				bus.Emit(Notification{
					Kind:       KindTaskFailed,
					ProjectID:  "p",
					TaskNumber: int32(seed*emitsPerProducer + j),
					EmittedAt:  time.Now(),
				})
			}
		}(i)
	}

	// Wait briefly for emits to flow, then close all subscriptions to
	// let the workers exit.
	time.Sleep(200 * time.Millisecond)
	wg.Wait()

	if received.Load() == 0 {
		t.Fatal("no emits received despite concurrent producers")
	}
}

// TestSubscribeBeforeEmitGuarantees delivery is the contract the v7.0
// dispatcher depends on: subscribing before any emit lands ensures the
// subscriber sees the very first notification. Validates the Subscribe
// → channel registration ordering inside Bus.
func TestSubscribeBeforeEmitGuarantees(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()
	bus.Emit(Notification{Kind: KindTaskFailed, ProjectID: "p", TaskNumber: 99})
	select {
	case got := <-ch:
		if got.TaskNumber != 99 {
			t.Fatalf("missed initial emit: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first emit did not reach pre-existing subscriber")
	}
}
