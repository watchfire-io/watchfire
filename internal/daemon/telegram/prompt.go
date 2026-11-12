// Approval-prompt relay — v10.2 Torch, task 0003 (issue #58, part 2).
//
// When a Claude Code session stops on an interactive dialog — a tool
// permission prompt, a plan approval, an AskUserQuestion list — the
// daemon raises an input_required issue carrying the prompt and the
// ordered option labels (agent/inputdialog.go). The agent is blocked
// until somebody answers, so the bridge forwards the dialog to the chat
// and lets the user answer it from the phone:
//
//   - Forward. One message per dialog INSTANCE per chat: a "⏸ Waiting
//     for your approval" header, the full prompt (a multi-line command,
//     diff or plan rides a <pre> block, never cut), the options, and an
//     inline keyboard with one button per option. Approval requests are
//     not chatter: a chat with /watch off still gets them. Muted chats
//     stay silent.
//   - Answer. A button tap, or /approve · /deny · /answer <n> [text].
//     The forwarded message is edited to show what was chosen and by
//     whom; when the dialog is answered somewhere else (GUI, TUI) the
//     message is closed out the same way. A tap on a dialog that is no
//     longer on screen is acknowledged and does nothing.
//
// Answering is a PTY write and goes through injectSay like every other
// write in this package. The keys were pinned from live captures of
// Claude Code v2.1.322 (2026-11-12):
//
//   - A digit selects AND submits option N the moment it arrives — the
//     dialog is gone ~50ms later. So the answer is the bare digit
//     (sayKeystroke): no Enter may follow, because it would land on the
//     NEXT thing on screen (see sayKind).
//   - Esc cancels the dialog ("Interrupted · What should Claude do
//     instead?" / "User declined to answer questions"). It is /deny's
//     fallback when the dialog has no "No" option.
//   - The free-text options ("Type something.", "Tell Claude what to
//     change") only take FOCUS on their digit; the text is then typed
//     and Enter submits it. That is digit (sayKeystroke), a beat, then
//     the text as an ordinary sayLine.

package telegram

import (
	"context"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
)

const (
	// callbackAnswerPrefix namespaces prompt-answer buttons on the
	// shared callback pipe: "ans:<project>:<instance>:<option>".
	callbackAnswerPrefix = "ans:"
	// maxCallbackData is the Bot API's cap on callback_data, in bytes.
	maxCallbackData = 64
	// promptMaxOptions is how many options are answerable: the CLI
	// selects by a single digit key.
	promptMaxOptions = 9
	// promptButtonRunes caps a button's label; the full label is in the
	// message body above the keyboard.
	promptButtonRunes = 48
	// promptVerifyTimeout / promptVerifyPollInterval bound the wait for
	// the dialog to leave the screen after an answer was typed. The
	// daemon clears the issue ~250ms after the frame disappears.
	promptVerifyTimeout      = 4 * time.Second
	promptVerifyPollInterval = 100 * time.Millisecond
	// promptAnswerTTL is how long a recorded answer is kept for closing
	// out forwarded messages.
	promptAnswerTTL = time.Hour
	// promptCancelOption is the pseudo-option for Esc (cancel the dialog).
	promptCancelOption = 0
	// promptEscKey is the Escape keypress.
	promptEscKey = "\x1b"
	// promptStaleNotice is the reply to an answer aimed at a dialog that
	// is not on screen any more.
	promptStaleNotice = "That prompt is no longer waiting."
)

// promptPost is one forwarded prompt message in one chat.
type promptPost struct {
	projectID string
	instance  string
	msgID     int64  // the message carrying the keyboard (0 = send failed)
	head      string // header line — the fallback when body + verdict overflows
	body      string // that message's text without the "tap a button" hint
}

// promptAnswer records who answered a dialog instance and with what, so
// every chat's forwarded copy can be closed out with the same verdict.
type promptAnswer struct {
	label string
	who   string
	at    time.Time
}

// promptArm marks a chat whose next plain-text message is the typed
// answer for a free-text option.
type promptArm struct {
	projectID string
	instance  string
	option    int
}

// sessionPrompt returns the dialog sess is parked on, or nil.
func sessionPrompt(sess *WatchedSession) *SessionPrompt {
	if sess == nil || sess.Prompt == nil {
		return nil
	}
	p := sess.Prompt()
	if p == nil || len(p.Options) == 0 {
		return nil
	}
	return p
}

// promptInstance identifies one dialog instance: the session, when the
// daemon raised the dialog, and its wording. It is what a button's
// callback data carries, so a tap on an old message can be told apart
// from the dialog currently on screen even when the wording repeats.
func promptInstance(sess *WatchedSession, p *SessionPrompt) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s|%d|%s|%s", sessionKey(sess), p.DetectedAt.UnixNano(), p.Text, strings.Join(p.Options, "\x00"))
	return fmt.Sprintf("%08x", h.Sum32())
}

func promptKey(projectID, instance string) string { return projectID + "|" + instance }

// flatLabel lower-cases a label and drops everything but letters and
// digits, for wording checks that survive the CLI's spacing quirks.
func flatLabel(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// promptIsApproval reports whether the dialog is a yes/no approval (its
// first option is a "Yes…") rather than an open question.
func promptIsApproval(p *SessionPrompt) bool {
	return len(p.Options) > 0 && strings.HasPrefix(flatLabel(p.Options[0]), "yes")
}

// promptOptionWantsText reports whether selecting the option only
// focuses a text field ("Type something.", "Tell Claude what to
// change", "No, and tell Claude what to do differently") — the answer
// is the text typed after it, submitted with Enter.
func promptOptionWantsText(label string) bool {
	f := flatLabel(label)
	return strings.HasPrefix(f, "typesomething") || strings.Contains(f, "tellclaude")
}

// promptTextOption returns the 1-based index of the dialog's free-text
// option, 0 when it has none.
func promptTextOption(p *SessionPrompt) int {
	for i, opt := range p.Options {
		if i < promptMaxOptions && promptOptionWantsText(opt) {
			return i + 1
		}
	}
	return 0
}

// promptDenyOption returns the 1-based index of the plain "No" option,
// or promptCancelOption (Esc) when the dialog has none — the plan
// approval and AskUserQuestion frames are dismissed with Esc.
func promptDenyOption(p *SessionPrompt) int {
	for i, opt := range p.Options {
		if i < promptMaxOptions && flatLabel(opt) == "no" {
			return i + 1
		}
	}
	return promptCancelOption
}

// truncRunes caps s at n runes with an ellipsis.
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// userLabel names a Telegram user for a verdict line.
func userLabel(username string, id int64) string {
	if username != "" {
		return "@" + username
	}
	return "user " + strconv.FormatInt(id, 10)
}

// ---------------------------------------------------------------------------
// Rendering

// promptWhere names the session a prompt belongs to: "<project> · task
// #0003 — title".
func promptWhere(sess *WatchedSession) string {
	state := sessionStateLine(sess) // already HTML-escaped
	if sess.ProjectPath == "" {
		return "<b>" + state + "</b>"
	}
	return "<b>" + EscapeHTML(filepath.Base(sess.ProjectPath)) + "</b> · " + state
}

// promptHeader is the first line of a forwarded prompt.
func promptHeader(sess *WatchedSession, p *SessionPrompt) string {
	what := "answer"
	if promptIsApproval(p) {
		what = "approval"
	}
	return "⏸ <b>Waiting for your " + what + "</b> on " + promptWhere(sess)
}

// promptHint is the last line of a forwarded prompt while it is open.
func promptHint(buttons bool) string {
	if buttons {
		return "<i>Tap a button — or send /approve, /deny or /answer &lt;n&gt;.</i>"
	}
	return "<i>Send /approve, /deny or /answer &lt;n&gt;.</i>"
}

// promptOptionLines renders the numbered option list.
func promptOptionLines(p *SessionPrompt) string {
	lines := make([]string, 0, len(p.Options))
	for i, opt := range p.Options {
		line := fmt.Sprintf("%d. <b>%s</b>", i+1, EscapeHTML(opt))
		if i < len(p.Details) && strings.TrimSpace(p.Details[i]) != "" {
			line += " — " + EscapeHTML(strings.TrimSpace(p.Details[i]))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// promptKeyboard builds one button per option. It returns nil when the
// callback data would not fit the Bot API's 64-byte cap (a project id
// far longer than a UUID) — the text commands still answer the prompt.
func promptKeyboard(projectID, instance string, p *SessionPrompt) [][]telegrambot.InlineKeyboardButton {
	keyboard := make([][]telegrambot.InlineKeyboardButton, 0, len(p.Options))
	for i, opt := range p.Options {
		if i >= promptMaxOptions {
			break
		}
		data := fmt.Sprintf("%s%s:%s:%d", callbackAnswerPrefix, projectID, instance, i+1)
		if len(data) > maxCallbackData {
			return nil
		}
		keyboard = append(keyboard, []telegrambot.InlineKeyboardButton{
			{Text: fmt.Sprintf("%d. %s", i+1, truncRunes(opt, promptButtonRunes)), CallbackData: data},
		})
	}
	return keyboard
}

// promptPiece is one block of a forwarded prompt and the separator that
// joins it to the block before it when both share a message.
type promptPiece struct {
	sep  string
	text string
}

// promptMessages lays a prompt out as Telegram HTML messages, each
// within the 4096-char cap: header, the prompt text, then the options
// and the hint. A prompt of more than two lines (a command, a diff, a
// plan) is rendered as <pre> — chunked on the body like the relay's
// tool blocks, so a long one spans messages with balanced tags instead
// of being cut. The LAST message always ends with the options + hint;
// it is the one that carries the keyboard. body is that message's text
// without the hint (what stays when the prompt is closed out).
func promptMessages(head string, p *SessionPrompt, buttons bool) (msgs []string, body string) {
	text := strings.Trim(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n")
	pieces := []promptPiece{{text: head}}
	switch {
	case strings.TrimSpace(text) == "":
	case strings.Count(text, "\n") >= 2:
		for _, c := range preChunks(text) {
			pieces = append(pieces, promptPiece{sep: "\n", text: c})
		}
	default:
		for _, c := range chunkLines(EscapeHTML(text)) {
			pieces = append(pieces, promptPiece{sep: "\n\n", text: c})
		}
	}
	options := promptOptionLines(p)
	hint := promptHint(buttons)
	tail := chunkLines(options + "\n\n" + hint)
	for _, c := range tail {
		pieces = append(pieces, promptPiece{sep: "\n\n", text: c})
	}

	var cur strings.Builder
	for _, pc := range pieces {
		if cur.Len() > 0 && cur.Len()+len(pc.sep)+len(pc.text) > maxMessageLen {
			msgs = append(msgs, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString(pc.sep)
		}
		cur.WriteString(pc.text)
	}
	msgs = append(msgs, cur.String())
	body = strings.TrimSuffix(msgs[len(msgs)-1], "\n\n"+hint)
	return msgs, body
}

// ---------------------------------------------------------------------------
// Forwarding

// reconcilePrompts brings every chat's forwarded prompts in line with
// the dialogs actually on screen: a forwarded prompt whose dialog is
// gone is closed out (edited to the verdict, keyboard dropped), and a
// dialog the chat has not been shown yet is posted — once per instance.
// Runs on the watch-loop tick and right after an answer.
func (b *Bridge) reconcilePrompts(ctx context.Context) {
	if b.sessions == nil {
		return
	}
	b.promptSyncMu.Lock()
	defer b.promptSyncMu.Unlock()

	// 1. Close out posts whose dialog left the screen — for every chat
	//    that has one, muted or not (an edit makes no noise).
	b.promptMu.Lock()
	open := make(map[int64][]*promptPost, len(b.promptPosts))
	for chatID, posts := range b.promptPosts {
		open[chatID] = append([]*promptPost(nil), posts...)
	}
	b.promptMu.Unlock()
	for chatID, posts := range open {
		for _, post := range posts {
			if b.promptLive(post.projectID, post.instance) {
				continue
			}
			b.closePrompt(ctx, chatID, post)
		}
	}

	// 2. Post dialogs the chat has not seen. The target is the session
	//    watch mode would stream — the chat's /use selection, or the
	//    auto-attached live session — whether or not watch is on.
	type target struct {
		chatID    int64
		projectID string
	}
	b.mu.Lock()
	targets := make([]target, 0, len(b.paired))
	for chatID, chat := range b.paired {
		if chat.Muted {
			continue
		}
		targets = append(targets, target{chatID, chat.DefaultProjectID})
	}
	b.mu.Unlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].chatID < targets[j].chatID })
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		sess, live := b.resolveWatchSession(t.projectID)
		if !live {
			continue
		}
		p := sessionPrompt(sess)
		if p == nil {
			continue
		}
		instance := promptInstance(sess, p)
		if b.promptPosted(t.chatID, sess.ProjectID, instance) {
			continue
		}
		b.postPrompt(ctx, t.chatID, sess, p, instance)
	}

	// 3. Forget old answers.
	b.promptMu.Lock()
	for key, a := range b.promptAnswers {
		if time.Since(a.at) > promptAnswerTTL {
			delete(b.promptAnswers, key)
		}
	}
	b.promptMu.Unlock()
}

// promptLive reports whether the dialog instance is still on screen.
func (b *Bridge) promptLive(projectID, instance string) bool {
	sess, live := b.runningSession(projectID)
	if !live {
		return false
	}
	p := sessionPrompt(sess)
	return p != nil && promptInstance(sess, p) == instance
}

// promptPosted reports whether the chat was already shown this instance.
func (b *Bridge) promptPosted(chatID int64, projectID, instance string) bool {
	b.promptMu.Lock()
	defer b.promptMu.Unlock()
	for _, post := range b.promptPosts[chatID] {
		if post.projectID == projectID && post.instance == instance {
			return true
		}
	}
	return false
}

// postPrompt forwards one dialog to one chat and records the post.
func (b *Bridge) postPrompt(ctx context.Context, chatID int64, sess *WatchedSession, p *SessionPrompt, instance string) {
	keyboard := promptKeyboard(sess.ProjectID, instance, p)
	head := promptHeader(sess, p)
	msgs, body := promptMessages(head, p, len(keyboard) > 0)

	// The prompt is a bridge-authored message: whatever the relay still
	// has queued goes out first (so the tool call reads above the
	// question it led to), and the relay's growth anchor is dropped so
	// the agent's next output starts a new bubble BELOW the prompt.
	b.settleRelayBefore(ctx, chatID)

	post := &promptPost{projectID: sess.ProjectID, instance: instance, head: head, body: body}
	delivered := false
	for i, m := range msgs {
		var kb [][]telegrambot.InlineKeyboardButton
		if i == len(msgs)-1 {
			kb = keyboard
		}
		id, err := b.client.SendMessageWithKeyboard(ctx, b.token, chatID, m, kb)
		if err != nil {
			b.logger.Printf("WARN: telegram bridge: forwarding approval prompt to %d failed: %v", chatID, err)
			break
		}
		delivered = true
		if i == len(msgs)-1 {
			post.msgID = id
		}
	}
	if !delivered {
		return // nothing reached the chat — the next tick retries
	}
	b.promptMu.Lock()
	b.promptPosts[chatID] = append(b.promptPosts[chatID], post)
	b.promptMu.Unlock()
}

// closePrompt edits a forwarded prompt to its verdict — who answered and
// what, or that it was dealt with elsewhere — which also removes the
// inline keyboard, and stops tracking it.
func (b *Bridge) closePrompt(ctx context.Context, chatID int64, post *promptPost) {
	b.promptMu.Lock()
	posts := b.promptPosts[chatID]
	kept := posts[:0]
	for _, other := range posts {
		if other != post {
			kept = append(kept, other)
		}
	}
	if len(kept) == 0 {
		delete(b.promptPosts, chatID)
	} else {
		b.promptPosts[chatID] = kept
	}
	if arm, ok := b.promptArmed[chatID]; ok && arm.projectID == post.projectID && arm.instance == post.instance {
		delete(b.promptArmed, chatID)
	}
	answer, answered := b.promptAnswers[promptKey(post.projectID, post.instance)]
	b.promptMu.Unlock()

	if post.msgID == 0 {
		return
	}
	verdict := "☑ <i>No longer waiting — answered or dismissed somewhere else.</i>"
	if answered {
		verdict = "✅ <b>" + EscapeHTML(answer.label) + "</b> — answered by " + EscapeHTML(answer.who)
	}
	text := post.body + "\n\n" + verdict
	if len(text) > maxMessageLen {
		text = post.head + "\n\n" + verdict
	}
	if err := b.client.EditMessageText(ctx, b.token, chatID, post.msgID, text); err != nil {
		b.logger.Printf("WARN: telegram bridge: closing out approval prompt in %d failed: %v", chatID, err)
	}
}

// ---------------------------------------------------------------------------
// Answering

type answerOutcome int

const (
	// answerDone: the key was typed and the dialog left the screen.
	answerDone answerOutcome = iota
	// answerStale: the dialog is not on screen (any more) — nothing typed.
	answerStale
	// answerBadOption: no such option, or text given to an option that
	// takes none — nothing typed.
	answerBadOption
	// answerNeedsText: the option is a free-text one and no text came
	// with it — nothing typed; the caller arms the chat for the text.
	answerNeedsText
	// answerStillUp: the key was typed but the dialog is still on screen.
	answerStillUp
	// answerFailed: the PTY write failed.
	answerFailed
)

// answerResult is what answerPrompt did.
type answerResult struct {
	outcome   answerOutcome
	projectID string
	instance  string
	option    int
	label     string // the chosen option, as shown in verdicts
	detail    string // user-facing explanation (bad option)
	err       error
}

// answerPrompt answers the dialog projectID's session is parked on.
// instance pins the answer to one dialog instance ("" = whichever is on
// screen now); option is 1-based, or promptCancelOption for Esc; text is
// the typed answer for a free-text option. It types through injectSay
// and then verifies the dialog actually left the screen.
func (b *Bridge) answerPrompt(ctx context.Context, projectID, instance string, option int, text, who string) answerResult {
	res := answerResult{projectID: projectID, instance: instance, option: option}
	sess, live := b.runningSession(projectID)
	p := sessionPrompt(sess)
	if !live || p == nil || b.runner == nil {
		res.outcome = answerStale
		return res
	}
	current := promptInstance(sess, p)
	if instance != "" && instance != current {
		res.outcome = answerStale
		return res
	}
	res.instance = current

	text = strings.Join(strings.Fields(text), " ") // one line: a newline would submit early
	wantsText := false
	switch {
	case option == promptCancelOption:
		res.label = "Declined (Esc)"
		if text != "" {
			res.outcome, res.detail = answerBadOption, "Declining takes no text."
			return res
		}
	case option < 1 || option > len(p.Options) || option > promptMaxOptions:
		res.outcome = answerBadOption
		res.detail = fmt.Sprintf("There is no option %d — this prompt has %d.", option, min(len(p.Options), promptMaxOptions))
		return res
	default:
		res.label = p.Options[option-1]
		wantsText = promptOptionWantsText(res.label)
		switch {
		case wantsText && text == "":
			res.outcome = answerNeedsText
			return res
		case !wantsText && text != "":
			res.outcome = answerBadOption
			res.detail = fmt.Sprintf("Option %d (%s) takes no text — send /answer %d on its own.", option, res.label, option)
			return res
		case wantsText:
			res.label = truncRunes(text, 120)
		}
	}

	// Record the answer BEFORE typing: the watch loop may notice the
	// dialog gone before this function returns, and must close the
	// forwarded messages with this verdict rather than "elsewhere".
	key := promptKey(projectID, current)
	b.promptMu.Lock()
	b.promptAnswers[key] = promptAnswer{label: res.label, who: who, at: time.Now()}
	b.promptMu.Unlock()
	unrecord := func() {
		b.promptMu.Lock()
		delete(b.promptAnswers, key)
		b.promptMu.Unlock()
	}

	keystroke := promptEscKey
	if option != promptCancelOption {
		keystroke = strconv.Itoa(option)
	}
	err := b.injectSay(projectID, keystroke, sayKeystroke)
	if err == nil && wantsText {
		// The digit only focused the text field; type the answer and
		// submit it. A beat first, so the CLI has taken the focus change.
		b.sleepFn(ctx, b.sayEnterDelay)
		err = b.injectSay(projectID, text, sayLine)
	}
	if err != nil {
		unrecord()
		res.outcome, res.err = answerFailed, err
		return res
	}

	deadline := time.Now().Add(b.promptVerifyWait)
	for {
		if !b.promptLive(projectID, current) {
			res.outcome = answerDone
			return res
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			break
		}
		b.sleepFn(ctx, b.promptVerifyPoll)
	}
	unrecord()
	res.outcome = answerStillUp
	return res
}

// answerSummary is the one-line plain-text outcome — the callback toast,
// and (escaped) the body of the chat reply.
func answerSummary(res answerResult) string {
	switch res.outcome {
	case answerDone:
		return "Answered: " + truncRunes(res.label, 150)
	case answerStale:
		return promptStaleNotice
	case answerBadOption:
		return res.detail
	case answerNeedsText:
		return "Send your answer as a message."
	case answerStillUp:
		return "Sent, but the prompt is still on screen — /screen to check."
	default:
		return "Failed to send the answer: " + res.err.Error()
	}
}

// finishAnswer does the bookkeeping after answerPrompt on behalf of a
// chat: closes out forwarded messages on success, arms the chat when
// the option needs typed text, and — when reply is set (the text
// commands; a button tap gets a toast instead) — posts the outcome.
func (b *Bridge) finishAnswer(ctx context.Context, chatID int64, res answerResult, reply bool) {
	switch res.outcome {
	case answerDone:
		b.reconcilePrompts(ctx)
		if reply {
			b.reply(ctx, chatID, "✅ Answered: <b>"+EscapeHTML(res.label)+"</b>")
		}
	case answerNeedsText:
		b.promptMu.Lock()
		b.promptArmed[chatID] = promptArm{projectID: res.projectID, instance: res.instance, option: res.option}
		b.promptMu.Unlock()
		b.reply(ctx, chatID, fmt.Sprintf("✍️ Option %d (<b>%s</b>) takes a typed answer — send it as your next message and I'll type it into the prompt. Send <code>cancel</code> to back out.",
			res.option, EscapeHTML(res.label)))
	default:
		if res.outcome == answerStale {
			// The dialog is gone; make sure its message says so.
			b.reconcilePrompts(ctx)
		}
		if reply {
			b.reply(ctx, chatID, EscapeHTML(answerSummary(res)))
		}
	}
}

// handleAnswerCallback services a tap on a prompt's option button.
func (b *Bridge) handleAnswerCallback(ctx context.Context, cq *telegrambot.CallbackQuery, answer func(string)) {
	chatID := cq.Message.Chat.ID
	projectID, instance, option, ok := parseAnswerCallback(cq.Data)
	if !ok {
		answer("")
		return
	}
	res := b.answerPrompt(ctx, projectID, instance, option, "", userLabel(cq.From.Username, cq.From.ID))
	answer(truncRunes(answerSummary(res), 190)) // toasts cap at 200 chars
	b.finishAnswer(ctx, chatID, res, false)
}

// parseAnswerCallback splits "ans:<project>:<instance>:<option>". It
// reads from the right, so a project id containing ':' still parses.
func parseAnswerCallback(data string) (projectID, instance string, option int, ok bool) {
	rest := strings.TrimPrefix(data, callbackAnswerPrefix)
	last := strings.LastIndexByte(rest, ':')
	if last <= 0 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(rest[last+1:])
	if err != nil || n < 1 || n > promptMaxOptions {
		return "", "", 0, false
	}
	rest = rest[:last]
	mid := strings.LastIndexByte(rest, ':')
	if mid <= 0 || mid == len(rest)-1 {
		return "", "", 0, false
	}
	return rest[:mid], rest[mid+1:], n, true
}

// waitingPrompt resolves the dialog the chat's session is parked on —
// the same session watch mode streams — replying when nothing waits.
func (b *Bridge) waitingPrompt(ctx context.Context, chatID int64) (*WatchedSession, *SessionPrompt, bool) {
	if !b.requireRunner(ctx, chatID) {
		return nil, nil, false
	}
	if b.sessions == nil {
		b.reply(ctx, chatID, "Sessions are not wired up on this daemon.")
		return nil, nil, false
	}
	b.mu.Lock()
	projectID := b.paired[chatID].DefaultProjectID
	b.mu.Unlock()
	sess, live := b.resolveWatchSession(projectID)
	if !live {
		b.reply(ctx, chatID, "No agent is running — nothing is waiting for an answer.")
		return nil, nil, false
	}
	p := sessionPrompt(sess)
	if p == nil {
		b.reply(ctx, chatID, "Nothing is waiting for an answer right now — /screen shows where the agent is.")
		return nil, nil, false
	}
	return sess, p, true
}

// cmdApprove answers a yes/no approval with its first option ("Yes").
// It refuses on an open question, where "option 1" is not an approval.
func (b *Bridge) cmdApprove(ctx context.Context, chatID int64, who string) {
	sess, p, ok := b.waitingPrompt(ctx, chatID)
	if !ok {
		return
	}
	if !promptIsApproval(p) {
		b.reply(ctx, chatID, "That prompt is a question, not a yes/no approval — pick an option with /answer &lt;n&gt;:\n"+promptOptionLines(p))
		return
	}
	b.finishAnswer(ctx, chatID, b.answerPrompt(ctx, sess.ProjectID, promptInstance(sess, p), 1, "", who), true)
}

// cmdDeny answers with the dialog's "No" option, or cancels it with Esc
// when it has none (plan approvals, questions).
func (b *Bridge) cmdDeny(ctx context.Context, chatID int64, who string) {
	sess, p, ok := b.waitingPrompt(ctx, chatID)
	if !ok {
		return
	}
	b.finishAnswer(ctx, chatID, b.answerPrompt(ctx, sess.ProjectID, promptInstance(sess, p), promptDenyOption(p), "", who), true)
}

// cmdAnswer picks option <n>; trailing text is the typed answer for a
// free-text option ("/answer 3 use teal instead").
func (b *Bridge) cmdAnswer(ctx context.Context, chatID int64, rest, who string) {
	fields := strings.Fields(rest)
	n := 0
	if len(fields) > 0 {
		n, _ = strconv.Atoi(strings.TrimSuffix(fields[0], "."))
	}
	if n < 1 {
		b.reply(ctx, chatID, "Usage: /answer &lt;n&gt; [text] — the option number from the prompt; text only for a \"type something\" option.")
		return
	}
	sess, p, ok := b.waitingPrompt(ctx, chatID)
	if !ok {
		return
	}
	text := strings.Join(fields[1:], " ")
	b.finishAnswer(ctx, chatID, b.answerPrompt(ctx, sess.ProjectID, promptInstance(sess, p), n, text, who), true)
}

// handlePromptText intercepts a plain-text message while a dialog is
// waiting. Plain text is normally typed into a chat session followed by
// Enter — on a dialog that Enter would accept the highlighted option, so
// it must never go through unexamined. Returns false when no dialog is
// involved (the caller treats the text as ordinary conversation).
//
//   - chat armed by a free-text option → the message is that answer;
//   - open question with a free-text option ("Type something.") → the
//     message is typed into it (replying in words to a question is the
//     natural thing to do);
//   - any other dialog (every yes/no approval) → refused, pointing at
//     the buttons.
func (b *Bridge) handlePromptText(ctx context.Context, chatID int64, text string) bool {
	b.mu.Lock()
	chat := b.paired[chatID]
	b.mu.Unlock()
	who := userLabel(chat.Username, chat.UserID)

	b.promptMu.Lock()
	arm, armed := b.promptArmed[chatID]
	delete(b.promptArmed, chatID)
	b.promptMu.Unlock()
	if armed {
		if strings.EqualFold(strings.TrimSpace(text), "cancel") {
			b.reply(ctx, chatID, "Okay — I typed nothing. The prompt is still waiting.")
			return true
		}
		b.finishAnswer(ctx, chatID, b.answerPrompt(ctx, arm.projectID, arm.instance, arm.option, text, who), true)
		return true
	}

	sess, live := b.resolveWatchSession(chat.DefaultProjectID)
	if !live {
		return false
	}
	p := sessionPrompt(sess)
	if p == nil {
		return false
	}
	// Only for open questions: on a yes/no approval a typed "yes" would
	// land in the "tell Claude what to change" field as feedback.
	if n := promptTextOption(p); n > 0 && !promptIsApproval(p) && strings.TrimSpace(text) != "" {
		b.finishAnswer(ctx, chatID, b.answerPrompt(ctx, sess.ProjectID, promptInstance(sess, p), n, text, who), true)
		return true
	}
	b.reply(ctx, chatID, "⏸ The agent is waiting on a prompt, so I didn't type that into it. Tap a button on the prompt, or send /approve, /deny or /answer &lt;n&gt;.")
	return true
}
