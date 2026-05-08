package tui

import (
	"io"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// vtScrollbackCap is the maximum number of rendered lines kept in the
// scrollback ring. xterm.js defaults to 1000; we set 5000 since chat
// sessions accumulate output quickly.
const vtScrollbackCap = 5000

// vtBuffer is a TUI-side terminal emulator with scrollback. It wraps
// charmbracelet/x/vt — a near-xterm-compatible emulator with proper
// alt-screen, scroll regions, modern SGR/OSC/mouse handling, and a
// first-class scrollback API. Replaced an earlier vt10x-based
// implementation that mishandled claude code's output (typed input
// rendered at the top of the pane). The daemon stays on vt10x for
// snapshot rendering and issue detection — only the TUI consumes the
// raw stream and runs it through this emulator.
//
// Critical: x/vt's emulator answers terminal queries (DA1, DA2, DSR,
// focus, mouse, ReportMode, …) by writing the response into an
// internal io.Pipe accessible via Read(). Because pipe writes block
// until a reader drains them, Write() will deadlock the moment the
// agent emits a query if nothing reads the response side. We run a
// background drain goroutine that reads those responses and hands
// them off via a forwarder callback — wired by Terminal to send them
// back to the daemon's PTY so the agent gets its answer.
type vtBuffer struct {
	mu       sync.Mutex
	term     *vt.Emulator
	rows     int
	cols     int
	fwdMu    sync.RWMutex
	forward  func([]byte)
}

func newVTBuffer(rows, cols int) *vtBuffer {
	if rows < 1 {
		rows = 24
	}
	if cols < 1 {
		cols = 80
	}
	t := vt.NewEmulator(cols, rows)
	t.SetScrollbackSize(vtScrollbackCap)
	b := &vtBuffer{
		term: t,
		rows: rows,
		cols: cols,
	}
	go b.drainResponses(t)
	return b
}

// SetForwarder installs a callback that receives bytes the emulator
// writes back as terminal-query responses. The forwarder should send
// those bytes to the daemon's PTY so the agent unblocks (it's
// waiting for a reply to e.g. \x1b[c). The callback runs on a
// background goroutine — keep it cheap or hand off.
func (b *vtBuffer) SetForwarder(fn func([]byte)) {
	b.fwdMu.Lock()
	b.forward = fn
	b.fwdMu.Unlock()
}

// drainResponses reads from the emulator's response pipe forever and
// forwards bytes to whatever forwarder is installed. Without this,
// the very first agent query (DA1, DSR, …) deadlocks Write().
func (b *vtBuffer) drainResponses(t *vt.Emulator) {
	buf := make([]byte, 1024)
	for {
		n, err := t.Read(buf)
		if n > 0 {
			b.fwdMu.RLock()
			fn := b.forward
			b.fwdMu.RUnlock()
			if fn != nil {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				fn(cp)
			}
		}
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}
	}
}

// Resize reshapes the emulator. Scrollback is preserved by the
// emulator across resizes.
func (b *vtBuffer) Resize(rows, cols int) {
	if rows < 1 || cols < 1 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rows == b.rows && cols == b.cols {
		return
	}
	b.rows = rows
	b.cols = cols
	b.term.Resize(cols, rows)
}

// Write feeds raw PTY bytes into the emulator. The emulator handles
// scroll-off into its own scrollback buffer automatically — we don't
// need any byte-level chunking or snapshot diffing.
func (b *vtBuffer) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.term.Write(data)
}

// Render returns scrollback + current screen joined by "\n", ready to
// drop into a bubbles viewport. Scrollback is empty while the agent
// is in alt-screen mode (claude code's TUI lives there) — the
// emulator stops capturing scrollback during alt-screen so history
// doesn't get polluted.
func (b *vtBuffer) Render() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	screen := b.term.Render()
	sb := b.term.Scrollback()
	if sb == nil || sb.Len() == 0 {
		return screen
	}

	var out strings.Builder
	out.WriteString(uv.Lines(sb.Lines()).Render())
	out.WriteByte('\n')
	out.WriteString(screen)
	return out.String()
}

// ScrollbackLen returns the current scrollback line count. Used by
// tests and by the auto-bottom logic.
func (b *vtBuffer) ScrollbackLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sb := b.term.Scrollback(); sb != nil {
		return sb.Len()
	}
	return 0
}

// Clear drops scrollback and resets the underlying emulator. Called
// when the agent stops so the next session starts fresh. The old
// emulator is closed so its drain goroutine exits on EOF and a fresh
// goroutine takes over the new one.
func (b *vtBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.term != nil {
		_ = b.term.Close()
	}
	t := vt.NewEmulator(b.cols, b.rows)
	t.SetScrollbackSize(vtScrollbackCap)
	b.term = t
	go b.drainResponses(t)
}
