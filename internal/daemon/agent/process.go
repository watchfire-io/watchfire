package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"

	"github.com/watchfire-io/watchfire/internal/vtansi"
)

// Process-level constants.
const (
	scrollbackCapacity      = 1024
	readBufferSize          = 32 * 1024
	rawSubsChannelSize      = 256
	screenSubsChannelSize   = 64
	maxRawBufferSize        = 1 << 20
	issueAutoClearThreshold = 3
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
	ProjectID  string
	Cmd        *exec.Cmd
	Rows       int
	Cols       int
	SandboxTmp string // temp .sb file to clean up on stop
}

// Process manages a PTY + vt10x agent process.
type Process struct {
	mu          sync.RWMutex
	projectID   string
	cmd         *exec.Cmd
	ptyFile     *os.File
	vt          vt10x.Terminal
	rows, cols  int
	done        chan struct{}
	exitErr     error
	sandboxTmp  string
	cleanupOnce sync.Once

	subMu      sync.RWMutex
	rawSubs    map[string]chan []byte
	screenSubs map[string]chan *ScreenUpdate

	scrollMu   sync.RWMutex
	scrollback []string
	startedAt  time.Time

	rawBufMu      sync.RWMutex
	rawBuf        []byte // Accumulated raw PTY output for late-join catch-up
	rawTotalBytes int64  // Total bytes ever broadcast (monotonic). bufStart = rawTotalBytes - len(rawBuf)

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
		scrollback: make([]string, 0, scrollbackCapacity),
		startedAt:  time.Now().UTC(),
		issueSubs:  make(map[string]chan *AgentIssue),
	}

	go p.readLoop()

	return p, nil
}

// readLoop reads from the PTY and distributes data to subscribers.
func (p *Process) readLoop() {
	buf := make([]byte, readBufferSize)
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
			_, _ = p.vt.Write(data)

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

// renderScreenANSI emits the current vt10x grid as ANSI SGR-coloured
// text. Delegates to the shared vtansi package so the TUI can use the
// same renderer when driving its own emulator from the raw stream.
func (p *Process) renderScreenANSI(rows, cols int) string {
	return vtansi.RenderScreen(p.vt, rows, cols)
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

// Stop terminates the agent process. Platform-specific implementation
// in process_unix.go (SIGTERM → SIGKILL) and process_windows.go (Kill).

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

	ch := make(chan []byte, rawSubsChannelSize)
	p.rawSubs[id] = ch
	return ch
}

// SubscribeRawWithSnapshot atomically registers a subscriber and
// captures the current raw buffer at the same instant — closing the
// race in the older Subscribe-then-GetRawBuffer pattern where a
// broadcast landing between the two steps would deliver the same
// bytes twice (once via the buffer snapshot, once via the live
// channel). The TUI's vt10x replays bytes in order and double-
// delivery leaves it in a state that disagrees with the daemon's.
func (p *Process) SubscribeRawWithSnapshot(id string) (snapshot []byte, ch chan []byte) {
	return p.SubscribeRawFrom(id, 0)
}

// SubscribeRawFrom is the cursor-aware variant of SubscribeRawWithSnapshot.
// bytesReceived is the count of raw bytes the client claims to already
// hold locally; the returned snapshot is sliced so only bytes past that
// offset are sent. Used by the GUI chat terminal (#0100) so a reconnect
// no longer replays the full session from byte 0 and snaps the viewport
// to the start.
//
// Clamping rules: bytesReceived <= 0 returns the full buffer (initial
// subscribe). bytesReceived >= rawTotalBytes returns an empty snapshot
// (client is fully caught up). When bytesReceived falls before bufStart
// (= rawTotalBytes - len(rawBuf), i.e. the client missed bytes that
// have aged out of the 1 MiB rolling buffer), the full buffer is sent —
// the gap is genuinely lost data and the client gets whatever the daemon
// can still produce.
func (p *Process) SubscribeRawFrom(id string, bytesReceived int64) (snapshot []byte, ch chan []byte) {
	p.rawBufMu.Lock()
	p.subMu.Lock()
	defer p.subMu.Unlock()
	defer p.rawBufMu.Unlock()

	if len(p.rawBuf) > 0 && bytesReceived < p.rawTotalBytes {
		bufStart := p.rawTotalBytes - int64(len(p.rawBuf))
		skip := bytesReceived - bufStart
		if skip < 0 {
			skip = 0
		}
		if skip < int64(len(p.rawBuf)) {
			snapshot = make([]byte, int64(len(p.rawBuf))-skip)
			copy(snapshot, p.rawBuf[skip:])
		}
	}
	ch = make(chan []byte, rawSubsChannelSize)
	p.rawSubs[id] = ch
	return snapshot, ch
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

	ch := make(chan *ScreenUpdate, screenSubsChannelSize)
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

// broadcastRaw sends raw data to all raw subscribers and appends to
// the late-join buffer atomically. Holding rawBufMu across both the
// append and the channel sends (in the same order
// SubscribeRawWithSnapshot uses) eliminates the window where a new
// subscriber could see the same bytes twice — once in its initial
// buffer snapshot and once via the live channel. Channel sends are
// non-blocking so holding the lock won't stall on a slow consumer.
func (p *Process) broadcastRaw(data []byte) {
	p.rawBufMu.Lock()
	defer p.rawBufMu.Unlock()

	p.rawBuf = append(p.rawBuf, data...)
	p.rawTotalBytes += int64(len(data))
	if len(p.rawBuf) > maxRawBufferSize {
		p.rawBuf = p.rawBuf[len(p.rawBuf)-maxRawBufferSize:]
	}

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

// GetRawBuffer returns a copy of the accumulated raw PTY output.
func (p *Process) GetRawBuffer() []byte {
	p.rawBufMu.RLock()
	defer p.rawBufMu.RUnlock()
	if len(p.rawBuf) == 0 {
		return nil
	}
	buf := make([]byte, len(p.rawBuf))
	copy(buf, p.rawBuf)
	return buf
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
func (p *Process) GetScrollback(offset, limit int) (lines []string, total int) {
	p.scrollMu.RLock()
	defer p.scrollMu.RUnlock()

	total = len(p.scrollback)
	if offset >= total {
		return nil, total
	}

	end := offset + limit
	if end > total {
		end = total
	}

	lines = make([]string, end-offset)
	copy(lines, p.scrollback[offset:end])
	return lines, total
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
		if p.cleanLineCount >= issueAutoClearThreshold {
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
