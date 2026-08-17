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
	"unicode"
	"unicode/utf8"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
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
// StartTask / StartRunAll follow the MCP run_task contract: they refuse
// with an error when an agent is already running for the project —
// they never queue behind it and never replace it.
type RunController interface {
	// StartTask starts a task-mode session on taskNumber.
	StartTask(ctx context.Context, projectID string, taskNumber int) (RunStart, error)
	// StartRunAll starts run-all mode (the daemon chains through the
	// ready queue after each merge).
	StartRunAll(ctx context.Context, projectID string) (RunStart, error)
	// SendInput writes raw bytes to the running agent's PTY. Reserved
	// for the explicit /say path.
	SendInput(projectID string, data []byte) error
}

// injectSay is the single sanctioned PTY write in the telegram
// package: the user's text verbatim, terminated by exactly one
// carriage return (Enter). The watch_guard_test source guard
// allowlists precisely this call site — any other SendInput reference
// in the package fails the build's tests.
func (b *Bridge) injectSay(projectID, text string) error {
	return b.runner.SendInput(projectID, append([]byte(text), '\r'))
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

// runRefusal renders the never-queue-never-replace refusal — the same
// contract as the MCP run_task tool, worded for chat.
func runRefusal(sess *WatchedSession) string {
	desc := "mode " + EscapeHTML(sess.Mode)
	if sess.TaskNumber > 0 {
		desc = fmt.Sprintf("task #%04d — %s", sess.TaskNumber, EscapeHTML(sess.TaskTitle))
	}
	return "⛔ An agent is already running for this project (" + desc + "). Runs are never queued or replaced — wait for it to finish or /cancel it first."
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

// cmdRun starts one task for the chat's active project. Refuses while
// an agent is running — same semantics as MCP run_task: never queue,
// never replace.
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
	if sess, live := b.runningSession(projectID); live {
		b.reply(ctx, chatID, runRefusal(sess))
		return
	}
	started, err := b.runner.StartTask(ctx, projectID, n)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("Failed to start task #%04d: %s", n, EscapeHTML(err.Error())))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("▶ Started task #%04d — <b>%s</b>. Send /watch on to follow the session, or /screen for a snapshot.",
		started.TaskNumber, EscapeHTML(started.TaskTitle)))
}

// cmdRunAll starts run-all mode for the chat's active project, with
// the same refusal semantics as /run.
func (b *Bridge) cmdRunAll(ctx context.Context, chatID int64) {
	if !b.requireRunner(ctx, chatID) {
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	if sess, live := b.runningSession(projectID); live {
		b.reply(ctx, chatID, runRefusal(sess))
		return
	}
	started, err := b.runner.StartRunAll(ctx, projectID)
	if err != nil {
		b.reply(ctx, chatID, "Failed to start run-all: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("▶ Run-all started on task #%04d — <b>%s</b>. The daemon chains through the remaining ready tasks after each merge.",
		started.TaskNumber, EscapeHTML(started.TaskTitle)))
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
