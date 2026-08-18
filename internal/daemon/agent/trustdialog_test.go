package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// currentTrustDialogRaw is the folder-trust dialog as the claude CLI
// actually emits it (captured live 2026-08-18 against a fresh directory).
// Note the CLI positions words with CHA sequences (ESC[<n>G) instead of
// spaces — an ANSI-stripped line therefore has no inter-word spacing.
var currentTrustDialogRaw = []string{
	"\x1b[38;2;255;193;7m────────────────────────────────────────\x1b[39m",
	"\x1b[2G\x1b[38;2;255;193;7m\x1b[1mAccessing\x1b[12Gworkspace:\x1b[22m\x1b[39m",
	"",
	"\x1b[2G\x1b[1m/private/tmp/some/long/worktree/path/that/wraps/over/multiple/lin\x1b[22m",
	"\x1b[2G\x1b[1mes/0042\x1b[22m",
	"",
	"\x1b[2GQuick\x1b[8Gsafety\x1b[15Gcheck:\x1b[22GIs\x1b[25Gthis\x1b[30Ga\x1b[32Gproject\x1b[40Gyou\x1b[44Gcreated\x1b[52Gor\x1b[55Gone\x1b[59Gyou\x1b[63Gtrust?\x1b[70G(Like\x1b[76Gyour",
	"\x1b[2Gown\x1b[6Gcode,\x1b[12Ga\x1b[14Gwell-known\x1b[25Gopen\x1b[30Gsource\x1b[37Gproject,\x1b[46Gor\x1b[49Gwork\x1b[54Gfrom\x1b[59Gyour\x1b[64Gteam).\x1b[71GIf\x1b[74Gnot,",
	"\x1b[2Gtake\x1b[7Ga\x1b[9Gmoment\x1b[16Gto\x1b[19Greview\x1b[26Gwhat's\x1b[33Gin\x1b[36Gthis\x1b[41Gfolder\x1b[48Gfirst.",
	"",
	"\x1b[2GClaude\x1b[9GCode'll\x1b[17Gbe\x1b[20Gable\x1b[25Gto\x1b[28Gread,\x1b[34Gedit,\x1b[40Gand\x1b[44Gexecute\x1b[52Gfiles\x1b[58Ghere.",
	"",
	"\x1b[2G\x1b[38;2;153;153;153mSecurity\x1b[11Gguide\x1b[39m",
	"",
	"\x1b[2G\x1b[38;2;177;185;249m❯\x1b[4G\x1b[38;2;153;153;153m1.\x1b[7G\x1b[38;2;177;185;249mYes,\x1b[12GI\x1b[14Gtrust\x1b[20Gthis\x1b[25Gfolder\x1b[39m",
	"\x1b[4G\x1b[38;2;153;153;153m2.\x1b[7G\x1b[39mNo,\x1b[11Gexit",
	"",
	"\x1b[2G\x1b[38;2;153;153;153mEnter\x1b[8Gto\x1b[11Gconfirm\x1b[19G·\x1b[21GEsc\x1b[25Gto\x1b[28Gcancel\x1b[39m",
}

// classicTrustDialogRaw is the older wording of the same dialog.
var classicTrustDialogRaw = []string{
	"╭──────────────────────────────────────────╮",
	"│ Do you trust the files in this folder?   │",
	"│                                          │",
	"│ /Users/dev/project                       │",
	"│                                          │",
	"│ Claude Code may read files in this       │",
	"│ folder. Reading untrusted files may lead │",
	"│ Claude Code to behave in unexpected ways │",
	"│                                          │",
	"│ ❯ 1. Yes, proceed                        │",
	"│   2. No, exit                            │",
	"╰──────────────────────────────────────────╯",
}

// feedDetector runs raw PTY lines through the same ANSI strip the
// production pipeline applies, returning how many frame completions fired.
func feedDetector(d *trustDialogDetector, rawLines []string) int {
	fires := 0
	for _, raw := range rawLines {
		clean := stripANSI(raw)
		if clean == "" {
			continue
		}
		if d.Feed(clean) {
			fires++
		}
	}
	return fires
}

func TestTrustDialogDetector_CurrentDialogFires(t *testing.T) {
	var d trustDialogDetector
	if got := feedDetector(&d, currentTrustDialogRaw); got != 1 {
		t.Fatalf("current dialog frame: got %d fires, want 1", got)
	}
}

func TestTrustDialogDetector_ClassicDialogFires(t *testing.T) {
	var d trustDialogDetector
	if got := feedDetector(&d, classicTrustDialogRaw); got != 1 {
		t.Fatalf("classic dialog frame: got %d fires, want 1", got)
	}
}

func TestTrustDialogDetector_WrappedLinesFire(t *testing.T) {
	// A narrow PTY can wrap both the question and the option line; the
	// detector joins adjacent lines so the split forms still match.
	wrapped := []string{
		"Do you trust the files in",
		"this folder?",
		"/Users/dev/project",
		"❯ 1. Yes, I trust this",
		"folder",
	}
	var d trustDialogDetector
	if got := feedDetector(&d, wrapped); got != 1 {
		t.Fatalf("wrapped dialog frame: got %d fires, want 1", got)
	}
}

// farApartFixture separates the question and option lines by more filler
// than trustDialogFrameSpan allows, so the pending question must expire.
func farApartFixture() []string {
	lines := []string{"Do you trust the files in this folder?"}
	for i := 0; i < trustDialogFrameSpan+5; i++ {
		lines = append(lines, "unrelated output line")
	}
	return append(lines, "❯ 1. Yes, proceed")
}

func TestTrustDialogDetector_NegativeFixtures(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			// Realistic scrollback: an agent working on THIS feature prints
			// the phrase inside a fenced code block. The bare question line
			// matches, but no accept-option line ever follows — no fire.
			name: "phrase quoted inside a code block",
			lines: []string{
				"The daemon must detect the dialog. From the CLI capture:",
				"```",
				"Do you trust the files in this folder?",
				"```",
				"We match it with a normalized comparison, tolerant of the",
				"CHA-positioned rendering the CLI actually uses.",
				"Building detector state machine...",
				"Tests passing.",
			},
		},
		{
			// This task's own diff: both dialog lines appear, but with diff
			// framing ("+", string-literal quotes) — exactness must reject.
			name: "dialog text inside a git diff",
			lines: []string{
				"+var trustDialogQuestions = []string{",
				"+\t\"doyoutrustthefilesinthisfolder?\",",
				"+}",
				"+ Do you trust the files in this folder?",
				"+ ❯ 1. Yes, proceed",
				"+\t\"1.yes,itrustthisfolder\",",
			},
		},
		{
			// The task prompt echoed at session start quotes the phrase
			// mid-sentence — never an exact frame line.
			name: "phrase quoted mid-sentence in the task prompt",
			lines: []string{
				"CONTEXT: the CLI blocks on the interactive \"Do you trust the",
				"files in this folder?\" dialog and the positional task prompt",
				"only submits after acceptance. Manual workaround:",
				"AgentService.SendInput with \"\\r\".",
				"1. Yes, proceed with the implementation as described above.",
			},
		},
		{
			// Question and option separated by more than the frame span —
			// stale question state must expire.
			name:  "option line far beyond the frame span",
			lines: farApartFixture(),
		},
		{
			name: "ordinary agent output",
			lines: []string{
				"Compiling internal/daemon/agent...",
				"ok  \tgithub.com/watchfire-io/watchfire/internal/daemon/agent\t0.5s",
				"All tests passed. Do you want me to continue?",
				"1. Yes",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d trustDialogDetector
			if got := feedDetector(&d, tt.lines); got != 0 {
				t.Fatalf("negative fixture fired %d times, want 0", got)
			}
		})
	}
}

// newTrustTestProcess builds a Process wired for trust-dialog testing:
// claude-code backend, fresh startedAt, and an ack sink instead of a PTY.
// projectID stays empty so logf is a no-op (no PTY, no log files).
func newTrustTestProcess(acks *[]string) *Process {
	return &Process{
		backendName: backend.ClaudeBackendName,
		startedAt:   time.Now().UTC(),
		issueSubs:   make(map[string]chan *AgentIssue),
		trustAckWrite: func(b []byte) error {
			*acks = append(*acks, string(b))
			return nil
		},
	}
}

// feedProcess pushes raw lines through the real detectIssues entry point.
func feedProcess(p *Process, rawLines []string) {
	p.detectIssues([]byte(strings.Join(rawLines, "\r\n") + "\r\n"))
}

func TestProcessTrustDialog_AutoAcksOnce(t *testing.T) {
	var acks []string
	p := newTrustTestProcess(&acks)

	feedProcess(p, currentTrustDialogRaw)

	if len(acks) != 1 || acks[0] != "\r" {
		t.Fatalf("expected exactly one \\r ack, got %q", acks)
	}
	if issue := p.GetIssue(); issue != nil {
		t.Fatalf("first detection must not raise an issue, got %+v", issue)
	}

	// The dialog redraws while the ack is in flight — same instance, and
	// still within the recurrence grace: no second ack, no issue.
	feedProcess(p, currentTrustDialogRaw)
	if len(acks) != 1 {
		t.Fatalf("redraw within grace must not re-ack, got %d acks", len(acks))
	}
	if issue := p.GetIssue(); issue != nil {
		t.Fatalf("redraw within grace must not raise an issue, got %+v", issue)
	}
}

func TestProcessTrustDialog_RecurrenceRaisesIssue(t *testing.T) {
	var acks []string
	p := newTrustTestProcess(&acks)

	feedProcess(p, currentTrustDialogRaw)
	if len(acks) != 1 {
		t.Fatalf("expected one ack, got %d", len(acks))
	}

	// Recurrence past the grace window: the ack demonstrably didn't take.
	p.issueMu.Lock()
	p.trustAckedAt = time.Now().Add(-2 * trustDialogRecurrenceGrace)
	p.issueMu.Unlock()

	feedProcess(p, currentTrustDialogRaw)

	if len(acks) != 1 {
		t.Fatalf("recurrence must not send a second ack, got %d acks", len(acks))
	}
	issue := p.GetIssue()
	if issue == nil || issue.Type != AgentIssueTrustDialog {
		t.Fatalf("recurrence must raise a trust_dialog issue, got %+v", issue)
	}

	// After the issue is raised, further frames are ignored entirely.
	feedProcess(p, currentTrustDialogRaw)
	if len(acks) != 1 {
		t.Fatalf("post-issue frames must not ack, got %d acks", len(acks))
	}
}

func TestProcessTrustDialog_OnlyClaudeBackend(t *testing.T) {
	var acks []string
	p := newTrustTestProcess(&acks)
	p.backendName = "codex"

	feedProcess(p, currentTrustDialogRaw)

	if len(acks) != 0 {
		t.Fatalf("non-claude backend must never ack, got %q", acks)
	}
	if issue := p.GetIssue(); issue != nil {
		t.Fatalf("non-claude backend must never raise a trust issue, got %+v", issue)
	}
}

func TestProcessTrustDialog_MidSessionNeverFires(t *testing.T) {
	var acks []string
	p := newTrustTestProcess(&acks)
	// Session is well past the startup window — even a byte-perfect dialog
	// frame (e.g. the agent replaying a capture) must be ignored.
	p.startedAt = time.Now().Add(-2 * trustDialogWindow)

	feedProcess(p, currentTrustDialogRaw)

	if len(acks) != 0 {
		t.Fatalf("mid-session frame must not ack, got %q", acks)
	}
	if issue := p.GetIssue(); issue != nil {
		t.Fatalf("mid-session frame must not raise an issue, got %+v", issue)
	}
}
