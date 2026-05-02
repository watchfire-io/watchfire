package focus

import (
	"sync"
	"testing"
	"time"
)

func TestBusFanout(t *testing.T) {
	bus := New()
	c1, cancel1 := bus.Subscribe()
	defer cancel1()
	c2, cancel2 := bus.Subscribe()
	defer cancel2()

	bus.Emit(Event{ProjectID: "p1", Target: TargetTasks})

	for i, ch := range []<-chan Event{c1, c2} {
		select {
		case ev := <-ch:
			if ev.ProjectID != "p1" || ev.Target != TargetTasks {
				t.Fatalf("subscriber %d got %+v", i, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive event", i)
		}
	}
}

func TestBusUnsubscribeClosesChannel(t *testing.T) {
	bus := New()
	ch, cancel := bus.Subscribe()
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after cancel")
	}
	if bus.SubscriberCount() != 0 {
		t.Fatalf("subscriber count = %d, want 0", bus.SubscriberCount())
	}
}

func TestBusEmitNoBlockOnSlowSubscriber(t *testing.T) {
	bus := New()
	_, cancel := bus.Subscribe()
	defer cancel()

	// Fill the buffer; further emits must drop silently rather than block.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			bus.Emit(Event{ProjectID: "p"})
		}
	}()
	select {
	case <-waitFor(&wg):
	case <-time.After(time.Second):
		t.Fatal("Emit blocked on slow subscriber")
	}
}

func waitFor(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}
