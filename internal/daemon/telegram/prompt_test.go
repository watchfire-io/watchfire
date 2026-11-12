package telegram

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// dialogSession scripts a Claude Code approval / question dialog as the
// bridge types into it, mirroring the keys captured live from Claude
// Code v2.1.322 (2026-11-12):
//
//   - a digit selects AND submits that option at once (dialog gone);
//   - Esc cancels the dialog at once;
//   - the digit of a free-text option ("Type something.") only focuses
//     its text field — text is then typed and Enter submits it;
//   - a bare Enter accepts the highlighted FIRST option. The bridge must
//     never send one at a dialog; and any input arriving when no dialog
//     is on screen is recorded as stray — an Enter trailing a digit
//     would show up there (or, worse, answer the next dialog).
type dialogSession struct {
	mu      sync.Mutex
	prompt  *SessionPrompt   // on screen now (nil = none)
	queue   []*SessionPrompt // shown, in order, as each one is answered
	focused int              // free-text option holding focus (0 = none)
	typed   string
	answers []string // what each dialog registered
	stray   []string // input received with no dialog on screen
}

func (d *dialogSession) current() *SessionPrompt {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.prompt
}

func (d *dialogSession) show(p *SessionPrompt) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prompt, d.focused, d.typed = p, 0, ""
}

func (d *dialogSession) state() (answers, stray []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.answers...), append([]string(nil), d.stray...)
}

func (d *dialogSession) dismissLocked(answer string) {
	d.answers = append(d.answers, answer)
	d.prompt, d.focused, d.typed = nil, 0, ""
	if len(d.queue) > 0 {
		d.prompt, d.queue = d.queue[0], d.queue[1:]
	}
}

func (d *dialogSession) onInput(chunk string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.prompt == nil {
		d.stray = append(d.stray, chunk)
		return
	}
	switch {
	case d.focused > 0 && chunk == "\r":
		d.dismissLocked("typed:" + d.typed)
	case d.focused > 0:
		d.typed += chunk
	case chunk == "\x1b":
		d.dismissLocked("esc")
	case chunk == "\r":
		d.dismissLocked("enter:" + d.prompt.Options[0])
	case len(chunk) == 1 && chunk[0] >= '1' && chunk[0] <= '9':
		n := int(chunk[0] - '0')
		if n > len(d.prompt.Options) {
			return
		}
		if promptOptionWantsText(d.prompt.Options[n-1]) {
			d.focused = n
			return
		}
		d.dismissLocked(d.prompt.Options[n-1])
	}
}

const bashCommandUnderApproval = "for f in alpha beta; do\n  echo \"line $f\" >> capture-note.txt\ndone"

func bashPrompt(at int64) *SessionPrompt {
	return &SessionPrompt{
		Text: "Bash command\nAppend two lines to capture-note.txt\n" + bashCommandUnderApproval + "\nDo you want to proceed?",
		Options: []string{
			"Yes",
			"Yes, and switch to auto mode · auto mode handles these prompts for you",
			"No",
		},
		DetectedAt: time.Unix(at, 0),
	}
}

func questionPrompt(at int64) *SessionPrompt {
	return &SessionPrompt{
		Text:       "Accent color\nWhich accent color should the dashboard use?",
		Options:    []string{"Crimson", "Cobalt", "Type something.", "Chat about this"},
		Details:    []string{"warm, high contrast", "cool, calmer", "", ""},
		DetectedAt: time.Unix(at, 0),
	}
}

func planPrompt(at int64) *SessionPrompt {
	return &SessionPrompt{
		Text: "Ready to code?\nHere is Claude's plan:\n1. Append hello to README.md\nClaude has written up a plan and is ready to execute. Would you like to proceed?",
		Options: []string{
			"Yes, and use auto mode",
			"Yes, manually approve edits",
			"Tell Claude what to change shift+tab to approve with this feedback",
		},
		DetectedAt: time.Unix(at, 0),
	}
}

// promptFixture is a bridge over one live session ("p1") whose dialog is
// scripted by dlg, with a recording runner.
type promptFixture struct {
	fake   *fakeBotAPI
	b      *Bridge
	dlg    *dialogSession
	runner *stubRunner
	sess   *WatchedSession
}

func newPromptFixture(t *testing.T, mode string, chats []models.TelegramPairedChat, first *SessionPrompt, script ...string) *promptFixture {
	t.Helper()
	withTestEnv(t)
	fx := &promptFixture{dlg: &dialogSession{prompt: first}, runner: &stubRunner{}}
	fx.fake = newFakeBotAPI(t, script...)
	fx.runner.onInput = fx.dlg.onInput
	fx.sess = &WatchedSession{
		ProjectID: "p1", ProjectPath: "/src/watchfire", Mode: mode,
		StartedAt: time.Unix(500, 0), Done: make(chan struct{}),
		Prompt: fx.dlg.current,
	}
	if mode == "task" {
		fx.sess.TaskNumber, fx.sess.TaskTitle = 7, "Relay <prompts>"
	}
	sessions := &fakeSessions{sess: map[string]*WatchedSession{"p1": fx.sess}}
	fx.b = runControlBridge(fx.runner, sessions, chats)
	speedUp(fx.b)
	fx.b.promptVerifyPoll = time.Millisecond
	fx.b.promptVerifyWait = 300 * time.Millisecond
	// No transcript in these tests — keep the relay off the real
	// ~/.claude lookup.
	fx.b.tailerForFn = func(*WatchedSession) (TailableTranscript, bool) { return nil, false }
	return fx
}

func (fx *promptFixture) push(script string) {
	fx.fake.mu.Lock()
	fx.fake.script = append(fx.fake.script, script)
	fx.fake.mu.Unlock()
}

// promptsSent returns the forwarded prompt messages (the ones carrying
// the "Waiting for your …" header), optionally for one chat.
func (fx *promptFixture) promptsSent(chatID string) []sentMessage {
	var out []sentMessage
	for _, m := range fx.fake.sentMessages() {
		if strings.Contains(m.Text, "Waiting for your") && (chatID == "" || m.ChatID == chatID) {
			out = append(out, m)
		}
	}
	return out
}

func (fx *promptFixture) inputs() []string {
	_, _, in := fx.runner.snapshot()
	return in
}

func (fx *promptFixture) instance(p *SessionPrompt) string { return promptInstance(fx.sess, p) }

func (fx *promptFixture) editContaining(needle string) (editedMessage, bool) {
	for _, e := range fx.fake.editedMessages() {
		if strings.Contains(e.Text, needle) {
			return e, true
		}
	}
	return editedMessage{}, false
}

// TestPromptForwardedOncePerDialog: a watched session that stops on an
// approval dialog produces exactly ONE message — header, the whole
// multi-line command in a <pre> block, the options — with an inline
// button per option; the same dialog staying on screen is not re-posted;
// a new dialog is posted again and the old message is closed out.
func TestPromptForwardedOncePerDialog(t *testing.T) {
	first := bashPrompt(1000)
	fx := newPromptFixture(t, "task", chatOnProject("p1"), first)
	startBridge(t, fx.b)

	waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
	time.Sleep(80 * time.Millisecond) // many reconcile ticks at 5ms
	posts := fx.promptsSent("")
	if len(posts) != 1 {
		t.Fatalf("the dialog must be forwarded exactly once while it stays on screen, got %d: %+v", len(posts), posts)
	}
	msg := posts[0]
	if msg.ChatID != "42" {
		t.Fatalf("prompt went to chat %s", msg.ChatID)
	}
	for _, want := range []string{
		"⏸ <b>Waiting for your approval</b>", "<b>watchfire</b>", "task #0007", "Relay &lt;prompts&gt;",
		"<pre>", "Do you want to proceed?",
		"1. <b>Yes</b>", "3. <b>No</b>", "/approve", "/answer &lt;n&gt;",
	} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("forwarded prompt missing %q:\n%s", want, msg.Text)
		}
	}
	// The command under approval arrives whole, newlines and indentation
	// intact, inside the monospace block.
	if !strings.Contains(unescapeHTML(msg.Text), bashCommandUnderApproval) {
		t.Errorf("multi-line command was not relayed intact:\n%s", msg.Text)
	}
	if !tagsBalanced(msg.Text) {
		t.Errorf("unbalanced HTML:\n%s", msg.Text)
	}

	// One button per option; callback data encodes (project, instance,
	// option index) and fits the Bot API's 64-byte cap.
	inst := fx.instance(first)
	for i := 1; i <= 3; i++ {
		data := "ans:p1:" + inst + ":" + string(rune('0'+i))
		if !strings.Contains(msg.ReplyMarkup, `"callback_data":"`+data+`"`) {
			t.Errorf("keyboard missing button %q: %s", data, msg.ReplyMarkup)
		}
		if len(data) > maxCallbackData {
			t.Errorf("callback data %q exceeds %d bytes", data, maxCallbackData)
		}
	}
	if strings.Count(msg.ReplyMarkup, "callback_data") != 3 {
		t.Errorf("expected exactly 3 buttons: %s", msg.ReplyMarkup)
	}
	if !strings.Contains(msg.ReplyMarkup, "1. Yes") || !strings.Contains(msg.ReplyMarkup, "3. No") {
		t.Errorf("buttons should be labeled with the options: %s", msg.ReplyMarkup)
	}

	// A NEW dialog (answered in the TUI, then another appears) is posted
	// again; the first message is closed out and loses its hint.
	fx.dlg.show(questionPrompt(2000))
	waitFor(t, "second dialog forwarded", func() bool { return len(fx.promptsSent("")) >= 2 })
	second := fx.promptsSent("")[1]
	if !strings.Contains(second.Text, "Waiting for your answer") || !strings.Contains(second.Text, "1. <b>Crimson</b> — warm, high contrast") {
		t.Errorf("question prompt rendering:\n%s", second.Text)
	}
	if strings.Count(second.ReplyMarkup, "callback_data") != 4 {
		t.Errorf("question keyboard: %s", second.ReplyMarkup)
	}
	waitFor(t, "first prompt closed out", func() bool {
		_, ok := fx.editContaining("No longer waiting")
		return ok
	})
	e, _ := fx.editContaining("No longer waiting")
	if e.MessageID != itoa(msg.MessageID) {
		t.Errorf("closed the wrong message: %+v (prompt was %d)", e, msg.MessageID)
	}
	if strings.Contains(e.Text, "Tap a button") || !strings.Contains(e.Text, "Do you want to proceed?") {
		t.Errorf("closed prompt should keep the question and drop the hint:\n%s", e.Text)
	}
	if in := fx.inputs(); len(in) != 0 {
		t.Fatalf("forwarding must never write to the PTY, got %q", in)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestPromptTapAnswersDialog: tapping a button types that option's
// digit — and nothing else — into the live session, the dialog goes
// away, and the forwarded message is edited to show the choice and who
// made it.
func TestPromptTapAnswersDialog(t *testing.T) {
	first := bashPrompt(1000)
	fx := newPromptFixture(t, "task", chatOnProject("p1"), first)
	// A second dialog is queued right behind the first (two parallel
	// tool calls): anything typed after the digit would answer IT.
	next := bashPrompt(2000)
	fx.dlg.queue = []*SessionPrompt{next}
	startBridge(t, fx.b)
	waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
	post := fx.promptsSent("")[0]

	fx.push(callbackJSON(10, 42, 42, "cb-ans", "ans:p1:"+fx.instance(first)+":3"))
	waitFor(t, "callback answered", func() bool { return len(fx.fake.answeredCallbacks()) >= 1 })
	if cb := fx.fake.answeredCallbacks()[0]; cb.ID != "cb-ans" || cb.Text != "Answered: No" {
		t.Fatalf("toast = %+v", cb)
	}
	waitFor(t, "message edited with the choice", func() bool {
		_, ok := fx.editContaining("answered by @u")
		return ok
	})
	e, _ := fx.editContaining("answered by @u")
	if e.MessageID != itoa(post.MessageID) || e.ChatID != "42" {
		t.Fatalf("edited %+v, want message %d in chat 42", e, post.MessageID)
	}
	if !strings.Contains(e.Text, "✅ <b>No</b>") || strings.Contains(e.Text, "Tap a button") {
		t.Fatalf("verdict edit:\n%s", e.Text)
	}
	// Exactly the digit. No Enter, nothing else.
	if in := fx.inputs(); len(in) != 1 || in[0] != "3" {
		t.Fatalf("a tap must type the option digit only, got %q", in)
	}
	answers, stray := fx.dlg.state()
	if len(answers) != 1 || answers[0] != "No" {
		t.Fatalf("dialog registered %q, want [No]", answers)
	}
	if len(stray) != 0 {
		t.Fatalf("stray input after the answer: %q", stray)
	}
	// The queued second dialog is untouched and forwarded as its own message.
	if fx.dlg.current() != next {
		t.Fatal("the next dialog was answered by leftover input")
	}
	waitFor(t, "next dialog forwarded", func() bool { return len(fx.promptsSent("")) >= 2 })
}

// TestPromptStaleTapRefused: a tap on a dialog that is no longer the one
// on screen — already answered, replaced by another, or its session
// gone — is acknowledged with "no longer waiting" and types nothing.
func TestPromptStaleTapRefused(t *testing.T) {
	first := bashPrompt(1000)
	fx := newPromptFixture(t, "task", chatOnProject("p1"), first)
	startBridge(t, fx.b)
	waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
	staleData := "ans:p1:" + fx.instance(first) + ":1"

	// The dialog is replaced by a different one (answered in the GUI).
	current := bashPrompt(2000) // identical wording, later instance
	fx.dlg.show(current)
	fx.push(callbackJSON(10, 42, 42, "cb-stale", staleData))
	waitFor(t, "stale tap answered", func() bool { return len(fx.fake.answeredCallbacks()) >= 1 })
	if cb := fx.fake.answeredCallbacks()[0]; cb.Text != promptStaleNotice {
		t.Fatalf("stale tap toast = %q", cb.Text)
	}

	// No dialog at all.
	fx.dlg.show(nil)
	fx.push(callbackJSON(11, 42, 42, "cb-gone", staleData))
	waitFor(t, "gone tap answered", func() bool { return len(fx.fake.answeredCallbacks()) >= 2 })
	if cb := fx.fake.answeredCallbacks()[1]; cb.Text != promptStaleNotice {
		t.Fatalf("gone tap toast = %q", cb.Text)
	}

	// Session gone, and a tap for a project that never had one.
	fx.b.sessions.(*fakeSessions).mu.Lock()
	delete(fx.b.sessions.(*fakeSessions).sess, "p1")
	fx.b.sessions.(*fakeSessions).mu.Unlock()
	fx.push(callbackJSON(12, 42, 42, "cb-nosess", staleData))
	fx.push(callbackJSON(13, 42, 42, "cb-junk", "ans:not-a-button"))
	waitFor(t, "all taps answered", func() bool { return len(fx.fake.answeredCallbacks()) >= 4 })
	if cb := fx.fake.answeredCallbacks()[2]; cb.Text != promptStaleNotice {
		t.Fatalf("no-session tap toast = %q", cb.Text)
	}

	if in := fx.inputs(); len(in) != 0 {
		t.Fatalf("a stale tap must not write to the PTY, got %q", in)
	}
	if answers, _ := fx.dlg.state(); len(answers) != 0 {
		t.Fatalf("a stale tap answered a dialog: %q", answers)
	}
	if _, ok := fx.editContaining("answered by"); ok {
		t.Fatal("a stale tap must not mark anything as answered")
	}
}

// TestPromptReachesWatchOffButNotMuted: an approval request is not
// chatter — a chat with /watch off still gets it (with buttons), both
// with the project selected and auto-attached; a muted chat does not.
func TestPromptReachesWatchOffButNotMuted(t *testing.T) {
	chats := []models.TelegramPairedChat{
		{ChatID: 42, UserID: 42, Username: "nuno", PairedAt: time.Now(), DefaultProjectID: "p1", WatchOff: true},
		{ChatID: 43, UserID: 43, Username: "muted", PairedAt: time.Now(), DefaultProjectID: "p1", WatchOff: true, Muted: true},
		{ChatID: 44, UserID: 44, Username: "auto", PairedAt: time.Now(), WatchOff: true}, // no /use → auto-attach
	}
	first := bashPrompt(1000)
	fx := newPromptFixture(t, "task", chats, first)
	startBridge(t, fx.b)

	waitFor(t, "watch-off chats get the prompt", func() bool {
		return len(fx.promptsSent("42")) == 1 && len(fx.promptsSent("44")) == 1
	})
	time.Sleep(80 * time.Millisecond)
	if got := fx.promptsSent("43"); len(got) != 0 {
		t.Fatalf("a muted chat must stay silent, got %+v", got)
	}
	for _, m := range fx.fake.sentMessages() {
		if m.ChatID == "43" {
			t.Fatalf("muted chat received %q", m.Text)
		}
	}
	for _, chat := range []string{"42", "44"} {
		posts := fx.promptsSent(chat)
		if len(posts) != 1 || strings.Count(posts[0].ReplyMarkup, "callback_data") != 3 {
			t.Fatalf("chat %s: want one prompt with 3 buttons, got %+v", chat, posts)
		}
	}
	// Watch really is off: no relay ran, so no start marker was sent.
	if sentContaining(fx.fake, "▶ task 0007") {
		t.Fatal("a watch-off chat must not get the relay's start marker")
	}

	// Answered from chat 42 → the copy in chat 44 closes with the same verdict.
	fx.push(callbackJSON(10, 42, 42, "cb", "ans:p1:"+fx.instance(first)+":1"))
	waitFor(t, "both copies closed", func() bool {
		n := 0
		for _, e := range fx.fake.editedMessages() {
			if strings.Contains(e.Text, "✅ <b>Yes</b> — answered by @u") {
				n++
			}
		}
		return n == 2
	})
	if in := fx.inputs(); len(in) != 1 || in[0] != "1" {
		t.Fatalf("inputs = %q", in)
	}
}

// TestPromptTextCommands: /approve, /deny and /answer <n> are the typed
// equivalents of the buttons, each one keystroke with no Enter.
func TestPromptTextCommands(t *testing.T) {
	cases := []struct {
		name       string
		prompt     *SessionPrompt
		command    string
		wantInputs []string
		wantAnswer string // what the dialog registered ("" = still on screen)
		wantReply  string
	}{
		{"approve picks Yes", bashPrompt(1), "/approve", []string{"1"}, "Yes", "Answered: <b>Yes</b>"},
		{"deny picks No", bashPrompt(1), "/deny", []string{"3"}, "No", "Answered: <b>No</b>"},
		{"answer by number", bashPrompt(1), "/answer 2", []string{"2"}, bashPrompt(1).Options[1], "Answered: <b>Yes, and switch to auto mode"},
		{"deny on a plan has no No option → Esc", planPrompt(1), "/deny", []string{"\x1b"}, "esc", "Declined (Esc)"},
		{"deny on a question → Esc", questionPrompt(1), "/deny", []string{"\x1b"}, "esc", "Declined (Esc)"},
		{"approve refuses on a question", questionPrompt(1), "/approve", nil, "", "is a question, not a yes/no approval"},
		{"answer out of range", bashPrompt(1), "/answer 7", nil, "", "There is no option 7"},
		{"answer needs a number", bashPrompt(1), "/answer yes", nil, "", "Usage: /answer"},
		{"text on a plain option is refused", bashPrompt(1), "/answer 1 go ahead", nil, "", "takes no text"},
		{"free-text option with text", questionPrompt(1), "/answer 3 teal  please", []string{"3", "teal please", "\r"}, "typed:teal please", "Answered: <b>teal please</b>"},
		{"plan feedback", planPrompt(1), "/answer 3 use goodbye", []string{"3", "use goodbye", "\r"}, "typed:use goodbye", "Answered: <b>use goodbye</b>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newPromptFixture(t, "task", chatOnProject("p1"), tc.prompt)
			startBridge(t, fx.b)
			waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
			fx.push(updateJSON(10, 42, 42, "nuno", tc.command))
			waitFor(t, "reply "+tc.wantReply, func() bool { return sentContaining(fx.fake, tc.wantReply) })

			in := fx.inputs()
			if strings.Join(in, "|") != strings.Join(tc.wantInputs, "|") {
				t.Fatalf("PTY input = %q, want %q", in, tc.wantInputs)
			}
			answers, stray := fx.dlg.state()
			if len(stray) != 0 {
				t.Fatalf("stray input: %q", stray)
			}
			if tc.wantAnswer == "" {
				if len(answers) != 0 || fx.dlg.current() == nil {
					t.Fatalf("the dialog must be left untouched, registered %q", answers)
				}
				return
			}
			if len(answers) != 1 || answers[0] != tc.wantAnswer {
				t.Fatalf("dialog registered %q, want %q", answers, tc.wantAnswer)
			}
			waitFor(t, "forwarded message closed with the verdict", func() bool {
				_, ok := fx.editContaining("answered by @nuno")
				return ok
			})
		})
	}
}

// TestPromptCommandsWithNothingWaiting: the text commands say so instead
// of typing into a session that is not on a dialog.
func TestPromptCommandsWithNothingWaiting(t *testing.T) {
	fx := newPromptFixture(t, "task", chatOnProject("p1"), nil,
		updateJSON(1, 42, 42, "nuno", "/approve"),
		updateJSON(2, 42, 42, "nuno", "/deny"),
		updateJSON(3, 42, 42, "nuno", "/answer 1"),
	)
	startBridge(t, fx.b)
	waitFor(t, "three replies", func() bool {
		n := 0
		for _, m := range fx.fake.sentMessages() {
			if strings.Contains(m.Text, "Nothing is waiting for an answer") {
				n++
			}
		}
		return n == 3
	})
	if in := fx.inputs(); len(in) != 0 {
		t.Fatalf("nothing may be typed when no dialog is waiting, got %q", in)
	}
}

// TestPromptFreeTextArming: tapping (or /answer-ing) a "type something"
// option without text types nothing yet — it arms the chat, and the next
// plain message is delivered as digit → text → Enter. "cancel" disarms.
func TestPromptFreeTextArming(t *testing.T) {
	q := questionPrompt(1000)
	fx := newPromptFixture(t, "task", chatOnProject("p1"), q)
	startBridge(t, fx.b)
	waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })

	fx.push(callbackJSON(10, 42, 42, "cb-text", "ans:p1:"+fx.instance(q)+":3"))
	waitFor(t, "asked for the text", func() bool { return sentContaining(fx.fake, "takes a typed answer") })
	if in := fx.inputs(); len(in) != 0 {
		t.Fatalf("arming must not type yet, got %q", in)
	}
	// "cancel" backs out; the dialog is untouched.
	fx.push(updateJSON(11, 42, 42, "nuno", "cancel"))
	waitFor(t, "cancel acknowledged", func() bool { return sentContaining(fx.fake, "I typed nothing") })
	if in := fx.inputs(); len(in) != 0 || fx.dlg.current() != q {
		t.Fatalf("cancel must leave the dialog alone, inputs %q", in)
	}

	// Arm again via the command, then send the answer.
	fx.push(updateJSON(12, 42, 42, "nuno", "/answer 3"))
	waitFor(t, "asked again", func() bool {
		n := 0
		for _, m := range fx.fake.sentMessages() {
			if strings.Contains(m.Text, "takes a typed answer") {
				n++
			}
		}
		return n == 2
	})
	fx.push(updateJSON(13, 42, 42, "nuno", "a deep\nteal"))
	waitFor(t, "answer confirmed", func() bool { return sentContaining(fx.fake, "Answered: <b>a deep teal</b>") })
	if in := fx.inputs(); strings.Join(in, "|") != "3|a deep teal|\r" {
		t.Fatalf("free text must go digit → one-line text → Enter, got %q", in)
	}
	answers, stray := fx.dlg.state()
	if len(answers) != 1 || answers[0] != "typed:a deep teal" || len(stray) != 0 {
		t.Fatalf("dialog registered %q (stray %q)", answers, stray)
	}
}

// TestPlainTextNeverTypedAtADialog: plain text to a chat session is
// normally text + Enter — on an approval dialog that Enter would accept
// "Yes". With a dialog waiting the message is refused (approval) or
// delivered as the question's typed answer, never injected raw.
func TestPlainTextNeverTypedAtADialog(t *testing.T) {
	t.Run("approval dialog refuses plain text", func(t *testing.T) {
		fx := newPromptFixture(t, "chat", chatOnProject("p1"), bashPrompt(1))
		startBridge(t, fx.b)
		waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
		fx.push(updateJSON(10, 42, 42, "nuno", "yes go ahead"))
		waitFor(t, "refusal", func() bool { return sentContaining(fx.fake, "waiting on a prompt, so I didn't type that") })
		if in := fx.inputs(); len(in) != 0 {
			t.Fatalf("plain text reached the dialog: %q", in)
		}
		if answers, _ := fx.dlg.state(); len(answers) != 0 {
			t.Fatalf("the dialog was answered by plain text: %q", answers)
		}
	})
	t.Run("question takes plain text as the typed answer", func(t *testing.T) {
		fx := newPromptFixture(t, "chat", chatOnProject("p1"), questionPrompt(1))
		startBridge(t, fx.b)
		waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
		fx.push(updateJSON(10, 42, 42, "nuno", "make it teal"))
		waitFor(t, "answer confirmed", func() bool { return sentContaining(fx.fake, "Answered: <b>make it teal</b>") })
		if in := fx.inputs(); strings.Join(in, "|") != "3|make it teal|\r" {
			t.Fatalf("inputs = %q", in)
		}
	})
}

// TestPromptAnswerStillOnScreen: when the key is typed but the dialog
// does not go away, the bridge says so instead of claiming success, and
// the forwarded message stays open for another try.
func TestPromptAnswerStillOnScreen(t *testing.T) {
	first := bashPrompt(1000)
	fx := newPromptFixture(t, "task", chatOnProject("p1"), first)
	fx.runner.onInput = nil // the session ignores the key
	fx.b.promptVerifyWait = 30 * time.Millisecond
	startBridge(t, fx.b)
	waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
	fx.push(updateJSON(10, 42, 42, "nuno", "/approve"))
	waitFor(t, "honest report", func() bool { return sentContaining(fx.fake, "still on screen") })
	time.Sleep(40 * time.Millisecond)
	if _, ok := fx.editContaining("answered by"); ok {
		t.Fatal("an unconfirmed answer must not be shown as answered")
	}
	if len(fx.promptsSent("")) != 1 {
		t.Fatal("the still-open dialog must not be re-posted")
	}
}

// TestPromptSendFailure: a PTY write error is reported and nothing is
// marked answered.
func TestPromptSendFailure(t *testing.T) {
	first := bashPrompt(1000)
	fx := newPromptFixture(t, "task", chatOnProject("p1"), first)
	fx.runner.inputErr = context.DeadlineExceeded
	fx.runner.onInput = nil
	startBridge(t, fx.b)
	waitFor(t, "prompt forwarded", func() bool { return len(fx.promptsSent("")) >= 1 })
	fx.push(callbackJSON(10, 42, 42, "cb", "ans:p1:"+fx.instance(first)+":1"))
	waitFor(t, "failure toast", func() bool { return len(fx.fake.answeredCallbacks()) >= 1 })
	if cb := fx.fake.answeredCallbacks()[0]; !strings.Contains(cb.Text, "Failed to send the answer") {
		t.Fatalf("toast = %q", cb.Text)
	}
	if _, ok := fx.editContaining("answered by"); ok {
		t.Fatal("a failed write must not be shown as answered")
	}
}

// TestPromptPostDropsRelayGrowthAnchor: the prompt is a bridge-authored
// message, so the relay must flush what it has queued (it reads above
// the prompt) and stop growing its previous bubble (the agent's next
// output must start BELOW the prompt) — the v10.1 edit-in-place rule.
func TestPromptPostDropsRelayGrowthAnchor(t *testing.T) {
	fx := newPromptFixture(t, "task", chatOnProject("p1"), nil)
	sender := fx.b.newChatSender(42)
	sender.Add(Emission{Kind: EmissionAssistantText, Text: "about to run a command"})
	sender.flush(context.Background(), true)
	sender.mu.Lock()
	grew := sender.growID
	sender.mu.Unlock()
	if grew == 0 {
		t.Fatal("precondition: the relay should be growing a message")
	}
	sender.Add(Emission{Kind: EmissionToolUse, Text: "⚒ Bash queued"})
	fx.b.watchMu.Lock()
	fx.b.relays[42] = &chatRelay{key: "k", cancel: func() {}, done: make(chan struct{}), sender: sender}
	fx.b.watchMu.Unlock()

	p := bashPrompt(1)
	fx.b.postPrompt(context.Background(), 42, fx.sess, p, fx.instance(p))

	sender.mu.Lock()
	grow, pending := sender.growID, len(sender.pending)
	sender.mu.Unlock()
	if grow != 0 || pending != 0 {
		t.Fatalf("after a prompt post the relay must have flushed and dropped its anchor (growID=%d pending=%d)", grow, pending)
	}
	if q, pr := sentOrderIndex(fx.fake, "Bash queued"), sentOrderIndex(fx.fake, "Waiting for your approval"); q < 0 || pr < 0 || q > pr {
		t.Fatalf("queued relay output must land above the prompt (queued=%d prompt=%d)", q, pr)
	}
}

// TestPromptMessagesLongPrompt: a prompt too long for one message (a big
// diff on a tall terminal) is split with balanced tags, nothing dropped,
// and the options + hint ride the LAST message — the one with the
// keyboard.
func TestPromptMessagesLongPrompt(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, "+ line <"+strings.Repeat("x", 20)+"> & more "+itoa(int64(i)))
	}
	p := &SessionPrompt{Text: strings.Join(lines, "\n") + "\nDo you want to make this edit?", Options: []string{"Yes", "No"}}
	msgs, body := promptMessages("⏸ <b>head</b>", p, true)
	if len(msgs) < 2 {
		t.Fatalf("expected the prompt to span messages, got %d", len(msgs))
	}
	joined := make([]string, 0, len(msgs))
	for i, m := range msgs {
		if len(m) > maxMessageLen {
			t.Errorf("message %d is %d bytes, over the cap", i, len(m))
		}
		if !tagsBalanced(m) {
			t.Errorf("message %d has unbalanced tags", i)
		}
		// Each message holds one <pre> block of the prompt.
		open, end := strings.Index(m, "<pre>"), strings.Index(m, "</pre>")
		if open < 0 && i == len(msgs)-1 {
			continue // the options + hint did not fit beside the last block
		}
		if open < 0 || end < open || strings.Count(m, "<pre>") != 1 {
			t.Fatalf("message %d should carry one <pre> block", i)
		}
		joined = append(joined, unescapeHTML(m[open+len("<pre>"):end]))
	}
	if got := strings.Join(joined, "\n"); got != p.Text {
		t.Errorf("prompt text was altered across chunks (%d bytes vs %d)", len(got), len(p.Text))
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last, "1. <b>Yes</b>") || !strings.HasSuffix(last, promptHint(true)) {
		t.Errorf("the last message must carry the options and hint:\n%s", last[max(0, len(last)-300):])
	}
	if strings.Contains(body, "Tap a button") || !strings.Contains(body, "2. <b>No</b>") {
		t.Errorf("body should be the last message without the hint")
	}
	if !strings.HasPrefix(msgs[0], "⏸ <b>head</b>\n<pre>") {
		t.Errorf("the header must open the first message: %q", msgs[0][:60])
	}
}

// TestPromptHelpers pins the small classification rules.
func TestPromptHelpers(t *testing.T) {
	if !promptIsApproval(bashPrompt(1)) || !promptIsApproval(planPrompt(1)) || promptIsApproval(questionPrompt(1)) {
		t.Error("promptIsApproval misclassifies")
	}
	if promptDenyOption(bashPrompt(1)) != 3 || promptDenyOption(planPrompt(1)) != promptCancelOption || promptDenyOption(questionPrompt(1)) != promptCancelOption {
		t.Error("promptDenyOption misclassifies")
	}
	if promptTextOption(questionPrompt(1)) != 3 || promptTextOption(planPrompt(1)) != 3 || promptTextOption(bashPrompt(1)) != 0 {
		t.Error("promptTextOption misclassifies")
	}
	// A project id too long for the 64-byte callback cap: no keyboard,
	// the text commands remain.
	if kb := promptKeyboard(strings.Repeat("p", 60), "deadbeef", bashPrompt(1)); kb != nil {
		t.Errorf("expected no keyboard for an oversized callback, got %v", kb)
	}
	uuid := "0b0a2f0e-3c1d-4e5f-8a9b-1c2d3e4f5a6b"
	kb := promptKeyboard(uuid, "deadbeef", bashPrompt(1))
	if len(kb) != 3 || len(kb[2][0].CallbackData) > maxCallbackData {
		t.Errorf("a UUID project id must fit: %v", kb)
	}
	if pid, inst, n, ok := parseAnswerCallback(kb[2][0].CallbackData); !ok || pid != uuid || inst != "deadbeef" || n != 3 {
		t.Errorf("parseAnswerCallback round-trip = %q %q %d %v", pid, inst, n, ok)
	}
	for _, bad := range []string{"ans:", "ans:p1", "ans:p1:x", "ans:p1:inst:0", "ans:p1:inst:x", "ans::inst:1", "ans:p1::1"} {
		if _, _, _, ok := parseAnswerCallback(bad); ok {
			t.Errorf("parseAnswerCallback accepted %q", bad)
		}
	}
	// Same wording, different instance → different id (stale buttons).
	sess := &WatchedSession{ProjectID: "p1", StartedAt: time.Unix(5, 0)}
	if promptInstance(sess, bashPrompt(1)) == promptInstance(sess, bashPrompt(2)) {
		t.Error("two dialog instances with identical wording must not share an id")
	}
}

// TestInjectSayKinds pins the write shapes: a line is text, beat, Enter;
// a keystroke is the bytes alone.
func TestInjectSayKinds(t *testing.T) {
	runner := &stubRunner{}
	b := runControlBridge(runner, idleSessions(), chatOnProject("p1"))
	b.sayEnterDelay = time.Millisecond
	if err := b.injectSay("p1", "hello", sayLine); err != nil {
		t.Fatal(err)
	}
	if err := b.injectSay("p1", "2", sayKeystroke); err != nil {
		t.Fatal(err)
	}
	_, _, in := runner.snapshot()
	if strings.Join(in, "|") != "hello|\r|2" {
		t.Fatalf("writes = %q", in)
	}
}
