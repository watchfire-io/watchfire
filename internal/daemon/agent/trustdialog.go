package agent

import (
	"strings"
	"time"
	"unicode"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// Claude Code folder-trust dialog detection (v10 Torch, task 0145).
//
// When the daemon starts a claude-code session in a directory the CLI has
// never seen, Claude Code blocks on an interactive trust dialog and the
// positional task prompt only submits after acceptance — the run stalls
// silently. The detector below recognises the dialog frame in the
// ANSI-stripped PTY lines that detectIssues already produces, so the daemon
// can auto-accept it once per session (SendInput "\r") and raise a visible
// "trust_dialog" issue if it somehow reappears.
//
// Rendering detail (captured live from the claude CLI): the dialog
// positions words with CHA sequences (ESC[<n>G) instead of space
// characters, so an ANSI-stripped dialog line has NO spaces between words
// ("Accessingworkspace:", "1.Yes,Itrustthisfolder"). Matching is therefore
// whitespace-insensitive: lines are normalised by lowercasing and dropping
// all whitespace plus the dialog's decorative glyphs (selection pointer,
// box drawing). Every other character is kept, so quoted occurrences with
// any framing (a diff's "+", a markdown "> ", a string literal's quotes)
// fail the exact-equality checks.
//
// False-positive safety is layered:
//   - A lone quoted phrase never fires: detection requires the dialog
//     FRAME — a question/context line followed within a few lines by the
//     "Yes …" option line, each matching exactly (nothing else on the line).
//   - The caller additionally gates on the claude-code backend and on the
//     session's first ~30 seconds (the dialog only ever appears before the
//     first prompt submission).

const (
	// trustDialogWindow bounds detection to session startup. The dialog can
	// only appear before the initial prompt submits; anything matching later
	// is the agent quoting the text (e.g. a task about this very feature).
	trustDialogWindow = 30 * time.Second

	// trustDialogFrameSpan is the maximum number of non-empty lines allowed
	// between the question/context line and the yes-option line. The real
	// frame has ~10 lines in between (wrapped path, safety copy, blank rows
	// collapse away); 20 leaves headroom for long worktree paths.
	trustDialogFrameSpan = 20

	// trustDialogRecurrenceGrace ignores frame matches briefly after the
	// auto-ack: the TUI redraws the dialog while the "\r" is still in
	// flight, and those redraws are the same dialog instance, not a
	// recurrence worth alarming the user about.
	trustDialogRecurrenceGrace = 2 * time.Second
)

// trustDialogQuestions are the normalised question/context lines that open
// the dialog frame. Current CLI wording first, classic wordings kept for
// older versions.
var trustDialogQuestions = []string{
	"accessingworkspace:",             // current CLI (2026): banner above the safety copy
	"doyoutrustthefilesinthisfolder?", // classic wording
	"doyoutrustthisfolder?",           // shortened variant
}

// trustDialogYesOptions are the normalised accept-option lines that close
// the frame. The selection pointer (❯) and inter-word spacing are already
// dropped by normalizeTrustLine.
var trustDialogYesOptions = []string{
	"1.yes,itrustthisfolder", // current CLI (2026)
	"yes,itrustthisfolder",
	"1.yes,proceed", // classic wording
	"yes,proceed",
}

// trustDialogDroppedRunes are decorative glyphs the dialog may draw around
// its text. Dropped during normalisation so frame chrome can't break the
// exact-equality match. Deliberately small: ordinary punctuation ("+", ">",
// quotes, backticks) is KEPT so quoted/diffed occurrences stay inexact.
const trustDialogDroppedRunes = "❯│┃─━╭╮╰╯├┤▏▕"

// normalizeTrustLine lowercases an ANSI-stripped line and removes all
// whitespace, control characters, and dialog chrome. Returns "" for lines
// that carry no content.
func normalizeTrustLine(line string) string {
	var sb strings.Builder
	for _, r := range line {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			continue
		}
		if strings.ContainsRune(trustDialogDroppedRunes, r) {
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

func isTrustDialogQuestion(norm string) bool {
	for _, q := range trustDialogQuestions {
		if norm == q {
			return true
		}
	}
	return false
}

func isTrustDialogYesOption(norm string) bool {
	for _, o := range trustDialogYesOptions {
		if norm == o {
			return true
		}
	}
	return false
}

// trustDialogDetector is a tiny state machine fed one ANSI-stripped,
// non-empty line at a time. Feed returns true exactly when a complete
// dialog frame (question line, then yes-option line within
// trustDialogFrameSpan lines) has been observed.
//
// Terminal wrapping tolerance: each check also tries the previous line
// joined with the current one, so a question or option split across two
// rows by a narrow PTY still matches.
type trustDialogDetector struct {
	prevNorm           string
	pendingQuestion    bool
	linesSinceQuestion int
}

// scanTrustDialogLocked feeds one ANSI-stripped line to the trust-dialog
// detector and acts on a completed frame: first detection auto-accepts the
// dialog with a single "\r", a genuine recurrence raises a visible
// "trust_dialog" issue instead of looping input. Must be called with
// issueMu held (detectIssues does).
func (p *Process) scanTrustDialogLocked(cleanLine string) {
	// Only the claude-code CLI shows this dialog, and only before the first
	// prompt submission — bound scanning to the session's startup window so
	// an agent later quoting the dialog text can never trip the matcher.
	if p.backendName != backend.ClaudeBackendName || p.trustIssueRaised {
		return
	}
	if time.Since(p.startedAt) > trustDialogWindow {
		return
	}
	if !p.trustDetector.Feed(cleanLine) {
		return
	}

	if !p.trustAcked {
		p.trustAcked = true
		p.trustAckedAt = time.Now()
		p.logf("auto-accepted Claude Code trust dialog")
		write := p.trustAckWrite
		if write == nil {
			write = p.SendInput
		}
		if err := write([]byte("\r")); err != nil {
			p.logf("failed to send trust-dialog auto-ack: %v", err)
		}
		return
	}

	// The dialog redraws while the "\r" is still in flight; those frames are
	// the same instance, not a recurrence.
	if time.Since(p.trustAckedAt) < trustDialogRecurrenceGrace {
		return
	}

	p.trustIssueRaised = true
	p.setIssueLocked(&AgentIssue{
		Type:       AgentIssueTrustDialog,
		DetectedAt: time.Now(),
		Message:    "Claude Code trust dialog reappeared after auto-accept — attach to the session and respond manually",
	})
}

// Feed processes one ANSI-stripped line. Returns true when the line
// completes a dialog frame; the pending state resets so a subsequent full
// frame can be detected again.
func (d *trustDialogDetector) Feed(cleanLine string) bool {
	norm := normalizeTrustLine(cleanLine)
	if norm == "" {
		return false
	}
	joined := d.prevNorm + norm
	defer func() { d.prevNorm = norm }()

	if isTrustDialogQuestion(norm) || isTrustDialogQuestion(joined) {
		d.pendingQuestion = true
		d.linesSinceQuestion = 0
		return false
	}

	if !d.pendingQuestion {
		return false
	}
	d.linesSinceQuestion++
	if d.linesSinceQuestion > trustDialogFrameSpan {
		d.pendingQuestion = false
		return false
	}
	if isTrustDialogYesOption(norm) || isTrustDialogYesOption(joined) {
		d.pendingQuestion = false
		return true
	}
	return false
}
