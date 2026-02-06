package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// ScreenUpdate represents a parsed terminal screen state.
type ScreenUpdate struct {
	ProjectID string
	Lines     []string
	CursorRow int
	CursorCol int
	Rows      int
	Cols      int
}

// ProcessOptions contains options for creating a new agent process.
type ProcessOptions struct {
	ProjectID string
	Cmd       *exec.Cmd
	Rows      int
	Cols      int
	SandboxTmp string // temp .sb file to clean up on stop
}

// Process manages a PTY + vt10x agent process.
type Process struct {
	mu         sync.RWMutex
	projectID  string
	cmd        *exec.Cmd
	ptyFile    *os.File
	vt         vt10x.Terminal
	rows, cols int
	done       chan struct{}
	exitErr    error
	sandboxTmp string

	subMu      sync.RWMutex
	rawSubs    map[string]chan []byte
	screenSubs map[string]chan *ScreenUpdate

	scrollMu   sync.RWMutex
	scrollback []string
}

// NewProcess creates and starts a new agent process with PTY and vt10x terminal emulation.
func NewProcess(opts ProcessOptions) (*Process, error) {
	rows := opts.Rows
	cols := opts.Cols
	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}

	// Create vt10x terminal
	vt := vt10x.New(vt10x.WithSize(cols, rows))

	// Start command in PTY
	winSize := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}
	ptmx, err := pty.StartWithSize(opts.Cmd, winSize)
	if err != nil {
		return nil, fmt.Errorf("failed to start PTY: %w", err)
	}

	p := &Process{
		projectID:  opts.ProjectID,
		cmd:        opts.Cmd,
		ptyFile:    ptmx,
		vt:         vt,
		rows:       rows,
		cols:       cols,
		done:       make(chan struct{}),
		sandboxTmp: opts.SandboxTmp,
		rawSubs:    make(map[string]chan []byte),
		screenSubs: make(map[string]chan *ScreenUpdate),
		scrollback: make([]string, 0, 1024),
	}

	go p.readLoop()

	return p, nil
}

// readLoop reads from the PTY and distributes data to subscribers.
func (p *Process) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.ptyFile.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			// Broadcast raw data to CLI subscribers
			p.broadcastRaw(data)

			// Feed data to vt10x terminal emulator
			p.vt.Write(data)

			// Snapshot screen state and broadcast to GUI subscribers
			screen := p.snapshotScreen()
			p.broadcastScreen(screen)

			// Append to scrollback buffer
			p.appendScrollback(data)
		}
		if err != nil {
			break
		}
	}

	// Wait for process to finish
	p.exitErr = p.cmd.Wait()
	close(p.done)
}

// snapshotScreen reads the current vt10x state into a ScreenUpdate.
func (p *Process) snapshotScreen() *ScreenUpdate {
	p.mu.RLock()
	rows := p.rows
	cols := p.cols
	p.mu.RUnlock()

	lines := make([]string, rows)
	for row := 0; row < rows; row++ {
		var sb strings.Builder
		for col := 0; col < cols; col++ {
			g := p.vt.Cell(col, row)
			if g.Char == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(g.Char)
			}
		}
		lines[row] = sb.String()
	}

	cur := p.vt.Cursor()

	return &ScreenUpdate{
		ProjectID: p.projectID,
		Lines:     lines,
		CursorRow: cur.Y,
		CursorCol: cur.X,
		Rows:      rows,
		Cols:      cols,
	}
}

// SendInput writes data to the PTY (user input).
func (p *Process) SendInput(data []byte) error {
	_, err := p.ptyFile.Write(data)
	return err
}

// Resize changes the PTY and vt10x terminal size.
func (p *Process) Resize(rows, cols int) error {
	p.mu.Lock()
	p.rows = rows
	p.cols = cols
	p.mu.Unlock()

	if err := pty.Setsize(p.ptyFile, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}); err != nil {
		return fmt.Errorf("failed to resize PTY: %w", err)
	}

	p.vt.Resize(cols, rows)
	return nil
}

// Stop terminates the agent process. Sends SIGTERM, waits 5 seconds, then SIGKILL.
func (p *Process) Stop() {
	if p.cmd.Process == nil {
		return
	}

	// Send SIGTERM
	_ = p.cmd.Process.Signal(syscall.SIGTERM)

	// Wait up to 5 seconds for graceful exit
	select {
	case <-p.done:
		p.cleanup()
		return
	case <-time.After(5 * time.Second):
	}

	// Force kill
	_ = p.cmd.Process.Kill()
	<-p.done
	p.cleanup()
}

// cleanup removes temp files after process exit.
func (p *Process) cleanup() {
	if p.ptyFile != nil {
		p.ptyFile.Close()
	}
	if p.sandboxTmp != "" {
		os.Remove(p.sandboxTmp)
	}
}

// Done returns a channel that is closed when the process exits.
func (p *Process) Done() <-chan struct{} {
	return p.done
}

// ExitErr returns the process exit error (nil if exited cleanly).
func (p *Process) ExitErr() error {
	return p.exitErr
}

// SubscribeRaw creates a raw output subscription for the given subscriber ID.
func (p *Process) SubscribeRaw(id string) chan []byte {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	ch := make(chan []byte, 256)
	p.rawSubs[id] = ch
	return ch
}

// UnsubscribeRaw removes a raw output subscription.
func (p *Process) UnsubscribeRaw(id string) {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	if ch, ok := p.rawSubs[id]; ok {
		close(ch)
		delete(p.rawSubs, id)
	}
}

// SubscribeScreen creates a screen update subscription for the given subscriber ID.
func (p *Process) SubscribeScreen(id string) chan *ScreenUpdate {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	ch := make(chan *ScreenUpdate, 64)
	p.screenSubs[id] = ch
	return ch
}

// UnsubscribeScreen removes a screen update subscription.
func (p *Process) UnsubscribeScreen(id string) {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	if ch, ok := p.screenSubs[id]; ok {
		close(ch)
		delete(p.screenSubs, id)
	}
}

// broadcastRaw sends raw data to all raw subscribers. Non-blocking: drops if channel full.
func (p *Process) broadcastRaw(data []byte) {
	p.subMu.RLock()
	defer p.subMu.RUnlock()

	for _, ch := range p.rawSubs {
		select {
		case ch <- data:
		default:
			// Drop if subscriber can't keep up
		}
	}
}

// broadcastScreen sends a screen update to all screen subscribers. Non-blocking.
func (p *Process) broadcastScreen(update *ScreenUpdate) {
	p.subMu.RLock()
	defer p.subMu.RUnlock()

	for _, ch := range p.screenSubs {
		select {
		case ch <- update:
		default:
			// Drop if subscriber can't keep up
		}
	}
}

// appendScrollback adds raw data lines to the scrollback buffer.
func (p *Process) appendScrollback(data []byte) {
	p.scrollMu.Lock()
	defer p.scrollMu.Unlock()

	// Split data into lines and append
	text := string(data)
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line != "" {
			p.scrollback = append(p.scrollback, line)
		}
	}
}

// GetScrollback returns a slice of the scrollback buffer.
func (p *Process) GetScrollback(offset, limit int) ([]string, int) {
	p.scrollMu.RLock()
	defer p.scrollMu.RUnlock()

	total := len(p.scrollback)
	if offset >= total {
		return nil, total
	}

	end := offset + limit
	if end > total {
		end = total
	}

	result := make([]string, end-offset)
	copy(result, p.scrollback[offset:end])
	return result, total
}

// TerminalSize returns the current terminal dimensions.
func (p *Process) TerminalSize() (rows, cols int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.rows, p.cols
}

// IsRunning returns true if the process is still running.
func (p *Process) IsRunning() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// logf logs a message with the process context.
func (p *Process) logf(format string, args ...interface{}) {
	prefix := fmt.Sprintf("[agent:%s] ", p.projectID)
	log.Printf(prefix+format, args...)
}
