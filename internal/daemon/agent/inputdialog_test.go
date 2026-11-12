package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// Screen fixtures below are vt10x snapshots (80x24, rows as
// Process.snapshotScreen emits them) captured live from Claude Code
// v2.1.322 on 2026-11-11. Trailing padding is trimmed for readability —
// matching is whitespace-insensitive, so the detector sees the same thing.

// permissionBashScreen: a Bash tool-permission prompt for a multi-line
// command (--permission-mode default).
var permissionBashScreen = []string{
	"  Appending two lines to capture-note.txt",
	"  ⎿  $ for f in alpha beta; do",
	"     echo \"line $f\" >> capture-note.txt",
	"     done",
	"",
	"────────────────────────────────────────────────────────────────────────────────",
	" Bash command",
	" Tip: auto mode handles these prompts for you — choose \"switch to auto mode\"",
	" below",
	" Append two lines to capture-note.txt",
	"╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	" │ for f in alpha beta; do",
	" │   echo \"line $f\" >> capture-note.txt",
	" │ done",
	"╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	" A variable in this command can't be checked before it runs",
	"",
	" Do you want to proceed?",
	" ❯ 1. Yes",
	"   2. Yes, and switch to auto mode · auto mode handles these prompts for you",
	"   3. No",
	"",
	" Esc to cancel · Tab to amend",
	"────────────────────────────────────────────────────────────────────── wfcap-a ─",
}

// permissionEditScreen: an Edit tool-permission prompt whose option 2
// wraps onto a second row.
var permissionEditScreen = []string{
	"❯ Append the line \"hello from capture\" to README.md using the Edit tool, then",
	"  stop.",
	"",
	"⏺ I'll read README.md first, then append the line with Edit.",
	"",
	"  Read 1 file",
	"",
	"⏺ Update(README.md)",
	"",
	"────────────────────────────────────────────────────────────────────────────────",
	" Edit file",
	" README.md",
	"╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	" 1  # scratch",
	" 2 +hello from capture",
	"╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	" Do you want to make this edit to README.md?",
	" ❯ 1. Yes",
	"   2. Yes, and switch to accept edits (auto-approve file edits and common file",
	"      commands) for this session (shift+tab)",
	"   3. No",
	"",
	" Esc to cancel · Tab to amend",
	"───────────────────────────────────────────────────────────────────── wfcap-a ─",
}

// planApprovalScreen: the ExitPlanMode approval prompt (--permission-mode
// plan). The question wraps over two rows and a blank row precedes the
// options.
var planApprovalScreen = []string{
	"",
	"  ⎿  /plan to preview",
	"▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔",
	"",
	"  ────────────────────────────────────────────────────────────────────────────",
	"   Ready to code?",
	"",
	"   Here is Claude's plan:",
	"  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	"   Append the line \"Scratch project.\" to the end of README.md in the current",
	"   working directory, creating the file if it does not exist.",
	"  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	"  ────────────────────────────────────────────────────────────────────────────",
	"   Claude has written up a plan and is ready to execute. Would you like to",
	"   proceed?",
	"",
	"   ❯ 1. Yes, and use auto mode",
	"     2. Yes, manually approve edits",
	"     3. Tell Claude what to change",
	"        shift+tab to approve with this feedback",
	"",
	"   ctrl+g to edit in Vim ·",
	"   ~/.claude/plans/plan-a-one-line-change-purrfect-blossom.md",
	"────────────────────────────────────────────────────────────────────── wfcap-c ─",
}

// askUserQuestionScreen: a single AskUserQuestion with three options
// (daemon flags: --dangerously-skip-permissions).
var askUserQuestionScreen = []string{
	"",
	"",
	"❯ Use the AskUserQuestion tool to ask me a single question: \"Which accent color",
	"  should the dashboard use?\" with exactly these options in this order:",
	"  \"Crimson\" (description: warm, high contrast), \"Cobalt\" (description: cool,",
	"  calmer), \"Forest\" (description: muted green). Header: \"Color\". After I",
	"  answer, say which one I picked and stop.",
	"────────────────────────────────────────────────────────────────────────────────",
	" ☐ Color",
	"",
	"Which accent color should the dashboard use?",
	"",
	"❯ 1. Crimson",
	"     warm, high contrast",
	"  2. Cobalt",
	"     cool, calmer",
	"  3. Forest",
	"     muted green",
	"  4. Type something.",
	"────────────────────────────────────────────────────────────────────────────────",
	"  5. Chat about this",
	"",
	"Enter to select · ↑/↓ to navigate · Esc to cancel",
	"────────────────────────────────────────────────────────────────────── wfcap-b ─",
}

// askUserQuestionWrappedScreen: a long question wrapped over three rows
// inside the CLI's │ gutter.
var askUserQuestionWrappedScreen = []string{
	"",
	"",
	"✻ Brewed for 2s · done 8:18 PM",
	"",
	"❯ Use AskUserQuestion once more with a deliberately long question that wraps",
	"  across more than one terminal line (about 150 characters), header \"Layout\",",
	"  options \"Single column\" and \"Two columns\". After I answer, stop.",
	"────────────────────────────────────────────────────────────────────────────────",
	" ☐ Layout",
	"",
	"│ For the main dashboard view that most people will open first thing in the",
	"│ morning on both wide desktop monitors and narrow laptop screens, which overall",
	"│ layout should we use?",
	"",
	"❯ 1. Single column",
	"     Stacks every panel vertically; simplest on narrow screens",
	"  2. Two columns",
	"     Side-by-side panels; denser on wide screens",
	"  3. Type something.",
	"────────────────────────────────────────────────────────────────────────────────",
	"  4. Chat about this",
	"",
	"Enter to select · ↑/↓ to navigate · Esc to cancel",
	"───────────────────────────────────────────────────────────────────── wfcap-b ─",
}

// answeredScreen: the screen right after the Bash permission prompt above
// was answered with Enter — the dialog is gone, the agent finished.
var answeredScreen = []string{
	"▝▜██████▀  Fable 5.1 · Claude Max",
	" ▝▝   ▝▝   /…/ef03e839-a6f5-4812-ac95-6c4a4769c125/scratchpad/scratchproj1",
	"",
	"❯ Run this exact multi-line bash command, verbatim, as a single Bash tool call",
	"  (do not simplify it): for f in alpha beta; do",
	"    echo \"line $f\" >> capture-note.txt",
	"  done",
	"  Then say DONE and stop.",
	"",
	"⏺ Running the loop command now.",
	"",
	"  Ran 1 shell command",
	"",
	"⏺ DONE",
	"",
	"✻ Cogitated for 3s · done 8:18 PM",
	"                                                              ● high · /effort",
	"────────────────────────────────────────────────────────────────────── wfcap-a ─",
	"❯ cat capture-note.txt",
	"───────────────────────────────────────────────────────────────────────────────",
	"  ⏸ manual mode on · ? for shortcuts",
}

// quotedInDiffScreen: this very feature's diff and task prompt scrolled
// across the screen — every marker phrase appears, but with diff / quote /
// backtick framing and never as the complete frame.
var quotedInDiffScreen = []string{
	"⏺ Bash(git diff internal/daemon/agent/inputdialog.go)",
	"  ⎿  +// Tool permission (Bash / Edit / …):",
	"     +//   Do you want to proceed?",
	"     +//   ❯ 1. Yes",
	"     +//     2. Yes, and switch to auto mode",
	"     +//     3. No",
	"     +//   Esc to cancel · Tab to amend",
	"     +\tinputDialogQuestionPrefixes = []string{\"doyouwantto\"}",
	"     +\tinputDialogChoiceFooters = []string{\"esctocancel·tabtoamend\"}",
	"",
	"⏺ The task prompt says: when a Claude Code agent stops to ask for approval",
	"  (tool permission \"Do you want to proceed?\", an `AskUserQuestion` dialog,",
	"  a plan-approval prompt, …) the daemon does not know the agent is blocked.",
	"",
	"  > ☐ Color",
	"  > Which accent color should the dashboard use?",
	"  > 1. Crimson",
	"  > 5. Chat about this",
	"  > Enter to select · ↑/↓ to navigate · Esc to cancel",
	"",
	"  All tests passed. Do you want me to continue?",
	"  1. Yes",
	"  2. No",
	"────────────────────────────────────────────────────────────────────── wfcap-a ─",
}

// quotedMarkdownScreen: the same wording in a markdown document the agent
// is reading back, unframed (no footer, no numbering run).
var quotedMarkdownScreen = []string{
	"⏺ Read(docs/approvals.md)",
	"  ⎿  ## Approval prompts",
	"     Claude asks `Do you want to proceed?` and lists:",
	"     1. Yes",
	"     2. Yes, and don't ask again",
	"     3. No",
	"     Answer with Enter. Would you like to proceed? is the plan-mode variant.",
	"",
	"⏺ Noted.",
	"────────────────────────────────────────────────────────────────────── wfcap-a ─",
	"❯",
}

func TestDetectInputDialog_PermissionBash(t *testing.T) {
	d := detectInputDialog(permissionBashScreen)
	if d == nil {
		t.Fatal("bash permission prompt not detected")
	}
	wantOpts := []string{
		"Yes",
		"Yes, and switch to auto mode · auto mode handles these prompts for you",
		"No",
	}
	if strings.Join(d.Options, "|") != strings.Join(wantOpts, "|") {
		t.Fatalf("options = %q, want %q", d.Options, wantOpts)
	}
	// The multi-line command under approval is carried verbatim, line by
	// line, and the scrollback above the frame (the ⎿ echo) is not.
	for _, want := range []string{
		"Bash command",
		"Append two lines to capture-note.txt",
		"for f in alpha beta; do\n  echo \"line $f\" >> capture-note.txt\ndone",
		"A variable in this command can't be checked before it runs",
		"Do you want to proceed?",
	} {
		if !strings.Contains(d.Prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, d.Prompt)
		}
	}
	for _, reject := range []string{"⎿", "Tip:", "╌", "wfcap-a"} {
		if strings.Contains(d.Prompt, reject) {
			t.Errorf("prompt should not carry %q:\n%s", reject, d.Prompt)
		}
	}
	msg := d.message()
	if !strings.HasSuffix(msg, "1. Yes\n2. Yes, and switch to auto mode · auto mode handles these prompts for you\n3. No") {
		t.Errorf("message does not end with the numbered options:\n%s", msg)
	}
}

func TestDetectInputDialog_PermissionEditWrappedOption(t *testing.T) {
	d := detectInputDialog(permissionEditScreen)
	if d == nil {
		t.Fatal("edit permission prompt not detected")
	}
	if len(d.Options) != 3 {
		t.Fatalf("options = %q, want 3", d.Options)
	}
	if want := "Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)"; d.Options[1] != want {
		t.Errorf("wrapped option 2 = %q, want %q", d.Options[1], want)
	}
	if !strings.Contains(d.Prompt, "2 +hello from capture") || !strings.Contains(d.Prompt, "Do you want to make this edit to README.md?") {
		t.Errorf("prompt missing diff or question:\n%s", d.Prompt)
	}
}

func TestDetectInputDialog_PlanApproval(t *testing.T) {
	d := detectInputDialog(planApprovalScreen)
	if d == nil {
		t.Fatal("plan approval prompt not detected")
	}
	wantOpts := []string{
		"Yes, and use auto mode",
		"Yes, manually approve edits",
		"Tell Claude what to change shift+tab to approve with this feedback",
	}
	if strings.Join(d.Options, "|") != strings.Join(wantOpts, "|") {
		t.Fatalf("options = %q, want %q", d.Options, wantOpts)
	}
	for _, want := range []string{"Ready to code?", "Append the line \"Scratch project.\"", "Would you like to\nproceed?"} {
		if !strings.Contains(d.Prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, d.Prompt)
		}
	}
	if strings.Contains(d.Prompt, "/plan to preview") {
		t.Errorf("prompt leaked scrollback above the frame:\n%s", d.Prompt)
	}
}

func TestDetectInputDialog_AskUserQuestion(t *testing.T) {
	d := detectInputDialog(askUserQuestionScreen)
	if d == nil {
		t.Fatal("AskUserQuestion dialog not detected")
	}
	wantOpts := []string{"Crimson", "Cobalt", "Forest", "Type something.", "Chat about this"}
	if strings.Join(d.Options, "|") != strings.Join(wantOpts, "|") {
		t.Fatalf("options = %q, want %q", d.Options, wantOpts)
	}
	if want := "Color\nWhich accent color should the dashboard use?"; d.Prompt != want {
		t.Errorf("prompt = %q, want %q", d.Prompt, want)
	}
	msg := d.message()
	if !strings.Contains(msg, "1. Crimson — warm, high contrast") || !strings.Contains(msg, "5. Chat about this") {
		t.Errorf("message lacks option descriptions:\n%s", msg)
	}
}

func TestDetectInputDialog_AskUserQuestionWrapped(t *testing.T) {
	d := detectInputDialog(askUserQuestionWrappedScreen)
	if d == nil {
		t.Fatal("wrapped AskUserQuestion dialog not detected")
	}
	want := "Layout\nFor the main dashboard view that most people will open first thing in the morning on both wide desktop monitors and narrow laptop screens, which overall layout should we use?"
	if d.Prompt != want {
		t.Errorf("prompt = %q, want %q", d.Prompt, want)
	}
	if len(d.Options) != 4 || d.Options[0] != "Single column" || d.Options[3] != "Chat about this" {
		t.Errorf("options = %q", d.Options)
	}
}

func TestDetectInputDialog_Negatives(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{"answered — dialog gone", answeredScreen},
		{"feature diff and task prompt in scrollback", quotedInDiffScreen},
		{"markdown doc quoting the prompt", quotedMarkdownScreen},
		{"empty screen", make([]string, 24)},
		{
			// Everything but the footer: the agent printing an option list.
			name: "question and options without the dialog footer",
			lines: []string{
				"Do you want to proceed?",
				"1. Yes",
				"2. Yes, and don't ask again",
				"3. No",
				"",
				"Reply with a number.",
			},
		},
		{
			// AskUserQuestion shape without the header checkbox.
			name: "option list ending in Chat about this, no header",
			lines: []string{
				"Which accent color should the dashboard use?",
				"1. Crimson",
				"2. Chat about this",
				"Enter to select · ↑/↓ to navigate · Esc to cancel",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if d := detectInputDialog(tt.lines); d != nil {
				t.Fatalf("negative fixture detected a dialog: %s", d)
			}
		})
	}
}

func TestDetectInputDialog_PointerMoveKeepsSignature(t *testing.T) {
	// Arrowing between options moves the ❯ pointer; the frame is the same
	// dialog instance and must not re-raise the issue.
	moved := append([]string(nil), permissionBashScreen...)
	moved[18] = "   1. Yes"
	moved[19] = " ❯ 2. Yes, and switch to auto mode · auto mode handles these prompts for you"
	a, b := detectInputDialog(permissionBashScreen), detectInputDialog(moved)
	if a == nil || b == nil {
		t.Fatal("both frames must be detected")
	}
	if a.signature() != b.signature() {
		t.Fatalf("signature changed with pointer move:\n%s\n---\n%s", a.signature(), b.signature())
	}
}

// newInputDialogTestProcess builds a Process wired for synchronous dialog
// detection: claude-code backend, settle disabled, no PTY.
func newInputDialogTestProcess() *Process {
	p := &Process{
		backendName: backend.ClaudeBackendName,
		startedAt:   time.Now().UTC(),
		issueSubs:   make(map[string]chan *AgentIssue),
	}
	p.inputDialog.settle = -1
	return p
}

func drainIssues(ch chan *AgentIssue) []*AgentIssue {
	var out []*AgentIssue
	for {
		select {
		case i := <-ch:
			out = append(out, i)
		default:
			return out
		}
	}
}

func TestProcessInputDialog_RaisesAndClears(t *testing.T) {
	p := newInputDialogTestProcess()
	sub := p.SubscribeIssues("t")

	p.scanInputDialog(permissionBashScreen)
	issue := p.GetIssue()
	if issue == nil || issue.Type != AgentIssueInputRequired {
		t.Fatalf("issue = %+v, want input_required", issue)
	}
	if len(issue.Options) != 3 || !strings.Contains(issue.Message, "Do you want to proceed?") {
		t.Fatalf("issue payload = %+v", issue)
	}
	got := drainIssues(sub)
	if len(got) != 1 || got[0] == nil || got[0].Type != AgentIssueInputRequired {
		t.Fatalf("broadcast = %+v, want one input_required", got)
	}

	// Same frame again (a redraw, pointer move): no new broadcast.
	p.scanInputDialog(permissionBashScreen)
	if extra := drainIssues(sub); len(extra) != 0 {
		t.Fatalf("redraw re-broadcast the issue: %+v", extra)
	}

	// Scrollback output while parked must not auto-clear the issue.
	feedProcess(p, []string{"redraw line", "another", "and a third", "fourth"})
	if p.GetIssue() == nil {
		t.Fatal("line-based auto-clear removed the dialog issue while the dialog is still on screen")
	}

	// Dialog answered (from any client): the frame leaves the screen and
	// the issue clears with a nil broadcast.
	p.scanInputDialog(answeredScreen)
	if p.GetIssue() != nil {
		t.Fatalf("issue still set after dialog went away: %+v", p.GetIssue())
	}
	got = drainIssues(sub)
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("clear broadcast = %+v, want one nil", got)
	}

	// A second, different dialog later in the session raises again.
	p.scanInputDialog(askUserQuestionScreen)
	issue = p.GetIssue()
	if issue == nil || issue.Type != AgentIssueInputRequired || issue.Options[0] != "Crimson" {
		t.Fatalf("second dialog issue = %+v", issue)
	}
}

func TestProcessInputDialog_OnlyClaudeBackend(t *testing.T) {
	p := newInputDialogTestProcess()
	p.backendName = "codex"
	p.scanInputDialog(permissionBashScreen)
	if p.GetIssue() != nil {
		t.Fatalf("non-claude backend raised %+v", p.GetIssue())
	}
}

func TestProcessInputDialog_ManualClearReraises(t *testing.T) {
	p := newInputDialogTestProcess()
	p.scanInputDialog(permissionBashScreen)
	p.ClearIssue()
	if p.GetIssue() != nil {
		t.Fatal("ClearIssue did not clear")
	}
	// Still on screen at the next snapshot → raised again (not answered).
	p.scanInputDialog(permissionBashScreen)
	if issue := p.GetIssue(); issue == nil || issue.Type != AgentIssueInputRequired {
		t.Fatalf("issue after re-scan = %+v, want input_required", issue)
	}
}

func TestProcessInputDialog_SettleDebouncesRedraw(t *testing.T) {
	p := newInputDialogTestProcess()
	p.inputDialog.settle = 30 * time.Millisecond
	sub := p.SubscribeIssues("t")

	// A half-drawn frame (no options yet) followed within the settle window
	// by the complete frame: exactly one raise, with the complete frame.
	partial := append([]string(nil), permissionBashScreen[:19]...)
	p.scanInputDialog(partial)
	p.scanInputDialog(permissionBashScreen)
	if p.GetIssue() != nil {
		t.Fatal("issue raised before the settle window elapsed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.GetIssue() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	issue := p.GetIssue()
	if issue == nil || len(issue.Options) != 3 {
		t.Fatalf("settled issue = %+v, want the complete frame", issue)
	}
	time.Sleep(60 * time.Millisecond)
	if n := len(drainIssues(sub)); n != 1 {
		t.Fatalf("broadcasts = %d, want exactly 1", n)
	}

	// Answer: a transient redraw with the frame missing, then the final
	// screen — one clear.
	p.scanInputDialog(answeredScreen)
	deadline = time.Now().Add(2 * time.Second)
	for p.GetIssue() != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.GetIssue() != nil {
		t.Fatal("issue not cleared after the dialog went away")
	}
	time.Sleep(60 * time.Millisecond)
	if got := drainIssues(sub); len(got) != 1 || got[0] != nil {
		t.Fatalf("clear broadcasts = %+v, want one nil", got)
	}

	// Late timers after the PTY closes are ignored.
	p.issueMu.Lock()
	p.stopInputDialogLocked()
	p.issueMu.Unlock()
	p.scanInputDialog(permissionBashScreen)
	time.Sleep(60 * time.Millisecond)
	if p.GetIssue() != nil {
		t.Fatal("detector raised after stop")
	}
}

// planApprovalWideScreen: the plan-approval frame on a 100-column PTY
// (captured live, Claude Code v2.1.322, 2026-11-12). Unlike the 80-column
// capture above, the question fits one row and the footer keeps the plan
// file path on the SAME row as "ctrl+g to edit in Vim ·" — an exact-row
// footer match missed this frame entirely.
var planApprovalWideScreen = []string{
	"  ⎿  /plan to preview",
	"▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔",
	"  ────────────────────────────────────────────────────────────────────────────────────────────────",
	"   Ready to code?",
	"",
	"   Here is Claude's plan:",
	"  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	"   1. Append the word goodbye as a new line at the end of README.md in the working directory",
	"      (creating the file if it does not exist).",
	"   2. Verify by reading README.md and confirming the last line is goodbye.",
	"  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌",
	"  ────────────────────────────────────────────────────────────────────────────────────────────────",
	"   Claude has written up a plan and is ready to execute. Would you like to proceed?",
	"",
	"   ❯ 1. Yes, and use auto mode",
	"     2. Yes, manually approve edits",
	"     3. Tell Claude what to change",
	"        shift+tab to approve with this feedback",
	"",
	"   ctrl+g to edit in Vim · ~/.claude/plans/wise-giggling-thacker.md",
}

func TestDetectInputDialog_PlanApprovalWideFooter(t *testing.T) {
	d := detectInputDialog(planApprovalWideScreen)
	if d == nil {
		t.Fatal("plan approval prompt not detected when the footer carries the plan path on the same row")
	}
	if len(d.Options) != 3 || d.Options[0] != "Yes, and use auto mode" {
		t.Fatalf("options = %q", d.Options)
	}
	if !strings.Contains(d.Prompt, "Would you like to proceed?") || !strings.Contains(d.Prompt, "word goodbye") {
		t.Fatalf("prompt = %q", d.Prompt)
	}
}

// TestProcessInputDialog_IssueCarriesPromptAndDetails: the issue exposes the
// prompt WITHOUT the flattened option list, plus the per-option detail
// text, so a client that renders its own option buttons (the Telegram
// bridge) does not have to parse Message apart.
func TestProcessInputDialog_IssueCarriesPromptAndDetails(t *testing.T) {
	p := newInputDialogTestProcess()
	p.scanInputDialog(askUserQuestionScreen)
	issue := p.GetIssue()
	if issue == nil || issue.Type != AgentIssueInputRequired {
		t.Fatalf("issue = %+v, want input_required", issue)
	}
	if !strings.Contains(issue.Prompt, "Which accent color") || strings.Contains(issue.Prompt, "Crimson") {
		t.Fatalf("Prompt should be the question without the options, got %q", issue.Prompt)
	}
	if len(issue.OptionDetails) != len(issue.Options) || issue.OptionDetails[0] == "" {
		t.Fatalf("OptionDetails = %q for options %q", issue.OptionDetails, issue.Options)
	}

	p.scanInputDialog(answeredScreen)
	p.scanInputDialog(permissionBashScreen)
	issue = p.GetIssue()
	if issue == nil || !strings.Contains(issue.Prompt, "Do you want to proceed?") || strings.Contains(issue.Prompt, "1.") {
		t.Fatalf("permission issue prompt = %+v", issue)
	}
}
