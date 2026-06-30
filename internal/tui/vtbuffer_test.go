package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// stripSGR removes ANSI escape sequences for test assertions —
// scrollback lines come back wrapped in SGR codes which would defeat
// substring matching.
func stripSGR(s string) string { return ansi.Strip(s) }

// TestVTBufferDeviceAttributesDoNotHang exercises the fix for the
// agent-startup hang: x/vt's emulator answers DA1 (\x1b[c) by writing
// the response to its internal pipe, which blocks the synchronous
// Write() until something reads. The drain goroutine forwarder must
// pick up those bytes so Write() returns. If this test ever times
// out, the drain wiring has regressed.
func TestVTBufferDeviceAttributesDoNotHang(t *testing.T) {
	b := newVTBuffer(24, 80)

	got := make(chan []byte, 4)
	b.SetForwarder(func(data []byte) {
		select {
		case got <- data:
		default:
		}
	})

	done := make(chan struct{})
	go func() {
		b.Write([]byte("\x1b[c"))
		close(done)
	}()

	select {
	case <-done:
		// Good — Write returned.
	case <-time.After(2 * time.Second):
		t.Fatal("Write() blocked for 2s on a DA1 query — drain goroutine isn't forwarding the response")
	}

	select {
	case resp := <-got:
		if len(resp) == 0 {
			t.Errorf("forwarder got an empty response")
		}
	case <-time.After(2 * time.Second):
		t.Errorf("forwarder never received the DA1 response")
	}
}

// TestVTBufferScrollbackOnNewlineOverflow drives a 4-row buffer past
// the bottom and verifies the lines that disappeared from the visible
// grid land in scrollback. vt10x scrolls on every `\n` issued at the
// bottom row including the trailing one — so 6 newline-terminated
// lines into 4 rows leave 3 lines in scrollback (the last `\n` parks
// the cursor on an empty row at the bottom).
func TestVTBufferScrollbackOnNewlineOverflow(t *testing.T) {
	b := newVTBuffer(4, 20)
	b.Write([]byte("line1\r\nline2\r\nline3\r\nline4\r\nline5\r\nline6\r\n"))

	if got := b.ScrollbackLen(); got != 3 {
		t.Fatalf("scrollback len = %d, want 3 (lines 1-3 scrolled off)", got)
	}
	rendered := stripSGR(b.Render())
	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected %q in scrollback, got:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "line6") {
		t.Errorf("expected line6 still on screen, got:\n%s", rendered)
	}
}

// TestVTBufferAltScreenSuppressesScrollback verifies that content
// written while alt-screen is active does NOT enter scrollback when
// alt-screen is exited (claude code's TUI lives there).
func TestVTBufferAltScreenSuppressesScrollback(t *testing.T) {
	b := newVTBuffer(4, 20)

	// Fill primary; one line should scroll off.
	b.Write([]byte("primary1\r\nprimary2\r\nprimary3\r\nprimary4\r\nprimary5\r\n"))
	primaryScrollback := b.ScrollbackLen()
	if primaryScrollback == 0 {
		t.Fatalf("expected primary-screen scrollback before alt swap, got 0")
	}

	// Enter alt screen, write a stack of lines, exit alt screen.
	// DECSET 1049 = enter alt + save cursor; DECRST 1049 = exit + restore.
	b.Write([]byte("\x1b[?1049h"))
	b.Write([]byte("alt1\r\nalt2\r\nalt3\r\nalt4\r\nalt5\r\nalt6\r\n"))
	b.Write([]byte("\x1b[?1049l"))

	if got := b.ScrollbackLen(); got != primaryScrollback {
		t.Errorf("alt-screen activity changed scrollback: was %d, now %d", primaryScrollback, got)
	}
	rendered := stripSGR(b.Render())
	if strings.Contains(rendered, "alt") {
		t.Errorf("alt-screen content should not appear after exit, got:\n%s", rendered)
	}
}

// TestVTBufferLargeBatchPreservesScrollback exercises the chunking
// path — a single big Write covering many newlines should still
// capture every intermediate line that scrolled off, not just the
// initial-vs-final delta.
func TestVTBufferLargeBatchPreservesScrollback(t *testing.T) {
	b := newVTBuffer(4, 20)
	var sb strings.Builder
	const lines = 50
	for i := 1; i <= lines; i++ {
		sb.WriteString("L")
		sb.WriteByte(byte('0' + i/10))
		sb.WriteByte(byte('0' + i%10))
		sb.WriteString("\r\n")
	}
	b.Write([]byte(sb.String()))

	got := b.ScrollbackLen()
	// 50 lines in a 4-row grid → ~46 should have scrolled off.
	if got < lines-5 || got > lines {
		t.Errorf("scrollback len = %d, want roughly %d", got, lines-4)
	}
	rendered := stripSGR(b.Render())
	if !strings.Contains(rendered, "L01") {
		t.Errorf("expected earliest line L01 in scrollback, rendered:\n%s", rendered)
	}
}
