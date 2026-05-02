package echo

import (
	"testing"
	"time"
)

func TestCacheFreshAndDuplicate(t *testing.T) {
	c := NewCache(10, time.Minute)
	if c.Seen("a") {
		t.Fatalf("first observation should miss")
	}
	if !c.Seen("a") {
		t.Fatalf("second observation should hit")
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCache(10, time.Minute)
	c.SetClockForTest(func() time.Time { return now })

	if c.Seen("a") {
		t.Fatalf("first observation should miss")
	}
	now = now.Add(2 * time.Minute)
	if c.Seen("a") {
		t.Fatalf("after TTL, observation should miss again")
	}
	// And now it's freshly inserted again
	if !c.Seen("a") {
		t.Fatalf("immediate re-observation after re-insert should hit")
	}
}

func TestCacheLRUEviction(t *testing.T) {
	c := NewCache(3, time.Hour)
	for _, k := range []string{"a", "b", "c"} {
		c.Seen(k)
	}
	if got := c.Len(); got != 3 {
		t.Fatalf("expected len 3, got %d", got)
	}
	// Insert d → evicts a (oldest)
	c.Seen("d")
	if c.Seen("a") {
		t.Fatalf("a should have been evicted")
	}
	if !c.Seen("d") {
		t.Fatalf("d should still be cached")
	}
}

func TestCacheRefreshOnHit(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCache(3, 10*time.Minute)
	c.SetClockForTest(func() time.Time { return now })

	c.Seen("a")
	now = now.Add(5 * time.Minute)
	if !c.Seen("a") { // refresh deadline
		t.Fatalf("a should still be cached")
	}
	now = now.Add(8 * time.Minute) // total 13min from initial, but 8min from refresh
	if !c.Seen("a") {
		t.Fatalf("a should still be cached after refresh")
	}
}

func TestCacheEmptyKey(t *testing.T) {
	c := NewCache(0, 0)
	if c.Seen("") {
		t.Fatalf("empty key should never be seen")
	}
	if c.Len() != 0 {
		t.Fatalf("empty key should not be inserted")
	}
}
