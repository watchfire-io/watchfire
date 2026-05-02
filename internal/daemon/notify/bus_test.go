package notify

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMakeIDDeterministic(t *testing.T) {
	emittedAt := time.Unix(1_700_000_000, 0).UTC()
	a := MakeID(KindTaskFailed, "proj-1", 7, emittedAt)
	b := MakeID(KindTaskFailed, "proj-1", 7, emittedAt)
	if a != b {
		t.Fatalf("MakeID not deterministic: %q vs %q", a, b)
	}
}

func TestMakeIDChangesPerKindProjectTaskAndSecond(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	id0 := MakeID(KindTaskFailed, "proj-1", 7, base)

	// Different kind → different id.
	if id := MakeID(KindRunComplete, "proj-1", 7, base); id == id0 {
		t.Fatalf("expected different id when kind changes")
	}
	// Different project → different id.
	if id := MakeID(KindTaskFailed, "proj-2", 7, base); id == id0 {
		t.Fatalf("expected different id when project changes")
	}
	// Different task → different id.
	if id := MakeID(KindTaskFailed, "proj-1", 8, base); id == id0 {
		t.Fatalf("expected different id when task number changes")
	}
	// Different second → different id.
	if id := MakeID(KindTaskFailed, "proj-1", 7, base.Add(time.Second)); id == id0 {
		t.Fatalf("expected different id when emitted_at second changes")
	}
	// Same second, different sub-second → SAME id (per-second dedupe is the
	// whole point of the spec).
	if id := MakeID(KindTaskFailed, "proj-1", 7, base.Add(500*time.Millisecond)); id != id0 {
		t.Fatalf("expected same id for same second sub-second jitter")
	}
}

func TestBusSubscribeReceivesEmits(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	want := Notification{
		ID:         "abc",
		Kind:       KindTaskFailed,
		ProjectID:  "p1",
		TaskNumber: 4,
		Title:      "t",
		Body:       "b",
		EmittedAt:  time.Unix(1_700_000_000, 0).UTC(),
	}
	bus.Emit(want)

	select {
	case got := <-ch:
		if got.ID != want.ID || got.ProjectID != want.ProjectID || got.TaskNumber != want.TaskNumber {
			t.Fatalf("unexpected notification: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

func TestBusCancelClosesChannel(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe()
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed within timeout")
	}

	// Subsequent emit must not panic on the closed subscriber.
	bus.Emit(Notification{Kind: KindTaskFailed, ProjectID: "p1"})
}

func TestBusEmitNonBlockingWhenSubscriberSlow(t *testing.T) {
	bus := NewBus()
	_, cancel := bus.Subscribe()
	defer cancel()

	// Emit far more than the buffered channel capacity (16). A blocking
	// emitter would deadlock here; the spec requires non-blocking emit.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			bus.Emit(Notification{Kind: KindTaskFailed, ProjectID: "p1", TaskNumber: int32(i)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked when subscriber was slow")
	}
}

func TestNilBusSubscribeReturnsClosedChannel(t *testing.T) {
	var bus *Bus
	ch, cancel := bus.Subscribe()
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel from nil bus")
		}
	case <-time.After(time.Second):
		t.Fatal("nil bus channel not closed")
	}
	// Nil bus must accept Emit without panicking.
	bus.Emit(Notification{Kind: KindTaskFailed, ProjectID: "p1"})
}

func TestAppendLogLineWritesJSONLEntry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	n := Notification{
		ID:         "xyz",
		Kind:       KindTaskFailed,
		ProjectID:  "proj-abc",
		TaskNumber: 17,
		Title:      "Project — task #0017 failed",
		Body:       "Some failure body",
		EmittedAt:  time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := AppendLogLine(n); err != nil {
		t.Fatalf("AppendLogLine: %v", err)
	}
	logPath := filepath.Join(tmp, ".watchfire", "logs", "proj-abc", "notifications.log")
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatal("expected at least one log line")
	}
	var got Notification
	if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != n.ID || got.ProjectID != n.ProjectID || got.TaskNumber != n.TaskNumber {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Kind != KindTaskFailed {
		t.Fatalf("kind round-trip mismatch: %v", got.Kind)
	}
}

func TestAppendLogLineRejectsMissingProjectID(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := AppendLogLine(Notification{Kind: KindTaskFailed}); err == nil {
		t.Fatal("expected error for empty ProjectID")
	}
}

func TestAppendLogLineAppendsMultipleEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	for i := 0; i < 3; i++ {
		n := Notification{
			ID:         "e",
			Kind:       KindTaskFailed,
			ProjectID:  "p",
			TaskNumber: int32(i),
			EmittedAt:  time.Unix(int64(1_700_000_000+i), 0).UTC(),
		}
		if err := AppendLogLine(n); err != nil {
			t.Fatalf("AppendLogLine #%d: %v", i, err)
		}
	}
	logPath := filepath.Join(tmp, ".watchfire", "logs", "p", "notifications.log")
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	count := 0
	for sc.Scan() {
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 lines, got %d", count)
	}
}
