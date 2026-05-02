// Package focus provides an in-process event bus for tray-driven GUI focus
// requests. The tray emits FocusEvent messages whenever the user clicks a
// menu row that should bring a specific project / view to the foreground;
// the gRPC daemon service fans them out to every connected GUI subscriber.
package focus

import (
	"sync"
)

// Target enumerates GUI view targets a focus event can address. Mirrors the
// proto FocusTarget enum but kept here so non-proto consumers (the tray)
// don't pull in the protobuf package.
type Target int

const (
	TargetMain   Target = 0
	TargetTasks  Target = 1
	TargetTask   Target = 2
	TargetDigest Target = 3 // v6.0 Ember — open the weekly digest modal
)

// Event is a single focus request — bring the GUI's view of ProjectID into
// focus, optionally on a specific task. For TargetDigest, ProjectID is empty
// and DigestDate carries the YYYY-MM-DD identifying the digest file under
// ~/.watchfire/digests/.
type Event struct {
	ProjectID   string
	Target      Target
	TaskNumber  int32
	DigestDate  string
}

// Bus fans an Event out to every subscriber. Subscribers receive a buffered
// channel; if the buffer fills (slow consumer), events are dropped silently
// for that subscriber rather than blocking the publisher — the tray click
// path must never be slowed down by a stuck GUI client.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// New creates an empty Bus.
func New() *Bus {
	return &Bus{subs: make(map[chan Event]struct{})}
}

// Subscribe registers a new subscriber. The returned cancel func unsubscribes
// and closes the channel; callers must invoke it (typically via defer) when
// the subscription ends.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Emit broadcasts an Event to every subscriber. Slow subscribers (full
// channels) drop the event silently.
func (b *Bus) Emit(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// SubscriberCount returns the number of currently-active subscribers.
// Useful for tests.
func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
