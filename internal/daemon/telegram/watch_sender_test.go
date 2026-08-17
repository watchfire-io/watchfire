package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// senderHarness drives a chatSender with a fake clock and recording
// send/edit hooks — no goroutines, no network, fully deterministic.
type senderHarness struct {
	sends   []string
	edits   []editRec
	editErr error
	nextID  int64
	now     time.Time
	cs      *chatSender
}

type editRec struct {
	ID   int64
	Text string
}

func newSenderHarness() *senderHarness {
	h := &senderHarness{now: time.Unix(1_700_000_000, 0)}
	h.cs = &chatSender{
		send: func(_ context.Context, text string) (int64, error) {
			h.nextID++
			h.sends = append(h.sends, text)
			return h.nextID, nil
		},
		edit: func(_ context.Context, id int64, text string) error {
			if h.editErr != nil {
				return h.editErr
			}
			h.edits = append(h.edits, editRec{ID: id, Text: text})
			return nil
		},
		now:         func() time.Time { return h.now },
		logf:        func(string, ...any) {},
		coalesce:    coalesceInterval,
		throttleGap: throttleInterval,
	}
	return h
}

func (h *senderHarness) advance(d time.Duration) { h.now = h.now.Add(d) }

func (h *senderHarness) flush() { h.cs.flush(context.Background(), false) }

func tool(text string) Emission { return Emission{Kind: EmissionToolUse, Text: text} }
func text(t string) Emission    { return Emission{Kind: EmissionAssistantText, Text: t} }

// TestSenderCoalescing: at most one send per chat per coalesce interval;
// emissions buffered in between are delivered together.
func TestSenderCoalescing(t *testing.T) {
	h := newSenderHarness()

	h.cs.Add(tool("⚒ Bash: make build"))
	h.flush()
	if len(h.sends) != 1 {
		t.Fatalf("first flush should send immediately, got %d sends", len(h.sends))
	}

	h.cs.Add(tool("⚒ Bash: make test"))
	h.flush()
	h.advance(time.Second)
	h.cs.Add(tool("⚒ Edit internal/tui/model.go"))
	h.flush()
	if len(h.sends) != 1 {
		t.Fatalf("flushes inside the coalesce interval must not send (got %d)", len(h.sends))
	}

	h.advance(1500 * time.Millisecond) // 2.5s since the first flush
	h.flush()
	if len(h.sends) != 2 {
		t.Fatalf("expected coalesced send after 2.5s, got %d sends", len(h.sends))
	}
	// Both buffered tool lines ride one message, packed one per line.
	if want := "<i>⚒ Bash: make test</i>\n<i>⚒ Edit internal/tui/model.go</i>"; h.sends[1] != want {
		t.Fatalf("coalesced message = %q, want %q", h.sends[1], want)
	}
}

// TestSenderEditInPlaceGrowth: a growing assistant message is edited in
// place; past the size ceiling a fresh message starts.
func TestSenderEditInPlaceGrowth(t *testing.T) {
	h := newSenderHarness()

	h.cs.Add(text("Hello."))
	h.flush()
	if len(h.sends) != 1 || h.sends[0] != "Hello." {
		t.Fatalf("initial sends = %v", h.sends)
	}

	h.advance(3 * time.Second)
	h.cs.Add(text("More detail."))
	h.flush()
	if len(h.sends) != 1 {
		t.Fatalf("growth should edit, not send (sends: %v)", h.sends)
	}
	if len(h.edits) != 1 || h.edits[0].ID != 1 || h.edits[0].Text != "Hello.\n\nMore detail." {
		t.Fatalf("edits = %+v", h.edits)
	}

	// Past the ceiling the growth stops and a new message starts.
	big := strings.Repeat("x", growEditLimit)
	h.advance(3 * time.Second)
	h.cs.Add(text(big))
	h.flush()
	if len(h.sends) != 2 || len(h.edits) != 1 {
		t.Fatalf("over-limit growth must fall back to a new message (sends=%d edits=%d)", len(h.sends), len(h.edits))
	}
	// The oversized message is itself not growable; the next text is a
	// new message again.
	h.advance(3 * time.Second)
	h.cs.Add(text("after"))
	h.flush()
	if len(h.sends) != 3 || len(h.edits) != 1 {
		t.Fatalf("message over the ceiling must not keep growing (sends=%d edits=%d)", len(h.sends), len(h.edits))
	}
}

// TestSenderEditFailureFallsBackToNewMessage: a failed edit degrades to
// a fresh send carrying the same content.
func TestSenderEditFailureFallsBackToNewMessage(t *testing.T) {
	h := newSenderHarness()
	h.cs.Add(text("Hello."))
	h.flush()

	h.editErr = errors.New("message can't be edited")
	h.advance(3 * time.Second)
	h.cs.Add(text("More."))
	h.flush()
	if len(h.sends) != 2 || h.sends[1] != "More." {
		t.Fatalf("edit failure must fall back to sendMessage: %v", h.sends)
	}
	if len(h.edits) != 0 {
		t.Fatalf("edit recorded despite failure: %+v", h.edits)
	}
}

// TestSenderMixedBatchEndsGrowth: a tool line ends the growing
// assistant message — the batch goes out as a new message.
func TestSenderMixedBatchEndsGrowth(t *testing.T) {
	h := newSenderHarness()
	h.cs.Add(text("Hello."))
	h.flush()

	h.advance(3 * time.Second)
	h.cs.Add(tool("⚒ Bash: make test"))
	h.cs.Add(text("And done."))
	h.flush()
	if len(h.edits) != 0 {
		t.Fatalf("mixed batch must not edit: %+v", h.edits)
	}
	if len(h.sends) != 2 || h.sends[1] != "<i>⚒ Bash: make test</i>\n\nAnd done." {
		t.Fatalf("mixed batch message = %v", h.sends)
	}
}

// TestSenderFloodCapEngagesAndRecovers: >20 deliveries/minute sends one
// "output is heavy" notice and throttles to one send per 30s; when the
// window drains the normal cadence returns and the notice is not
// repeated.
func TestSenderFloodCapEngagesAndRecovers(t *testing.T) {
	h := newSenderHarness()

	// 19 paced sends stay under the cap.
	for i := 0; i < 19; i++ {
		h.cs.Add(tool(fmt.Sprintf("⚒ Bash: step %d", i)))
		h.flush()
		if i < 18 {
			h.advance(coalesceInterval)
		}
	}
	if len(h.sends) != 19 || noticeCount(h.sends) != 0 {
		t.Fatalf("cap engaged early: %d sends, %d notices", len(h.sends), noticeCount(h.sends))
	}

	// The 20th delivery crosses the cap: content + one notice.
	h.advance(coalesceInterval)
	h.cs.Add(tool("⚒ Bash: step 19"))
	h.flush()
	if len(h.sends) != 21 || noticeCount(h.sends) != 1 {
		t.Fatalf("cap should add exactly one notice: %d sends, %d notices", len(h.sends), noticeCount(h.sends))
	}
	engagedAt := h.now

	// Throttled: the usual 2.5s cadence goes quiet…
	h.cs.Add(tool("⚒ Bash: noisy"))
	h.advance(coalesceInterval)
	h.flush()
	if len(h.sends) != 21 {
		t.Fatalf("throttle must suppress the 2.5s cadence (sends=%d)", len(h.sends))
	}
	// …and one send goes out per 30s.
	h.now = engagedAt.Add(throttleInterval)
	h.flush()
	if len(h.sends) != 22 {
		t.Fatalf("throttled cadence should deliver after 30s (sends=%d)", len(h.sends))
	}

	// After a quiet spell the window drains and the cap disengages.
	h.advance(70 * time.Second)
	h.cs.Add(tool("⚒ Bash: calm again"))
	h.flush()
	h.advance(coalesceInterval)
	h.cs.Add(tool("⚒ Bash: normal cadence"))
	h.flush()
	if len(h.sends) != 24 {
		t.Fatalf("recovered sender should be back on the 2.5s cadence (sends=%d)", len(h.sends))
	}
	if noticeCount(h.sends) != 1 {
		t.Fatalf("notice must not repeat: %d", noticeCount(h.sends))
	}
}

func noticeCount(sends []string) int {
	n := 0
	for _, s := range sends {
		if s == floodNotice {
			n++
		}
	}
	return n
}

// TestSenderForceFlushBypassesPacing: the session-end flush delivers
// pending content even inside the coalesce interval.
func TestSenderForceFlushBypassesPacing(t *testing.T) {
	h := newSenderHarness()
	h.cs.Add(text("working…"))
	h.flush()
	h.advance(time.Second)
	h.cs.Add(Emission{Kind: EmissionMarker, Text: "✔ task 0007 merged"})
	h.cs.flush(context.Background(), true)
	if len(h.sends) != 2 || h.sends[1] != "<b>✔ task 0007 merged</b>" {
		t.Fatalf("force flush should deliver immediately: %v", h.sends)
	}
}

// TestBuildMessagesScreenPre: screen snapshots ride standalone <pre>
// blocks; oversized bodies chunk with tags balanced in every message.
func TestBuildMessagesScreenPre(t *testing.T) {
	msgs := buildMessages([]Emission{
		text("Status update"),
		{Kind: EmissionScreen, Text: "line1 <ok>\nline2"},
	})
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	if msgs[1] != "<pre>line1 &lt;ok&gt;\nline2</pre>" {
		t.Fatalf("pre block = %q", msgs[1])
	}

	// A giant screen chunks under the cap with balanced tags.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 60))
		b.WriteByte('\n')
	}
	big := buildMessages([]Emission{{Kind: EmissionScreen, Text: b.String()}})
	if len(big) < 2 {
		t.Fatalf("oversized screen should chunk, got %d message(s)", len(big))
	}
	for i, m := range big {
		if len(m) > maxMessageLen {
			t.Fatalf("chunk %d exceeds the message cap: %d bytes", i, len(m))
		}
		if !strings.HasPrefix(m, "<pre>") || !strings.HasSuffix(m, "</pre>") {
			t.Fatalf("chunk %d has unbalanced tags: %q…", i, m[:40])
		}
	}
}
