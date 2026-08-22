package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// newScrollbackProcess builds the minimum Process needed to exercise the
// scrollback buffer — no PTY, no command.
func newScrollbackProcess() *Process {
	return &Process{scrollback: make([]string, 0, scrollbackCapacity)}
}

// TestAppendScrollbackSplitRune: a multi-byte rune split across two PTY
// reads must survive intact and valid. Before the fix each half was
// converted with string(data) independently, so the buffer held invalid
// UTF-8 — and because scrollback is served over gRPC (proto3 strings
// must be valid UTF-8) the whole response failed to marshal with "string
// field contains invalid UTF-8", breaking GetScrollback and
// get_agent_screen for the rest of the session.
//
// Only rune integrity is asserted, not line reassembly: a line split
// across reads has always produced two scrollback entries, which is
// pre-existing behaviour this fix deliberately leaves alone.
func TestAppendScrollbackSplitRune(t *testing.T) {
	// Claude Code's box-drawing frame — three bytes, routinely split.
	const line = "▔▔▔ Login ▔▔▔"
	raw := []byte(line + "\n")

	for split := 1; split < len(raw); split++ {
		p := newScrollbackProcess()
		p.appendScrollback(raw[:split])
		p.appendScrollback(raw[split:])

		got := p.GetFullScrollback()
		for _, l := range got {
			if !utf8.ValidString(l) {
				t.Fatalf("split at %d: scrollback holds invalid UTF-8: %q", split, l)
			}
		}
		if joined := strings.Join(got, ""); joined != line {
			t.Fatalf("split at %d: reassembled %q, want %q", split, joined, line)
		}
	}
}

// TestAppendScrollbackAlwaysValidUTF8: whatever the PTY emits — including
// genuinely malformed bytes that no amount of re-chunking fixes — the
// buffer never holds bytes the wire cannot carry.
func TestAppendScrollbackAlwaysValidUTF8(t *testing.T) {
	cases := map[string][]byte{
		"lone continuation": {0x80, 0x81, '\n'},
		"truncated 3-byte":  {0xE2, 0x96, '\n'},
		"invalid start":     {0xFF, 0xFE, '\n'},
		"binary noise":      {0x00, 0xC0, 0xAF, 0x7F, '\n'},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			p := newScrollbackProcess()
			p.appendScrollback(raw)
			// Flush any held-back partial with a terminator.
			p.appendScrollback([]byte("end\n"))
			for _, l := range p.GetFullScrollback() {
				if !utf8.ValidString(l) {
					t.Fatalf("scrollback line is not valid UTF-8: %q", l)
				}
			}
		})
	}
}

// TestAppendScrollbackChunkedStream: however the stream is cut, every
// scrollback line is valid UTF-8 and no character is lost or corrupted.
func TestAppendScrollbackChunkedStream(t *testing.T) {
	stream := "❯ hello\n⏺ Please run /login · API Error: 401\n✻ Crunched for 2s\n"
	want := strings.ReplaceAll(stream, "\n", "")

	raw := []byte(stream)
	for chunk := 1; chunk <= len(raw); chunk++ {
		p := newScrollbackProcess()
		for i := 0; i < len(raw); i += chunk {
			end := i + chunk
			if end > len(raw) {
				end = len(raw)
			}
			p.appendScrollback(raw[i:end])
		}
		got := p.GetFullScrollback()
		for _, l := range got {
			if !utf8.ValidString(l) {
				t.Fatalf("chunk size %d: invalid UTF-8 in scrollback: %q", chunk, l)
			}
		}
		if joined := strings.Join(got, ""); joined != want {
			t.Fatalf("chunk size %d: reassembled %q, want %q", chunk, joined, want)
		}
	}
}

// TestIncompleteRuneSuffix pins the hold-back decision itself.
func TestIncompleteRuneSuffix(t *testing.T) {
	full := []byte("▔") // E2 96 94
	cases := []struct {
		name string
		in   []byte
		want int
	}{
		{"empty", nil, 0},
		{"ascii", []byte("abc"), 0},
		{"complete rune", full, 0},
		{"one byte short", full[:2], 2},
		{"two bytes short", full[:1], 1},
		{"ascii then partial", append([]byte("hi"), full[:1]...), 1},
		{"lone continuation", []byte{0x80}, 0},
	}
	for _, c := range cases {
		if got := incompleteRuneSuffix(c.in); got != c.want {
			t.Errorf("%s: incompleteRuneSuffix(%v) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
}
