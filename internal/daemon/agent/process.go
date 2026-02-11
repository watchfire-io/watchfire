package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// ScreenUpdate represents a parsed terminal screen state.
type ScreenUpdate struct {
	ProjectID   string
	Lines       []string
	CursorRow   int
	CursorCol   int
	Rows        int
	Cols        int
	AnsiContent string
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
	sandboxTmp  string
	cleanupOnce sync.Once

	subMu      sync.RWMutex
	rawSubs    map[string]chan []byte
	screenSubs map[string]chan *ScreenUpdate

	scrollMu   sync.RWMutex
	scrollback []string
	startedAt  time.Time

	// Issue detection
	issueMu        sync.RWMutex
	issue          *AgentIssue
	issueSubs      map[string]chan *AgentIssue
	lineBuffer     strings.Builder // Accumulate partial lines for detection
	cleanLineCount int             // Non-issue lines seen since last issue
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
		startedAt:  time.Now().UTC(),
		issueSubs:  make(map[string]chan *AgentIssue),
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

			// Detect issues in output (auth errors, rate limits)
			p.detectIssues(data)

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
		ProjectID:   p.projectID,
		Lines:       lines,
		CursorRow:   cur.Y,
		CursorCol:   cur.X,
		Rows:        rows,
		Cols:        cols,
		AnsiContent: p.renderScreenANSI(rows, cols),
	}
}

// SnapshotScreen returns the current screen state (exported for initial snapshot on subscribe).
func (p *Process) SnapshotScreen() *ScreenUpdate {
	return p.snapshotScreen()
}

// vt10x attr constants (unexported in the package).
const (
	vtAttrReverse   = 1
	vtAttrUnderline = 2
	vtAttrBold      = 4
	vtAttrItalic    = 16
)

// renderScreenANSI iterates over the vt10x cell grid and emits ANSI SGR-colored text.
// Trailing default-color spaces are stripped to avoid overflowing the viewport.
func (p *Process) renderScreenANSI(rows, cols int) string {
	var sb strings.Builder
	sb.Grow(cols * rows * 3)

	for row := 0; row < rows; row++ {
		if row > 0 {
			sb.WriteByte('\n')
		}

		// Find last significant column (non-space or non-default attributes)
		lastCol := cols - 1
		for lastCol >= 0 {
			g := p.vt.Cell(lastCol, row)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			if ch != ' ' || g.FG != vt10x.DefaultFG || g.BG != vt10x.DefaultBG || g.Mode != 0 {
				break
			}
			lastCol--
		}

		var lastFG vt10x.Color = vt10x.DefaultFG
		var lastBG vt10x.Color = vt10x.DefaultBG
		var lastBold, lastItalic, lastUnderline, lastReverse bool
		inSGR := false

		for col := 0; col <= lastCol; col++ {
			g := p.vt.Cell(col, row)

			ch := g.Char
			if ch == 0 {
				ch = ' '
			}

			fg := g.FG
			bg := g.BG
			bold := g.Mode&vtAttrBold != 0
			italic := g.Mode&vtAttrItalic != 0
			underline := g.Mode&vtAttrUnderline != 0
			reverse := g.Mode&vtAttrReverse != 0

			if fg != lastFG || bg != lastBG || bold != lastBold || italic != lastItalic || underline != lastUnderline || reverse != lastReverse {
				sb.WriteString("\033[0")

				if bold {
					sb.WriteString(";1")
				}
				if italic {
					sb.WriteString(";3")
				}
				if underline {
					sb.WriteString(";4")
				}
				if reverse {
					sb.WriteString(";7")
				}

				if fg != vt10x.DefaultFG {
					writeSGRColor(&sb, fg, true)
				}
				if bg != vt10x.DefaultBG {
					writeSGRColor(&sb, bg, false)
				}

				sb.WriteByte('m')
				inSGR = true

				lastFG = fg
				lastBG = bg
				lastBold = bold
				lastItalic = italic
				lastUnderline = underline
				lastReverse = reverse
			}

			sb.WriteRune(ch)
		}

		if inSGR {
			sb.WriteString("\033[0m")
		}
	}

	return sb.String()
}

// writeSGRColor writes an SGR color parameter for the given vt10x color.
func writeSGRColor(sb *strings.Builder, c vt10x.Color, isFG bool) {
	idx := uint32(c)

	// Sentinel values (DefaultFG, DefaultBG, DefaultCursor) have bit 24 set — skip
	if idx >= 1<<24 {
		return
	}

	if idx < 8 {
		// Standard ANSI 0-7
		if isFG {
			fmt.Fprintf(sb, ";%d", 30+idx)
		} else {
			fmt.Fprintf(sb, ";%d", 40+idx)
		}
	} else if idx < 16 {
		// Bright ANSI 8-15
		if isFG {
			fmt.Fprintf(sb, ";%d", 90+idx-8)
		} else {
			fmt.Fprintf(sb, ";%d", 100+idx-8)
		}
	} else if idx < 256 {
		// 256-color palette
		if isFG {
			fmt.Fprintf(sb, ";38;5;%d", idx)
		} else {
			fmt.Fprintf(sb, ";48;5;%d", idx)
		}
	} else {
		// 24-bit RGB: value is r<<16 | g<<8 | b
		r := (idx >> 16) & 0xFF
		g := (idx >> 8) & 0xFF
		b := idx & 0xFF
		if isFG {
			fmt.Fprintf(sb, ";38;2;%d;%d;%d", r, g, b)
		} else {
			fmt.Fprintf(sb, ";48;2;%d;%d;%d", r, g, b)
		}
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
		p.Cleanup()
		return
	case <-time.After(5 * time.Second):
	}

	// Force kill
	_ = p.cmd.Process.Kill()
	<-p.done
	p.Cleanup()
}

// Cleanup releases process resources (PTY file, sandbox temp file).
// Safe to call multiple times — only runs once.
func (p *Process) Cleanup() {
	p.cleanupOnce.Do(func() {
		if p.ptyFile != nil {
			_ = p.ptyFile.Close()
		}
		if p.sandboxTmp != "" {
			_ = os.Remove(p.sandboxTmp)
		}
	})
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

// GetFullScrollback returns all scrollback lines.
func (p *Process) GetFullScrollback() []string {
	p.scrollMu.RLock()
	defer p.scrollMu.RUnlock()

	result := make([]string, len(p.scrollback))
	copy(result, p.scrollback)
	return result
}

// StartedAt returns when the process was created.
func (p *Process) StartedAt() time.Time {
	return p.startedAt
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

// detectIssues scans PTY output for auth errors and rate limits.
func (p *Process) detectIssues(data []byte) {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()

	// Append data to line buffer
	p.lineBuffer.Write(data)

	// Process complete lines
	content := p.lineBuffer.String()
	lines := strings.Split(content, "\n")

	// Keep the last incomplete line in the buffer
	if len(lines) > 0 {
		p.lineBuffer.Reset()
		p.lineBuffer.WriteString(lines[len(lines)-1])
		lines = lines[:len(lines)-1]
	}

	// Check each complete line for issues
	hasNonEmpty := false
	for _, line := range lines {
		// Strip ANSI escape codes for cleaner matching
		cleanLine := stripANSI(line)
		if cleanLine == "" {
			continue
		}
		hasNonEmpty = true

		if issue := DetectIssue(cleanLine); issue != nil {
			p.cleanLineCount = 0
			p.setIssueLocked(issue)
			return // Only report first issue found
		}
	}

	// Auto-clear: if we have an active issue and see enough normal output,
	// the agent has resumed working — clear the issue banner.
	if hasNonEmpty && p.issue != nil {
		p.cleanLineCount++
		if p.cleanLineCount >= 3 {
			p.logf("Issue auto-cleared after %d clean line batches", p.cleanLineCount)
			p.cleanLineCount = 0
			p.issue = nil
			for _, ch := range p.issueSubs {
				select {
				case ch <- nil:
				default:
				}
			}
		}
	}
}

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	// Simple regex to strip common ANSI escape sequences
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07`)
	return strings.TrimSpace(ansiPattern.ReplaceAllString(s, ""))
}

// setIssueLocked sets the current issue and broadcasts to subscribers.
// Must be called while holding issueMu.
func (p *Process) setIssueLocked(issue *AgentIssue) {
	p.issue = issue
	p.logf("Issue detected: type=%s message=%q", issue.Type, issue.Message)

	// Broadcast to subscribers (non-blocking)
	for _, ch := range p.issueSubs {
		select {
		case ch <- issue:
		default:
			// Drop if subscriber can't keep up
		}
	}
}

// SetIssue sets the current issue and broadcasts to subscribers.
func (p *Process) SetIssue(issue *AgentIssue) {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()
	p.setIssueLocked(issue)
}

// ClearIssue clears the current issue (e.g., after user runs /login or rate limit resets).
func (p *Process) ClearIssue() {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()

	if p.issue != nil {
		p.logf("Issue cleared: type=%s", p.issue.Type)
		p.issue = nil

		// Broadcast nil to indicate issue cleared
		for _, ch := range p.issueSubs {
			select {
			case ch <- nil:
			default:
			}
		}
	}
}

// GetIssue returns the current issue, or nil if none.
func (p *Process) GetIssue() *AgentIssue {
	p.issueMu.RLock()
	defer p.issueMu.RUnlock()
	return p.issue
}

// SubscribeIssues creates an issue subscription for the given subscriber ID.
func (p *Process) SubscribeIssues(id string) chan *AgentIssue {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()

	ch := make(chan *AgentIssue, 16)
	p.issueSubs[id] = ch
	return ch
}

// UnsubscribeIssues removes an issue subscription.
func (p *Process) UnsubscribeIssues(id string) {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()

	if ch, ok := p.issueSubs[id]; ok {
		close(ch)
		delete(p.issueSubs, id)
	}
}
