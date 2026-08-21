// Live conversation relay ("watch mode") — v10.0 Torch, task 0141.
//
// A chat that has toggled `/watch on` gets the agent's *conversation*
// relayed to Telegram while a session runs for the chat's active
// project. Two source tiers, cheapest first:
//
//   - Tier 1 — transcript tail. Backends that append a JSONL transcript
//     during the session (Claude Code first-class) are tailed via a
//     TailableTranscript; assistant text is relayed verbatim and tool
//     uses become one-liners ("⚒ Edit internal/tui/model.go").
//   - Tier 2 — screen deltas. Backends without a tailable transcript
//     (or a tailer that errors) fall back to debounced plain-text
//     screen snapshots, sent only when the normalized content changed.
//
// Telegram-side discipline (both tiers): 4096-char chunking, per-chat
// coalescing (≤1 send per ~2.5s), edit-in-place growth of the current
// assistant message, and a hard flood cap that throttles to one send
// per 30s when the agent floods.
//
// The relay only ever READS: sessions are observed through the
// SessionSource seam, which exposes snapshots and outcomes but no PTY
// write and no Resize (guarded by watch_guard_test.go).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// Watch-mode tuning. Bridge fields default to these; tests shrink them.
const (
	// tailPollInterval is how often the transcript tailer polls for
	// appended bytes.
	tailPollInterval = time.Second
	// screenDeltaInterval is the tier-2 snapshot debounce (spec: ≥5s).
	screenDeltaInterval = 5 * time.Second
	// sessionPollInterval is how often the watch loop reconciles
	// watching chats against live sessions.
	sessionPollInterval = 2 * time.Second
	// senderFlushTick is how often a relay asks its sender to flush;
	// the sender itself enforces the coalesce interval.
	senderFlushTick = 500 * time.Millisecond
	// coalesceInterval is the minimum gap between sends to one chat.
	coalesceInterval = 2500 * time.Millisecond
	// throttleInterval replaces coalesceInterval while flood-capped.
	throttleInterval = 30 * time.Second
	// floodWindow / floodMaxSends: more than floodMaxSends deliveries
	// inside floodWindow engages the throttle.
	floodWindow   = time.Minute
	floodMaxSends = 20
	// floodRecoverSends: the throttle disengages once the delivery
	// count inside the window has drained to this (hysteresis).
	floodRecoverSends = 10
	// growEditLimit is the rendered-size ceiling for editMessageText
	// growth; beyond it a fresh message starts.
	growEditLimit = 3500
	// outcomeAttempts × outcomeRetryInterval bounds how long the relay
	// waits for the task YAML to reach its final state after the
	// session ends (the merge runs asynchronously after process exit).
	outcomeAttempts      = 10
	outcomeRetryInterval = time.Second
	// chatStartPollInterval / chatStartWaitTimeout bound how long the
	// plain-text conversation path waits for an auto-started chat agent
	// to paint its first screen before giving up on delivery.
	chatStartPollInterval = 500 * time.Millisecond
	chatStartWaitTimeout  = 30 * time.Second
	// chatStartSettleDelay is the grace between first screen paint and
	// the injection, so the CLI's input loop is accepting keys.
	chatStartSettleDelay = 1500 * time.Millisecond
	// sayEnterDelayDefault is the beat between injectSay's text write
	// and its Enter write — long enough that the CLI's paste detection
	// treats them as separate keystrokes.
	sayEnterDelayDefault = 250 * time.Millisecond
	// typingInterval is how often the relay re-sends the "typing…" chat
	// action (the Bot API clears it after ~5s); typingActivityWindow is
	// how long after the last emission the indicator keeps showing —
	// past it the agent is considered idle at its prompt.
	typingInterval       = 4 * time.Second
	typingActivityWindow = 15 * time.Second
)

// floodNotice is sent once when the flood cap engages.
const floodNotice = "⚡ output is heavy — open the GUI for the full session"

// EmissionKind classifies one relay emission.
type EmissionKind int

// Emission kinds.
const (
	// EmissionAssistantText is an assistant text block, relayed verbatim.
	EmissionAssistantText EmissionKind = iota
	// EmissionToolUse is a one-line tool-use summary ("⚒ Bash: make test").
	EmissionToolUse
	// EmissionScreen is a tier-2 plain-text screen snapshot (rendered
	// as a monospace <pre> block).
	EmissionScreen
	// EmissionMarker is a session lifecycle marker ("▶ task 0141 — …").
	EmissionMarker
	// EmissionTurnBreak carries no text: a real user turn appeared in
	// the transcript, so the sender must end the current edit-grown
	// message — the answer to a new question has to arrive as a NEW
	// message (after the user's own), not spliced onto the previous
	// reply.
	EmissionTurnBreak
)

// Emission is one unit of relayed content, produced by a source tier
// and consumed by the per-chat sender.
type Emission struct {
	Kind EmissionKind
	Text string
}

// WatchedSession is the read-only view of one running agent session
// that watch mode observes. Snapshot (may be nil) returns the current
// plain-text screen lines; there is deliberately no input or resize
// surface here.
type WatchedSession struct {
	ProjectID   string
	ProjectPath string
	Mode        string
	// Phase is the wildfire phase ("execute" / "refine" / "generate"),
	// empty outside wildfire mode. Drives the high-level milestone
	// markers wildfire sessions relay instead of a raw stream.
	Phase       string
	TaskNumber  int
	TaskTitle   string
	BackendName string
	WorkDir     string
	SessionName string
	StartedAt   time.Time
	Done        <-chan struct{}
	Snapshot    func() []string
	// IssueType returns the session's current detected issue type
	// ("auth_required", "rate_limited", …) or "" — read-only, like
	// Snapshot; drives the auto-relayed /login hint.
	IssueType func() string
}

// TaskSummary is the number + title of one task, for milestone markers.
type TaskSummary struct {
	Number int
	Title  string
}

// TaskOutcome is the final state of a task after its session ended,
// resolved by the SessionSource from the task YAML.
type TaskOutcome struct {
	Done               bool
	Success            bool
	FailureReason      string
	Merged             bool
	MergeFailureReason string
}

// SessionSource lets the bridge observe agent sessions. The production
// implementation lives in the server package over agent.Manager; it is
// the seam that keeps the telegram package free of any PTY handle.
type SessionSource interface {
	// ActiveSession returns the live session for projectID, if any.
	ActiveSession(projectID string) (*WatchedSession, bool)
	// ActiveProjects lists the projects that currently have a live
	// session — the candidate pool a watching chat with no /use
	// selection auto-attaches to.
	ActiveProjects() []string
	// TaskOutcome reports the task's state after its session ended.
	TaskOutcome(projectPath string, taskNumber int) (TaskOutcome, error)
	// TasksCreatedSince lists tasks created at/after since — how the
	// wildfire milestone feed reports "generated task NNNN — title"
	// after a generate/refine session ends.
	TasksCreatedSince(projectPath string, since time.Time) []TaskSummary
}

// ---------------------------------------------------------------------------
// Tier 1 — transcript tail

// TailableTranscript locates and parses a backend's live JSONL
// transcript. Implementations are per-backend; Claude Code is
// first-class, everything else falls through to tier 2.
type TailableTranscript interface {
	// Locate returns the transcript path. An error means "not found
	// (yet)" — the tailer keeps retrying while the session runs,
	// because the agent creates the file only after startup.
	Locate() (string, error)
	// ParseLine converts one complete JSONL line into zero or more
	// emissions, in transcript order.
	ParseLine(line []byte) []Emission
}

// TailerFor returns the tail implementation for backendName, or false
// when the backend has no tailable transcript (→ tier 2). An empty
// backend name means Claude Code, mirroring the agent manager default.
func TailerFor(backendName, workDir string, started time.Time, sessionName string) (TailableTranscript, bool) {
	switch backendName {
	case "", backend.ClaudeBackendName:
		return &claudeTranscript{workDir: workDir, started: started, session: sessionName}, true
	default:
		return nil, false
	}
}

// claudeTranscript tails the Claude Code JSONL transcript, located via
// the backend's existing LocateTranscript (the same lookup the
// session-log copier uses at session end).
type claudeTranscript struct {
	workDir string
	started time.Time
	session string
	// locateFn overrides transcript discovery (test seam).
	locateFn func() (string, error)
}

func (c *claudeTranscript) Locate() (string, error) {
	if c.locateFn != nil {
		return c.locateFn()
	}
	be, ok := backend.Get(backend.ClaudeBackendName)
	if !ok {
		return "", fmt.Errorf("claude-code backend not registered")
	}
	return be.LocateTranscript(c.workDir, c.started, c.session)
}

// claudeWatchEntry mirrors the subset of the Claude Code JSONL schema
// the relay cares about (see backend/claude.go for the log-viewer
// formatter over the same file).
type claudeWatchEntry struct {
	Type    string `json:"type"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeWatchBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ParseLine emits assistant text blocks verbatim, tool uses as
// one-liners, and a TurnBreak for real user turns (typed text — NOT
// the "user" entries that merely carry tool_result blocks). Everything
// else — thinking blocks, the custom-title line, progress records — is
// skipped.
func (c *claudeTranscript) ParseLine(line []byte) []Emission {
	var entry claudeWatchEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil
	}
	if entry.Type == "user" && len(entry.Message.Content) > 0 {
		if userEntryIsTurn(entry.Message.Content) {
			return []Emission{{Kind: EmissionTurnBreak}}
		}
		return nil
	}
	if entry.Type != "assistant" || len(entry.Message.Content) == 0 {
		return nil
	}

	// Content is either a plain string or a block list.
	var s string
	if err := json.Unmarshal(entry.Message.Content, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []Emission{{Kind: EmissionAssistantText, Text: s}}
	}

	var blocks []claudeWatchBlock
	if err := json.Unmarshal(entry.Message.Content, &blocks); err != nil {
		return nil
	}
	var out []Emission
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, Emission{Kind: EmissionAssistantText, Text: b.Text})
			}
		case "tool_use":
			out = append(out, Emission{Kind: EmissionToolUse, Text: toolOneLiner(b.Name, b.Input)})
		}
	}
	return out
}

// toolOneLiner compresses a tool_use block into one line: commands
// render as "⚒ Bash: make test", path-shaped inputs as
// "⚒ Edit internal/tui/model.go", anything else as the bare name.
func toolOneLiner(name string, input map[string]any) string {
	if name == "" {
		name = "tool"
	}
	if cmd, ok := input["command"].(string); ok && strings.TrimSpace(cmd) != "" {
		return "⚒ " + name + ": " + firstLineTrunc(cmd, 120)
	}
	for _, key := range []string{"file_path", "path", "pattern", "url", "query", "description"} {
		if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
			return "⚒ " + name + " " + firstLineTrunc(v, 120)
		}
	}
	return "⚒ " + name
}

// userEntryIsTurn reports whether a transcript "user" entry is an
// actual typed turn: a plain-string content, or a block list carrying
// a text block. Entries whose blocks are only tool_result carriers are
// mid-turn plumbing, not a new question.
func userEntryIsTurn(content json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return strings.TrimSpace(s) != ""
	}
	var blocks []claudeWatchBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}

// firstLineTrunc reduces s to its first non-empty line, capped at max
// runes with an ellipsis.
func firstLineTrunc(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	runes := []rune(s)
	if len(runes) > max {
		s = string(runes[:max]) + "…"
	}
	return s
}

// TranscriptTailer polls a TailableTranscript for appended lines and
// feeds parsed emissions to Emit. It tolerates the file not existing
// yet (the agent creates it after startup) and truncation (offset
// resets to zero); any other read error is returned so the caller can
// fall back to screen deltas.
type TranscriptTailer struct {
	Source TailableTranscript
	Emit   func(Emission)
	Poll   time.Duration // 0 → tailPollInterval
}

// staleRelocateAfter is how many consecutive no-growth polls the tailer
// tolerates before re-running Locate. A tailer can lock onto the wrong
// file when it locates before the session's own transcript exists (the
// live failure: a just-killed predecessor's file matched, was replayed,
// and was then tailed forever while the real session streamed unseen).
// Re-locating on staleness self-heals: once the live file exists it is
// the freshest match, and switching resets the offset so the session's
// content is delivered from the top.
const staleRelocateAfter = 8

// Run tails until ctx is cancelled or done closes; the done path does
// one final drain so trailing lines written at session end still land.
func (t *TranscriptTailer) Run(ctx context.Context, done <-chan struct{}) error {
	poll := t.Poll
	if poll <= 0 {
		poll = tailPollInterval
	}
	var (
		path    string
		offset  int64
		partial []byte
		stale   int
	)
	for {
		final := false
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			final = true
		default:
		}
		if path == "" {
			if p, err := t.Source.Locate(); err == nil && p != "" {
				path = p
			}
		}
		if path != "" {
			grew, err := t.drain(path, &offset, &partial)
			switch {
			case err != nil && os.IsNotExist(err):
				// The file vanished (or was never created despite
				// Locate) — restart discovery from scratch.
				path, offset, partial, stale = "", 0, nil, 0
			case err != nil:
				return err
			case grew:
				stale = 0
			default:
				stale++
				if stale >= staleRelocateAfter {
					stale = 0
					if p, err := t.Source.Locate(); err == nil && p != "" && p != path {
						path, offset, partial = p, 0, nil
					}
				}
			}
		}
		if final {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-done:
			timer.Stop() // loop once more for the final drain
		case <-timer.C:
		}
	}
}

// drain reads bytes appended since *offset and emits every complete
// line; a trailing partial line is buffered until its newline arrives.
// The bool reports whether the file had new bytes (the staleness
// signal for the re-locate logic in Run).
func (t *TranscriptTailer) drain(path string, offset *int64, partial *[]byte) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if fi.Size() < *offset {
		*offset, *partial = 0, nil // truncated / rewritten
	}
	if fi.Size() == *offset {
		return false, nil
	}
	f, err := os.Open(path) //nolint:gosec // path comes from the backend's transcript discovery
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(*offset, io.SeekStart); err != nil {
		return false, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false, err
	}
	*offset += int64(len(data))
	buf := append(*partial, data...)
	for {
		nl := bytes.IndexByte(buf, '\n')
		if nl < 0 {
			break
		}
		line := bytes.TrimSpace(buf[:nl])
		buf = buf[nl+1:]
		if len(line) == 0 {
			continue
		}
		for _, e := range t.Source.ParseLine(line) {
			t.Emit(e)
		}
	}
	*partial = append([]byte(nil), buf...)
	return true, nil
}

// ---------------------------------------------------------------------------
// Tier 2 — screen deltas

// runScreenDeltas emits a normalized plain-text snapshot whenever the
// screen content actually changed, debounced by interval. Exits when
// ctx is cancelled or the session ends (after one final snapshot).
func runScreenDeltas(ctx context.Context, done <-chan struct{}, snapshot func() []string, emit func(Emission), interval time.Duration) {
	if interval <= 0 {
		interval = screenDeltaInterval
	}
	var last string
	for {
		final := false
		select {
		case <-ctx.Done():
			return
		case <-done:
			final = true
		default:
		}
		if s := normalizeScreen(snapshot()); s != "" && s != last {
			last = s
			emit(Emission{Kind: EmissionScreen, Text: s})
		}
		if final {
			return
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-done:
			timer.Stop() // loop once more for the final snapshot
		case <-timer.C:
		}
	}
}

// normalizeScreen converts raw screen lines to comparable plain text:
// per-line ANSI strip + \r resolution + right-trim (the mcpserver
// plainLine approach), then blank top/bottom edges dropped.
func normalizeScreen(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, plainScreenLine(l))
	}
	start := 0
	for start < len(out) && out[start] == "" {
		start++
	}
	end := len(out)
	for end > start && out[end-1] == "" {
		end--
	}
	return strings.Join(out[start:end], "\n")
}

// plainScreenLine converts one raw line to plain text: ANSI escapes
// stripped, carriage-return overwrites (spinner redraws) resolved to
// the last non-empty segment, trailing padding trimmed.
func plainScreenLine(l string) string {
	l = ansi.Strip(l)
	if strings.ContainsRune(l, '\r') {
		segments := strings.Split(l, "\r")
		l = ""
		for i := len(segments) - 1; i >= 0; i-- {
			if segments[i] != "" {
				l = segments[i]
				break
			}
		}
	}
	return strings.TrimRight(l, " ")
}

// ---------------------------------------------------------------------------
// Per-chat sender: coalescing, edit-in-place growth, flood cap

// chatSender owns the Telegram-side rate discipline for one chat's
// relay. Emissions accumulate in pending; flush delivers them at most
// once per coalesce interval, growing the current assistant message in
// place when possible and engaging the flood throttle under load.
type chatSender struct {
	send        func(ctx context.Context, text string) (int64, error)
	edit        func(ctx context.Context, messageID int64, text string) error
	now         func() time.Time
	logf        func(format string, args ...any)
	coalesce    time.Duration
	throttleGap time.Duration

	mu        sync.Mutex
	pending   []Emission
	growID    int64  // message currently growing via edits (0 = none)
	growText  string // its rendered HTML text
	lastFlush time.Time
	sendLog   []time.Time // delivery timestamps inside the flood window
	throttled bool
	// lastActivity is when the last emission was queued (seeded to
	// sender creation, i.e. session start). Drives the typing indicator.
	lastActivity time.Time
}

// activeWithin reports whether an emission was queued within window.
func (cs *chatSender) activeWithin(window time.Duration) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.now().Sub(cs.lastActivity) < window
}

// Add queues one emission for the next flush.
func (cs *chatSender) Add(e Emission) {
	cs.mu.Lock()
	cs.lastActivity = cs.now()
	cs.pending = append(cs.pending, e)
	cs.mu.Unlock()
}

// flush delivers pending emissions if the pacing interval has elapsed
// (force bypasses pacing — used for the final flush at session end).
func (cs *chatSender) flush(ctx context.Context, force bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.pending) == 0 {
		return
	}
	now := cs.now()
	cs.pruneWindowLocked(now)
	if cs.throttled && len(cs.sendLog) <= floodRecoverSends {
		cs.throttled = false
	}
	gap := cs.coalesce
	if cs.throttled {
		gap = cs.throttleGap
	}
	if !force && !cs.lastFlush.IsZero() && now.Sub(cs.lastFlush) < gap {
		return
	}
	batch := cs.pending
	cs.pending = nil
	cs.lastFlush = now

	// Turn breaks: a real user turn ends the current edit-grown message
	// so the answer to a new question starts a NEW bubble, positioned
	// after the user's own message. The break itself renders nothing.
	if hasTurnBreak(batch) {
		cs.growID, cs.growText = 0, ""
		kept := batch[:0]
		for _, e := range batch {
			if e.Kind != EmissionTurnBreak {
				kept = append(kept, e)
			}
		}
		batch = kept
		if len(batch) == 0 {
			return
		}
	}

	// Edit-in-place growth: while the batch is pure assistant text and
	// the combined message stays under the growth ceiling, edit the
	// current message instead of sending a new one.
	if cs.growID != 0 && allAssistantText(batch) {
		combined := cs.growText + "\n\n" + joinAssistantText(batch)
		if len(combined) <= growEditLimit {
			err := cs.edit(ctx, cs.growID, combined)
			cs.recordDeliveryLocked(ctx, now)
			if err == nil {
				cs.growText = combined
				return
			}
			cs.logf("WARN: telegram watch: editMessageText failed (sending a new message instead): %v", err)
		}
	}
	cs.growID, cs.growText = 0, ""

	msgs := buildMessages(batch)
	var lastID int64
	failed := false
	for _, m := range msgs {
		id, err := cs.send(ctx, m)
		cs.recordDeliveryLocked(ctx, now)
		if err != nil {
			failed = true
			cs.logf("WARN: telegram watch: sendMessage failed: %v", err)
			continue
		}
		lastID = id
	}
	if !failed && len(msgs) == 1 && allAssistantText(batch) && len(msgs[0]) <= growEditLimit {
		cs.growID, cs.growText = lastID, msgs[0]
	}
}

// pruneWindowLocked drops delivery timestamps older than the flood window.
func (cs *chatSender) pruneWindowLocked(now time.Time) {
	cutoff := now.Add(-floodWindow)
	i := 0
	for i < len(cs.sendLog) && !cs.sendLog[i].After(cutoff) {
		i++
	}
	cs.sendLog = cs.sendLog[i:]
}

// recordDeliveryLocked counts one send/edit toward the flood window and
// engages the throttle (with a single notice) when the cap is hit.
func (cs *chatSender) recordDeliveryLocked(ctx context.Context, now time.Time) {
	cs.sendLog = append(cs.sendLog, now)
	if cs.throttled || len(cs.sendLog) < floodMaxSends {
		return
	}
	cs.throttled = true
	if _, err := cs.send(ctx, floodNotice); err != nil {
		cs.logf("WARN: telegram watch: flood notice failed: %v", err)
	}
	cs.sendLog = append(cs.sendLog, now)
}

// hasTurnBreak reports whether the batch contains a user-turn break.
func hasTurnBreak(batch []Emission) bool {
	for _, e := range batch {
		if e.Kind == EmissionTurnBreak {
			return true
		}
	}
	return false
}

// allAssistantText reports whether the batch is non-empty pure
// assistant text (the only shape that grows via edits).
func allAssistantText(batch []Emission) bool {
	for _, e := range batch {
		if e.Kind != EmissionAssistantText {
			return false
		}
	}
	return len(batch) > 0
}

// joinAssistantText renders a pure-text batch for the growth path.
func joinAssistantText(batch []Emission) string {
	parts := make([]string, 0, len(batch))
	for _, e := range batch {
		parts = append(parts, renderEmission(e))
	}
	return strings.Join(parts, "\n\n")
}

// buildMessages renders a batch into ready-to-send Telegram HTML
// messages, each within the 4096-char cap. Screen snapshots become
// standalone <pre> messages (chunked on the body so tags never split);
// everything else coalesces into shared messages, with consecutive
// tool one-liners packed one per line.
func buildMessages(batch []Emission) []string {
	var msgs []string
	var cur strings.Builder
	flushCur := func() {
		if cur.Len() > 0 {
			msgs = append(msgs, chunkLines(cur.String())...)
			cur.Reset()
		}
	}
	prev := EmissionKind(-1)
	for _, e := range batch {
		if e.Kind == EmissionScreen {
			flushCur()
			msgs = append(msgs, preChunks(e.Text)...)
			prev = e.Kind
			continue
		}
		if cur.Len() > 0 {
			if e.Kind == EmissionToolUse && prev == EmissionToolUse {
				cur.WriteByte('\n')
			} else {
				cur.WriteString("\n\n")
			}
		}
		cur.WriteString(renderEmission(e))
		prev = e.Kind
	}
	flushCur()
	return msgs
}

// renderEmission maps one emission onto Telegram HTML. Tags wrap whole
// single-line content only (tool one-liners, markers), so line-boundary
// chunking can never split a tag.
func renderEmission(e Emission) string {
	switch e.Kind {
	case EmissionToolUse:
		return "<i>" + EscapeHTML(e.Text) + "</i>"
	case EmissionMarker:
		return "<b>" + EscapeHTML(e.Text) + "</b>"
	default:
		return EscapeHTML(strings.TrimRight(e.Text, "\n"))
	}
}

// preChunks wraps a screen snapshot in <pre> blocks, chunking the
// escaped body so every message stays under the cap with its tags
// balanced.
func preChunks(body string) []string {
	const overhead = len("<pre></pre>")
	chunks := chunkLinesLimit(EscapeHTML(body), maxMessageLen-overhead)
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, "<pre>"+c+"</pre>")
	}
	return out
}

// ---------------------------------------------------------------------------
// Relay lifecycle

// chatRelay is one chat's live relay of one session.
type chatRelay struct {
	key    string // session identity — see sessionKey
	cancel context.CancelFunc
	done   chan struct{}
}

// sessionKey identifies a session so the reconciler can tell "same
// session, keep the relay" from "new session, restart it".
func sessionKey(s *WatchedSession) string {
	return fmt.Sprintf("%s|%d|%d", s.ProjectID, s.TaskNumber, s.StartedAt.UnixNano())
}

// defaultTailerFor is the production tailer factory.
func defaultTailerFor(sess *WatchedSession) (TailableTranscript, bool) {
	return TailerFor(sess.BackendName, sess.WorkDir, sess.StartedAt, sess.SessionName)
}

// watchLoop reconciles watching chats against live sessions until ctx
// is cancelled, then stops every relay (bridge shutdown stops tailers
// cleanly).
func (b *Bridge) watchLoop(ctx context.Context) {
	defer b.stopAllRelays()
	for {
		if ctx.Err() != nil {
			return
		}
		b.reconcileWatch(ctx)
		b.sleepFn(ctx, b.watchPoll)
		if ctx.Err() != nil {
			return
		}
	}
}

// resolveWatchSession picks the session a watching chat should relay:
// its /use-selected project's live session, or — when the chat never
// selected one — the most recently started live session anywhere, so a
// fresh pairing streams activity instead of sitting silent.
func (b *Bridge) resolveWatchSession(projectID string) (*WatchedSession, bool) {
	if projectID != "" {
		return b.sessions.ActiveSession(projectID)
	}
	var best *WatchedSession
	for _, pid := range b.sessions.ActiveProjects() {
		sess, ok := b.sessions.ActiveSession(pid)
		if !ok {
			continue
		}
		if best == nil || sess.StartedAt.After(best.StartedAt) {
			best = sess
		}
	}
	return best, best != nil
}

// reconcileWatch starts relays for watching chats that resolve to a
// live session, and stops relays whose chat stopped watching or whose
// session was replaced. Each watching chat gets its own relay state.
func (b *Bridge) reconcileWatch(ctx context.Context) {
	if b.sessions == nil {
		return
	}
	watching := make(map[int64]string)
	b.mu.Lock()
	for chatID, chat := range b.paired {
		if chat.Watching() {
			// Empty DefaultProjectID = auto-attach (resolveWatchSession).
			watching[chatID] = chat.DefaultProjectID
		}
	}
	b.mu.Unlock()

	b.watchMu.Lock()
	defer b.watchMu.Unlock()
	for chatID, r := range b.relays {
		projectID, ok := watching[chatID]
		if !ok {
			r.cancel()
			delete(b.relays, chatID)
			continue
		}
		sess, live := b.resolveWatchSession(projectID)
		if !live {
			// Session gone — drop the relay once its final flush ran.
			select {
			case <-r.done:
				delete(b.relays, chatID)
			default:
			}
			continue
		}
		if r.key != sessionKey(sess) {
			// A different session took over (project switched via /use,
			// a new task chained, or auto-attach moved to a newer
			// session) — restart below.
			r.cancel()
			delete(b.relays, chatID)
		}
	}
	for chatID, projectID := range watching {
		if _, exists := b.relays[chatID]; exists {
			continue
		}
		sess, ok := b.resolveWatchSession(projectID)
		if !ok {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		b.relays[chatID] = b.startRelay(ctx, chatID, sess)
	}
}

// stopAllRelays cancels every relay and waits briefly for their final
// cleanup. Called on bridge shutdown.
func (b *Bridge) stopAllRelays() {
	b.watchMu.Lock()
	relays := make([]*chatRelay, 0, len(b.relays))
	for _, r := range b.relays {
		relays = append(relays, r)
	}
	b.relays = make(map[int64]*chatRelay)
	b.watchMu.Unlock()
	for _, r := range relays {
		r.cancel()
	}
	for _, r := range relays {
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
		}
	}
}

// startRelay spawns the relay goroutine for one chat + session. Must be
// called with watchMu held.
func (b *Bridge) startRelay(ctx context.Context, chatID int64, sess *WatchedSession) *chatRelay {
	rctx, cancel := context.WithCancel(ctx)
	r := &chatRelay{key: sessionKey(sess), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		b.runRelay(rctx, chatID, sess)
	}()
	return r
}

// newChatSender builds the rate-disciplined sender for one chat.
func (b *Bridge) newChatSender(chatID int64) *chatSender {
	return &chatSender{
		send: func(ctx context.Context, text string) (int64, error) {
			return b.client.SendMessage(ctx, b.token, chatID, text)
		},
		edit: func(ctx context.Context, messageID int64, text string) error {
			return b.client.EditMessageText(ctx, b.token, chatID, messageID, text)
		},
		now:          time.Now,
		logf:         b.logger.Printf,
		coalesce:     b.coalesceEvery,
		throttleGap:  throttleInterval,
		lastActivity: time.Now(),
	}
}

// runRelay relays one session to one chat: start marker, source tier
// (transcript tail, else screen deltas), outcome marker, final flush.
func (b *Bridge) runRelay(ctx context.Context, chatID int64, sess *WatchedSession) {
	sender := b.newChatSender(chatID)
	if marker := startMarker(sess); marker != "" {
		sender.Add(Emission{Kind: EmissionMarker, Text: marker})
	}

	stopFlush := make(chan struct{})
	go func() {
		for {
			timer := time.NewTimer(b.flushEvery)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-stopFlush:
				timer.Stop()
				return
			case <-timer.C:
				sender.flush(ctx, false)
			}
		}
	}()

	// Auth watch: when the session raises auth_required (Claude's OAuth
	// token revoked — "Please run /login"), tell the chat once how to
	// fix it from the phone. Polled on the typing tick; cheap.
	loginHinted := false
	go func() {
		for {
			timer := time.NewTimer(b.typingEvery)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-stopFlush:
				timer.Stop()
				return
			case <-timer.C:
				if !loginHinted && sess.IssueType != nil && sess.IssueType() == "auth_required" {
					loginHinted = true
					sender.Add(Emission{Kind: EmissionMarker, Text: loginHint(sess)})
				}
			}
		}
	}()

	// Typing indicator: keep the chat's "typing…" status alive while the
	// session is recently active, so between coalesced sends (and while
	// the agent thinks) the user sees that something is happening.
	go func() {
		b.sendTyping(ctx, chatID)
		for {
			timer := time.NewTimer(b.typingEvery)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-stopFlush:
				timer.Stop()
				return
			case <-timer.C:
				if sender.activeWithin(b.typingWindow) {
					b.sendTyping(ctx, chatID)
				}
			}
		}
	}()

	if sess.Mode == "wildfire" && sess.TaskNumber == 0 {
		// Wildfire planning phases (generate / refine) relay milestones,
		// not a raw stream — the high-level "what did the loop decide"
		// feed. Execute-phase task sessions and every other mode keep
		// the full conversation relay below.
		select {
		case <-ctx.Done():
		case <-sess.Done:
		}
	} else {
		relayed := false
		if src, ok := b.tailerForFn(sess); ok {
			relayed = true
			tailer := &TranscriptTailer{Source: src, Emit: sender.Add, Poll: b.tailPoll}
			if err := tailer.Run(ctx, sess.Done); err != nil {
				b.logger.Printf("WARN: telegram watch: transcript tailer for project %s failed (%v) — falling back to screen deltas", sess.ProjectID, err)
				relayed = false
			}
		}
		if !relayed {
			if sess.Snapshot != nil {
				runScreenDeltas(ctx, sess.Done, sess.Snapshot, sender.Add, b.screenEvery)
			} else {
				select {
				case <-ctx.Done():
				case <-sess.Done:
				}
			}
		}
	}
	close(stopFlush)
	if ctx.Err() != nil {
		return
	}
	if sess.TaskNumber > 0 {
		sender.Add(Emission{Kind: EmissionMarker, Text: b.outcomeMarker(ctx, sess)})
	}
	// Wildfire planning phases close with what they produced: the tasks
	// created during the session window ("generated task NNNN — title").
	if sess.Mode == "wildfire" && sess.TaskNumber == 0 && b.sessions != nil {
		created := b.sessions.TasksCreatedSince(sess.ProjectPath, sess.StartedAt)
		for _, t := range created {
			sender.Add(Emission{Kind: EmissionMarker, Text: fmt.Sprintf("✚ generated task %04d — %s", t.Number, t.Title)})
		}
		if len(created) == 0 && sess.Phase == "generate" {
			sender.Add(Emission{Kind: EmissionMarker, Text: "🔥 wildfire — no new tasks generated"})
		}
	}
	sender.flush(ctx, true)
}

// startMarker renders the session-start milestone. Wildfire sessions get
// phase-specific markers (the milestone feed); task sessions in any mode
// name the task; plain chat/generate sessions open silently.
func startMarker(sess *WatchedSession) string {
	if sess.Mode == "wildfire" {
		switch {
		case sess.TaskNumber > 0:
			return fmt.Sprintf("🔥 wildfire — implementing task %04d — %s", sess.TaskNumber, sess.TaskTitle)
		case sess.Phase == "generate":
			return "🔥 wildfire — generating new tasks…"
		case sess.Phase == "refine":
			return "🔥 wildfire — reviewing the plan and refining the backlog…"
		default:
			return "🔥 wildfire — " + sess.Phase
		}
	}
	if sess.TaskNumber > 0 {
		return fmt.Sprintf("▶ task %04d — %s", sess.TaskNumber, sess.TaskTitle)
	}
	return ""
}

// sendTyping best-effort sets the chat's "typing…" status. Cosmetic —
// errors are ignored (the next real send will surface a broken chat).
func (b *Bridge) sendTyping(ctx context.Context, chatID int64) {
	_ = b.client.SendChatAction(ctx, b.token, chatID, "typing")
}

// outcomeMarker resolves the session-end marker from the task's final
// state, polling briefly because the merge runs after process exit.
func (b *Bridge) outcomeMarker(ctx context.Context, sess *WatchedSession) string {
	for attempt := 0; attempt < outcomeAttempts && ctx.Err() == nil; attempt++ {
		if attempt > 0 {
			b.sleepFn(ctx, b.outcomeRetry)
		}
		oc, err := b.sessions.TaskOutcome(sess.ProjectPath, sess.TaskNumber)
		if err != nil || !oc.Done {
			continue
		}
		switch {
		case !oc.Success:
			reason := oc.FailureReason
			if reason == "" {
				reason = "no reason given"
			}
			return fmt.Sprintf("✖ task %04d failed: %s", sess.TaskNumber, reason)
		case oc.MergeFailureReason != "":
			return fmt.Sprintf("⚠ task %04d done, but merge failed: %s", sess.TaskNumber, oc.MergeFailureReason)
		case oc.Merged:
			return fmt.Sprintf("✔ task %04d merged", sess.TaskNumber)
		default:
			return fmt.Sprintf("✔ task %04d done", sess.TaskNumber)
		}
	}
	return fmt.Sprintf("■ task %04d — session ended", sess.TaskNumber)
}

// ---------------------------------------------------------------------------
// /watch command + persistence

// cmdWatch toggles the chat's live relay. The setting persists on the
// paired-chat record (survives daemon restarts) and applies to the
// chat's active project only.
func (b *Bridge) cmdWatch(ctx context.Context, chatID int64, rest string) {
	b.mu.Lock()
	chat := b.paired[chatID]
	b.mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(rest)) {
	case "on":
		if err := b.setWatch(chatID, true); err != nil {
			b.logger.Printf("ERROR: telegram bridge: persist watch for chat %d: %v", chatID, err)
			b.reply(ctx, chatID, "Failed to save the watch setting — please try again.")
			return
		}
		msg := "🔭 Watch is <b>on</b> — I'll relay the agent conversation for your active project here. Send /watch off to stop."
		if chat.DefaultProjectID == "" {
			msg += "\nNo project selected — I'll follow whichever session is live. Pin one with /projects, then /use &lt;name|number&gt;."
		}
		b.reply(ctx, chatID, msg)
		b.reconcileWatch(ctx)
	case "off":
		if err := b.setWatch(chatID, false); err != nil {
			b.logger.Printf("ERROR: telegram bridge: persist watch for chat %d: %v", chatID, err)
			b.reply(ctx, chatID, "Failed to save the watch setting — please try again.")
			return
		}
		b.reply(ctx, chatID, "Watch is <b>off</b>.")
		b.reconcileWatch(ctx)
	case "":
		state := "off"
		if chat.Watching() {
			state = "on"
		}
		b.reply(ctx, chatID, "Watch is <b>"+state+"</b>. Usage: /watch on|off")
	default:
		b.reply(ctx, chatID, "Usage: /watch on|off")
	}
}

// setWatch persists the toggle and mirrors it into the in-memory
// allowlist snapshot.
func (b *Bridge) setWatch(chatID int64, on bool) error {
	if err := b.persistWatchFn(chatID, on); err != nil {
		return err
	}
	b.mu.Lock()
	if chat, ok := b.paired[chatID]; ok {
		chat.WatchOff = !on
		b.paired[chatID] = chat
	}
	b.mu.Unlock()
	return nil
}

// persistWatch is the production /watch persist hook — same
// config.SaveIntegrations path as /use, so the toggle lands in
// integrations.yaml and survives daemon restarts.
func persistWatch(chatID int64, watch bool) error {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return err
	}
	if cfg.Telegram == nil {
		return fmt.Errorf("telegram is not configured")
	}
	for i := range cfg.Telegram.PairedChats {
		if cfg.Telegram.PairedChats[i].ChatID != chatID {
			continue
		}
		cfg.Telegram.PairedChats[i].WatchOff = !watch
		return config.SaveIntegrations(cfg)
	}
	return fmt.Errorf("chat %d is not paired", chatID)
}
