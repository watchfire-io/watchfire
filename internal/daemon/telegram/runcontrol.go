// Run-control verbs for paired chats (v10.0 Torch, task 0142):
// /run /runall /retry /cancel /screen /say /mute /unmute.
//
// /retry and /cancel are pure dispatch — they route through echo.Route
// exactly like /status, reusing the production task-0133 callbacks.
// /run and /runall start sessions through the RunController seam with
// the same semantics as the MCP run_task tool: refuse when an agent is
// already running for the project — never queue, never replace.
// /screen is a one-shot plain-text tail of the live session, reusing
// the tier-2 normalization from watch mode (task 0141).
//
// /say is the ONLY write path into an agent PTY in the entire telegram
// package: the user's text verbatim plus exactly one carriage return,
// injected through injectSay — the single call site the source-guard
// test (watch_guard_test.go) allowlists. Everything else in this
// package only ever reads.
package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/models"
)

// screenTailLines caps the /screen snapshot at the last N normalized
// lines — a phone-sized tail, not the whole scrollback.
const screenTailLines = 40

// RunStart describes the session a run-control verb started, for the
// confirmation reply.
type RunStart struct {
	TaskNumber int
	TaskTitle  string
}

// RunController is the seam through which /run and /runall start agent
// sessions and /say writes input. The production implementation lives
// in the server package over the agent manager (the telegram package
// must never import it — guard-tested); tests inject stubs.
//
// The mode starters REPLACE a running agent through the daemon's atomic
// kill+restart — the same semantics as the GUI mode buttons and the
// TUI. (The MCP run_task "never replace" contract is MCP-only, where
// the caller is another agent.) StartChat is the exception: a chat
// start never displaces a working non-chat agent — the daemon refuses
// that itself.
type RunController interface {
	// StartTask starts a task-mode session on taskNumber.
	StartTask(ctx context.Context, projectID string, taskNumber int) (RunStart, error)
	// StartRunAll starts run-all mode (the daemon chains through the
	// ready queue after each merge).
	StartRunAll(ctx context.Context, projectID string) (RunStart, error)
	// StartChat starts an interactive chat-mode session — the
	// plain-text conversation path auto-starts one when nothing is
	// running. Same never-queue-never-replace contract as the others.
	StartChat(ctx context.Context, projectID string) (RunStart, error)
	// StartWildfire starts the autonomous wildfire loop (/wildfire on).
	// Same never-queue-never-replace contract as the others.
	StartWildfire(ctx context.Context, projectID string) (RunStart, error)
	// StartGenerate starts a generate-definition session (/generate) —
	// the agent analyzes the codebase and writes the project definition.
	StartGenerate(ctx context.Context, projectID string) (RunStart, error)
	// StartPlan starts a generate-tasks session (/plan) — the agent
	// derives tasks from the project definition.
	StartPlan(ctx context.Context, projectID string) (RunStart, error)
	// RestartChat starts a FRESH chat session (/new), atomically
	// replacing a running chat via the daemon's chat-over-chat replace
	// path; the daemon itself refuses to displace a working (non-chat)
	// agent, so this needs no manual refusal pre-check.
	RestartChat(ctx context.Context, projectID string) (RunStart, error)
	// StopAgent user-stops the running agent (also ends a wildfire /
	// run-all chain) — the /wildfire off path.
	StopAgent(projectID string) error
	// SendInput writes raw bytes to the running agent's PTY. Reserved
	// for the injectSay path.
	SendInput(projectID string, data []byte) error
}

// injectSay is the single sanctioned PTY write path in the telegram
// package: the user's text verbatim, then — after a short beat — one
// carriage return (Enter) as its own write. Sending text+\r as a
// single chunk trips the agent CLI's paste detection, which absorbs
// the trailing Enter into the pasted content instead of submitting
// (observed live: the message sat in Claude Code's input box, unsent).
// The split write mirrors a human typing and then pressing Enter. The
// watch_guard_test source guard allowlists precisely this call site —
// any other SendInput reference in the package fails the build's tests.
func (b *Bridge) injectSay(projectID, text string) error {
	for i, chunk := range [][]byte{[]byte(text), {'\r'}} {
		if i > 0 {
			b.sleepFn(context.Background(), b.sayEnterDelay)
		}
		if err := b.runner.SendInput(projectID, chunk); err != nil {
			return err
		}
	}
	return nil
}

// chatProject resolves the chat's selected project id, prompting for
// /use when none is selected yet.
func (b *Bridge) chatProject(ctx context.Context, chatID int64) (string, bool) {
	b.mu.Lock()
	projectID := b.paired[chatID].DefaultProjectID
	b.mu.Unlock()
	if projectID == "" {
		b.reply(ctx, chatID, "No project selected yet — send /projects, then /use &lt;name|number&gt;.")
		return "", false
	}
	return projectID, true
}

// runningSession reports the live session for the chat's project, if
// the bridge has a session source to ask.
func (b *Bridge) runningSession(projectID string) (*WatchedSession, bool) {
	if b.sessions == nil {
		return nil, false
	}
	return b.sessions.ActiveSession(projectID)
}

// replacedNote is the suffix a starter's confirmation carries when it
// displaced a running agent. Mode switches from Telegram REPLACE the
// running agent — exactly like the GUI's mode buttons and the TUI —
// through the daemon's atomic kill+restart; the MCP "never replace"
// contract stays MCP-only, where the caller is another agent. The
// note keeps the replacement honest rather than silent.
func replacedNote(prev *WatchedSession, had bool) string {
	if !had {
		return ""
	}
	return " Replaced the running " + sessionStateLine(prev) + "."
}

// chatRefusal is the one remaining refusal: /new (a chat start) never
// displaces a working non-chat agent — the daemon refuses that too.
func chatRefusal(sess *WatchedSession) string {
	return "⛔ A working agent is running for this project (" + sessionStateLine(sess) + "). /stop it first — /new only replaces a chat session."
}

// requireRunner resolves the run-control seam, replying with a shrug
// when the bridge was built without one (never the case in production
// wiring).
func (b *Bridge) requireRunner(ctx context.Context, chatID int64) bool {
	if b.runner == nil {
		b.reply(ctx, chatID, "Run controls are not wired up on this daemon.")
		return false
	}
	return true
}

// cmdRun starts one task for the chat's active project, replacing any
// running agent (mode switches replace, like the GUI/TUI) and saying
// so in the confirmation.
func (b *Bridge) cmdRun(ctx context.Context, chatID int64, rest string) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	n, ok := parseTaskNumber(rest)
	if !ok {
		b.reply(ctx, chatID, "Usage: /run &lt;task-number&gt;")
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	prev, had := b.runningSession(projectID)
	started, err := b.runner.StartTask(ctx, projectID, n)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("Failed to start task #%04d: %s", n, EscapeHTML(err.Error())))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("▶ Started task #%04d — <b>%s</b>.%s Watch streams the session; /screen for a snapshot.",
		started.TaskNumber, EscapeHTML(started.TaskTitle), replacedNote(prev, had)))
}

// cmdRunAll starts run-all mode for the chat's active project,
// replacing a running agent like /run.
func (b *Bridge) cmdRunAll(ctx context.Context, chatID int64) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	prev, had := b.runningSession(projectID)
	started, err := b.runner.StartRunAll(ctx, projectID)
	if err != nil {
		b.reply(ctx, chatID, "Failed to start run-all: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("▶ Run-all started on task #%04d — <b>%s</b>.%s The daemon chains through the remaining ready tasks after each merge.",
		started.TaskNumber, EscapeHTML(started.TaskTitle), replacedNote(prev, had)))
}

// cmdRouteVerb dispatches /retry and /cancel through echo.Route — the
// same production handlers Slack and Discord use, rendered as Telegram
// HTML. No verb logic lives here.
func (b *Bridge) cmdRouteVerb(ctx context.Context, chatID, userID int64, verb, rest string) {
	if strings.TrimSpace(rest) == "" {
		b.reply(ctx, chatID, "Usage: /"+verb+" &lt;task-number&gt;")
		return
	}
	cc, ok := b.commandContext(ctx, chatID, userID)
	if !ok {
		return
	}
	resp := echo.Route(ctx, "/watchfire", verb, rest, cc)
	for _, chunk := range RenderHTML(resp) {
		b.reply(ctx, chatID, chunk)
	}
}

// cmdScreen sends a one-shot plain-text tail of the live session:
// the last screenTailLines lines of the tier-2-normalized screen,
// wrapped in <pre>.
func (b *Bridge) cmdScreen(ctx context.Context, chatID int64) {
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	sess, live := b.runningSession(projectID)
	if !live {
		b.reply(ctx, chatID, "No agent is running for this project — /run &lt;n&gt; starts one.")
		return
	}
	var lines []string
	if sess.Snapshot != nil {
		lines = sess.Snapshot()
	}
	normalized := normalizeScreen(lines)
	if normalized == "" {
		b.reply(ctx, chatID, "The session screen is empty right now.")
		return
	}
	for _, m := range preChunks(lastLines(normalized, screenTailLines)) {
		b.reply(ctx, chatID, m)
	}
}

// cmdSay injects text into the running agent's PTY — the one explicit,
// user-initiated write the bridge is allowed. text is the verbatim
// remainder of the message (see sayVerbatim); the bridge appends
// exactly one carriage return.
func (b *Bridge) cmdSay(ctx context.Context, chatID int64, text string) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	if text == "" {
		b.reply(ctx, chatID, "Usage: /say &lt;text&gt;")
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	if _, live := b.runningSession(projectID); !live {
		b.reply(ctx, chatID, "No agent is running for this project — there is no session to send input to.")
		return
	}
	if err := b.injectSay(projectID, text); err != nil {
		b.reply(ctx, chatID, "Failed to send input: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, "→ sent")
}

// cmdNew starts a fresh chat session for the chat's project (/new),
// clearing the previous conversation context. A running chat is
// replaced atomically (the daemon's chat-over-chat path); a working
// non-chat agent is never displaced — same refusal as /run.
func (b *Bridge) cmdNew(ctx context.Context, chatID int64) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	if sess, live := b.runningSession(projectID); live && sess.Mode != "chat" {
		b.reply(ctx, chatID, chatRefusal(sess))
		return
	}
	if _, err := b.runner.RestartChat(ctx, projectID); err != nil {
		b.reply(ctx, chatID, "Failed to start a new chat session: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, "🆕 Fresh chat session started — the previous context is gone. Just type to talk to it.")
}

// cmdStop user-stops whatever agent is running for the chat's active
// project — task, run-all, wildfire (the chain ends), or chat. The
// natural follow-up is just typing: plain text auto-starts a fresh
// chat agent.
func (b *Bridge) cmdStop(ctx context.Context, chatID int64) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	sess, live := b.runningSession(projectID)
	if !live {
		b.reply(ctx, chatID, "Nothing is running for this project.")
		return
	}
	if err := b.runner.StopAgent(projectID); err != nil {
		b.reply(ctx, chatID, "Failed to stop the agent: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, "🛑 Stopped: "+sessionStateLine(sess)+". Just type to start a fresh chat.")
}

// cmdSimpleMode is the shared start path for the one-shot generator
// verbs (/generate, /plan): resolve the chat's project, start (replacing
// any running agent, like the GUI), confirm — naming what was replaced. The watch
// relay (on by default) then streams the session like any other.
func (b *Bridge) cmdSimpleMode(ctx context.Context, chatID int64, start func(context.Context, string) (RunStart, error), confirm string) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	prev, had := b.runningSession(projectID)
	if _, err := start(ctx, projectID); err != nil {
		b.reply(ctx, chatID, "Failed to start: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, confirm+replacedNote(prev, had))
}

// cmdWildfire starts (bare /wildfire — "on"/"start" are aliases) or
// stops (/wildfire off) the autonomous loop for the chat's active
// project. Starting replaces a running agent (like the GUI's Wildfire
// button); stopping user-stops the agent so the chain doesn't continue. The watch relay
// (on by default) then carries the high-level milestone feed:
// "generating new tasks…", "✚ generated task NNNN — title",
// "implementing task NNNN — title", "✔ task NNNN merged", …
func (b *Bridge) cmdWildfire(ctx context.Context, chatID int64, rest string) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	switch strings.ToLower(strings.TrimSpace(rest)) {
	case "", "on", "start":
		// Bare /wildfire starts the loop — "on" and "start" are
		// accepted aliases.
		prev, had := b.runningSession(projectID)
		if had && prev.Mode == "wildfire" {
			state := "🔥 Wildfire is already running"
			if prev.Phase != "" {
				state += " (" + EscapeHTML(prev.Phase) + " phase)"
			}
			b.reply(ctx, chatID, state+". /wildfire off to stop it.")
			return
		}
		if _, err := b.runner.StartWildfire(ctx, projectID); err != nil {
			b.reply(ctx, chatID, "Failed to start wildfire: "+EscapeHTML(err.Error()))
			return
		}
		b.mu.Lock()
		watching := b.paired[chatID].Watching()
		b.mu.Unlock()
		msg := "🔥 Wildfire started — the autonomous loop executes ready tasks, refines the backlog, and generates new tasks until nothing is left. /wildfire off to stop." + replacedNote(prev, had)
		if watching {
			msg += "\nI'll post milestones here as they happen."
		} else {
			msg += "\nSend /watch on to get the milestone feed here."
		}
		b.reply(ctx, chatID, msg)
	case "off", "stop":
		sess, live := b.runningSession(projectID)
		if !live || sess.Mode != "wildfire" {
			b.reply(ctx, chatID, "Wildfire is not running for this project.")
			return
		}
		if err := b.runner.StopAgent(projectID); err != nil {
			b.reply(ctx, chatID, "Failed to stop wildfire: "+EscapeHTML(err.Error()))
			return
		}
		b.reply(ctx, chatID, "🧯 Wildfire stopped.")
	default:
		b.reply(ctx, chatID, "Usage: /wildfire — start the loop; /wildfire off — stop it.")
	}
}

// handlePlainText is the conversational core of the bridge: a paired
// chat's non-command message talks to a CHAT agent with no /say
// prefix. Same delivery guarantees as /say — verbatim text plus
// exactly one Enter through injectSay, which stays the package's only
// PTY write. The target is the session watch mode streams (the chat's
// /use selection, or the auto-attached live session). Three cases:
//
//   - live chat session → inject.
//   - live NON-chat session (task / run-all / wildfire / generate) →
//     never type into a working agent implicitly; explain what's
//     running and offer options (/watch, /screen, explicit /say,
//     /cancel).
//   - nothing running → auto-start a chat agent on the chat's project
//     and deliver the message once the session is ready.
func (b *Bridge) handlePlainText(ctx context.Context, chatID int64, text string) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	if b.sessions == nil {
		b.reply(ctx, chatID, "Sessions are not wired up on this daemon.")
		return
	}
	b.mu.Lock()
	chat := b.paired[chatID]
	b.mu.Unlock()

	if sess, live := b.resolveWatchSession(chat.DefaultProjectID); live {
		if sess.Mode != "chat" {
			b.replyBusyOptions(ctx, chatID, chat, sess)
			return
		}
		if err := b.injectSay(sess.ProjectID, text); err != nil {
			b.reply(ctx, chatID, "Failed to send input: "+EscapeHTML(err.Error()))
			return
		}
		// The agent is now thinking — show "typing…" right away; the
		// watch relay's typing loop takes over once output flows.
		b.sendTyping(ctx, chatID)
		if !chat.Watching() {
			b.reply(ctx, chatID, "→ sent. Send /watch on to stream the agent's replies here.")
		}
		return
	}

	projectID := chat.DefaultProjectID
	if projectID == "" {
		b.reply(ctx, chatID, "No project selected — send /projects, then /use &lt;name|number&gt;, and I'll start a chat agent there.")
		return
	}
	b.queueChatStart(ctx, chatID, projectID, text, chat.Watching())
}

// replyBusyOptions explains that the live session is a working
// (non-chat) agent and lists what the user can do instead of the
// bridge implicitly typing into it.
func (b *Bridge) replyBusyOptions(ctx context.Context, chatID int64, chat models.TelegramPairedChat, sess *WatchedSession) {
	desc := "in mode <b>" + EscapeHTML(sess.Mode) + "</b>"
	if sess.TaskNumber > 0 {
		desc = fmt.Sprintf("on task <b>#%04d</b>", sess.TaskNumber)
		if sess.TaskTitle != "" {
			desc += " — " + EscapeHTML(sess.TaskTitle)
		}
	}
	lines := []string{
		"⚙ The agent is busy " + desc + ", so I won't type into its session. You can:",
	}
	if !chat.Watching() {
		lines = append(lines, "• /watch on — stream what it's doing")
	}
	lines = append(lines,
		"• /screen — see where it is right now",
		"• /say &lt;text&gt; — type into the working session anyway",
	)
	if sess.TaskNumber > 0 {
		lines = append(lines, fmt.Sprintf("• /cancel %d — stop the task", sess.TaskNumber))
	}
	lines = append(lines, "Message me again once it's done and I'll start a chat agent.")
	b.reply(ctx, chatID, strings.Join(lines, "\n"))
}

// chatPendingStart tracks one project's in-flight auto-started chat
// session: the messages queued for delivery and the chats to notify on
// failure.
type chatPendingStart struct {
	chatIDs map[int64]bool
	texts   []string
}

// queueChatStart starts a chat agent for projectID (once — concurrent
// messages while it boots just queue) and hands delivery to
// deliverWhenReady.
func (b *Bridge) queueChatStart(ctx context.Context, chatID int64, projectID, text string, watch bool) {
	b.chatStartMu.Lock()
	if p, ok := b.chatPending[projectID]; ok {
		p.texts = append(p.texts, text)
		p.chatIDs[chatID] = true
		b.chatStartMu.Unlock()
		return
	}
	p := &chatPendingStart{chatIDs: map[int64]bool{chatID: true}, texts: []string{text}}
	b.chatPending[projectID] = p
	b.chatStartMu.Unlock()

	if _, err := b.runner.StartChat(ctx, projectID); err != nil {
		b.takeChatPending(projectID)
		b.reply(ctx, chatID, "Couldn't start a chat agent: "+EscapeHTML(err.Error()))
		return
	}
	msg := "🔥 No agent was running — starting a chat agent. I'll deliver your message when it's ready."
	if !watch {
		msg += " Send /watch on to stream its replies here."
	}
	b.reply(ctx, chatID, msg)
	go b.deliverWhenReady(ctx, projectID)
}

// takeChatPending removes and returns projectID's pending record (nil
// when none).
func (b *Bridge) takeChatPending(projectID string) *chatPendingStart {
	b.chatStartMu.Lock()
	defer b.chatStartMu.Unlock()
	p := b.chatPending[projectID]
	delete(b.chatPending, projectID)
	return p
}

// deliverWhenReady polls the freshly started chat session until its
// screen shows content (the CLI has painted), settles briefly so its
// input loop is accepting keys, then injects the queued messages in
// order. On timeout the queued chats are told the message was not
// delivered.
func (b *Bridge) deliverWhenReady(ctx context.Context, projectID string) {
	deadline := time.Now().Add(b.chatStartWait)
	ready := false
	var lastTyping time.Time
	for time.Now().Before(deadline) && ctx.Err() == nil {
		// Show "typing…" while the agent boots so the wait doesn't read
		// as the message having vanished.
		if time.Since(lastTyping) >= b.typingEvery {
			b.chatStartMu.Lock()
			var ids []int64
			if p := b.chatPending[projectID]; p != nil {
				for cid := range p.chatIDs {
					ids = append(ids, cid)
				}
			}
			b.chatStartMu.Unlock()
			for _, cid := range ids {
				b.sendTyping(ctx, cid)
			}
			lastTyping = time.Now()
		}
		if sess, live := b.sessions.ActiveSession(projectID); live && sess.Mode == "chat" && sess.Snapshot != nil {
			if screenHasContent(sess.Snapshot()) {
				ready = true
				break
			}
		}
		b.sleepFn(ctx, b.chatStartPoll)
	}
	p := b.takeChatPending(projectID)
	if p == nil {
		return
	}
	if !ready {
		for cid := range p.chatIDs {
			b.reply(ctx, cid, "The chat agent didn't come up in time — your message was not delivered. /screen shows its current state.")
		}
		return
	}
	b.sleepFn(ctx, b.chatStartSettle)
	for _, t := range p.texts {
		if err := b.injectSay(projectID, t); err != nil {
			for cid := range p.chatIDs {
				b.reply(ctx, cid, "Failed to send your message: "+EscapeHTML(err.Error()))
			}
			return
		}
	}
}

// screenHasContent reports whether a snapshot has any non-blank line.
func screenHasContent(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// cmdMute toggles the chat's Muted flag (pauses outbound event pushes
// from the relay adapter; commands keep working). Persisted, so it
// survives daemon restarts.
func (b *Bridge) cmdMute(ctx context.Context, chatID int64, mute bool) {
	if err := b.setMuted(chatID, mute); err != nil {
		b.logger.Printf("ERROR: telegram bridge: persist mute for chat %d: %v", chatID, err)
		b.reply(ctx, chatID, "Failed to save the mute setting — please try again.")
		return
	}
	if mute {
		b.reply(ctx, chatID, "🔕 Muted — event pushes to this chat are paused. Commands still work; send /unmute to resume.")
	} else {
		b.reply(ctx, chatID, "🔔 Unmuted — event pushes to this chat are back on.")
	}
}

// setMuted persists the toggle and mirrors it into the in-memory
// allowlist snapshot.
func (b *Bridge) setMuted(chatID int64, muted bool) error {
	if err := b.persistMutedFn(chatID, muted); err != nil {
		return err
	}
	b.mu.Lock()
	if chat, ok := b.paired[chatID]; ok {
		chat.Muted = muted
		b.paired[chatID] = chat
	}
	b.mu.Unlock()
	return nil
}

// persistMuted is the production /mute persist hook — same
// config.SaveIntegrations path as /use and /watch, so the flag lands
// in integrations.yaml where the outbound relay adapter reads it.
func persistMuted(chatID int64, muted bool) error {
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
		cfg.Telegram.PairedChats[i].Muted = muted
		return config.SaveIntegrations(cfg)
	}
	return fmt.Errorf("chat %d is not paired", chatID)
}

// parseTaskNumber parses a /run or /retry style task-number argument
// ("7", "#7", "0007").
func parseTaskNumber(arg string) (int, bool) {
	arg = strings.TrimPrefix(strings.TrimSpace(arg), "#")
	n, err := strconv.Atoi(arg)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// sayVerbatim strips the leading "/say" (or "/say@BotName") token and
// its single separator character from the raw message text, returning
// the remainder verbatim — /say is the one command whose argument must
// NOT be whitespace-normalized, because it is delivered byte-for-byte
// to the agent's PTY.
func sayVerbatim(text string) string {
	t := strings.TrimLeftFunc(text, unicode.IsSpace)
	i := strings.IndexFunc(t, unicode.IsSpace)
	if i < 0 {
		return ""
	}
	_, size := utf8.DecodeRuneInString(t[i:])
	return t[i+size:]
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
