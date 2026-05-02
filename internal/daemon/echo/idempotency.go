package echo

import (
	"container/list"
	"sync"
	"time"
)

// DefaultIdempotencyMaxEntries caps the number of remembered delivery
// IDs. 1000 is enough to absorb a burst of duplicate retries without
// growing unbounded; eviction is LRU.
const DefaultIdempotencyMaxEntries = 1000

// DefaultIdempotencyTTL is how long a delivery ID is considered seen.
// Discord can re-deliver an interaction up to a handful of times if
// the initial response is delayed; 24 hours is generous and matches
// the GitHub redelivery window.
const DefaultIdempotencyTTL = 24 * time.Hour

// Cache is a thread-safe LRU+TTL idempotency cache. Each `Seen(key)`
// call returns true if the key was inserted within the past TTL,
// false otherwise (and inserts the key as a side effect). Keys are
// the provider-specific delivery IDs:
//
//   - GitHub  → X-GitHub-Delivery header
//   - Slack   → form field `client_msg_id` (or trigger_id as fallback)
//   - Discord → interaction `id`
//
// Eviction is LRU: when the table exceeds maxEntries the oldest entry
// is dropped on the next insert. Independently, a per-entry deadline
// expires entries past their TTL on the next access.
type Cache struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	order      *list.List
	entries    map[string]*list.Element
	now        func() time.Time
}

type cacheEntry struct {
	key      string
	deadline time.Time
}

// NewCache returns a new Cache with the given limits. Pass 0 / 0 to
// use the package defaults. The clock is `time.Now` by default; tests
// override it via `SetClockForTest` on the returned Cache.
func NewCache(maxEntries int, ttl time.Duration) *Cache {
	if maxEntries <= 0 {
		maxEntries = DefaultIdempotencyMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultIdempotencyTTL
	}
	return &Cache{
		maxEntries: maxEntries,
		ttl:        ttl,
		order:      list.New(),
		entries:    make(map[string]*list.Element, maxEntries),
		now:        time.Now,
	}
}

// SetClockForTest overrides the wall-clock used for TTL expiry. Pass
// nil to reset to time.Now.
func (c *Cache) SetClockForTest(fn func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn == nil {
		c.now = time.Now
		return
	}
	c.now = fn
}

// Seen reports whether key was inserted within the past TTL and
// inserts it on miss. On a hit the deadline is refreshed (matches
// "treat re-delivery as a single observed delivery").
//
// An empty key is never seen and never inserted — provider handlers
// that fail to extract a delivery ID should reject the request rather
// than relying on the cache to deduplicate them.
func (c *Cache) Seen(key string) bool {
	if key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if el, ok := c.entries[key]; ok {
		entry := el.Value.(*cacheEntry)
		if now.Before(entry.deadline) {
			entry.deadline = now.Add(c.ttl)
			c.order.MoveToFront(el)
			return true
		}
		// Expired — drop and treat as miss.
		c.removeElement(el)
	}

	entry := &cacheEntry{key: key, deadline: now.Add(c.ttl)}
	el := c.order.PushFront(entry)
	c.entries[key] = el

	for c.order.Len() > c.maxEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.removeElement(oldest)
	}
	return false
}

// Len returns the number of live entries; useful for tests.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *Cache) removeElement(el *list.Element) {
	entry := el.Value.(*cacheEntry)
	c.order.Remove(el)
	delete(c.entries, entry.key)
}
