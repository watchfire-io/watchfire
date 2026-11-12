package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// Claude Code approval / question dialog detection (v10.2 Torch, task 0002,
// issue #58 part 2).
//
// When a claude-code session stops on an interactive dialog — a tool
// permission prompt ("Do you want to proceed?"), a plan-approval prompt
// ("Would you like to proceed?") or an AskUserQuestion option list — the
// agent is blocked until somebody answers, and nothing in the PTY stream
// says so. The detector below recognizes those dialog FRAMES on the vt10x
// screen snapshot the read loop already produces and raises an
// "input_required" issue carrying the prompt text and the option labels in
// order, so remote clients (MCP, gRPC status, GUI/TUI banners, Telegram via
// task 0003) can show the question and the user can answer it. The daemon
// never answers on the user's behalf; the issue clears as soon as the frame
// leaves the screen, whichever client answered it.
//
// Markers were pinned from live captures of Claude Code v2.1.322 (80x24 PTY,
// 2026-11-11). Three frames exist:
//
//	Tool permission (Bash / Edit / …; the context block is the command or
//	the diff, bounded above by a full-width ─ rule):
//	  ────────────────────────────────────────
//	   Bash command
//	   Append two lines to capture-note.txt
//	  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
//	   │ for f in alpha beta; do
//	   │   echo "line $f" >> capture-note.txt
//	   │ done
//	  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
//	   Do you want to proceed?
//	   ❯ 1. Yes
//	     2. Yes, and switch to auto mode · auto mode handles these prompts for you
//	     3. No
//	   Esc to cancel · Tab to amend
//
//	Plan approval (plan mode, ExitPlanMode):
//	   Claude has written up a plan and is ready to execute. Would you like to
//	   proceed?
//	   ❯ 1. Yes, and use auto mode
//	     2. Yes, manually approve edits
//	     3. Tell Claude what to change
//	   ctrl+g to edit in Vim ·
//
//	AskUserQuestion:
//	   ☐ Color
//	   Which accent color should the dashboard use?
//	   ❯ 1. Crimson
//	        warm, high contrast
//	     2. Cobalt
//	        cool, calmer
//	     3. Type something.
//	   ────────────────────────────────────────
//	     4. Chat about this
//	   Enter to select · ↑/↓ to navigate · Esc to cancel
//
// Like the trust dialog (trustdialog.go), the CLI positions words with CHA
// cursor moves (ESC[<n>G) rather than spaces — "Do\x1b[5Gyou\x1b[9Gwant…" in
// the raw capture — so matching is whitespace-insensitive: rows are
// normalized by lowercasing and dropping whitespace and dialog chrome, and
// markers compare against that form. The vt10x grid resolves the cursor
// moves, which is why detection reads the screen snapshot rather than the
// raw line stream: scrollback that merely quotes the wording (a task prompt
// about this feature, a diff of this file) is only ever on the CURRENT
// screen as part of ordinary prose, and prose never carries the full frame.
//
// False-positive safety: a lone phrase never fires. Every frame requires
// its question/header marker, consecutively numbered option rows starting
// at "1." (with the pinned first-option wording), and the dialog's footer
// row directly below the options. There is no startup window — the dialog
// can appear at any point in the session.

const (
	// inputDialogSettle is how long a frame (or its absence) must be stable
	// on screen before the issue is raised or cleared. Ink redraws a dialog
	// in several PTY writes; without settling, the half-drawn frame between
	// two reads would clear and re-raise the issue (and re-notify Telegram)
	// on every redraw. Human keystrokes are far slower than this.
	inputDialogSettle = 250 * time.Millisecond

	// inputDialogMaxGap is the most blank rows tolerated between the
	// question row and the first option row (the plan dialog has one).
	inputDialogMaxGap = 1
)

// inputDialog is a recognized dialog frame: the prompt the user must read
// and the option labels in display order.
type inputDialog struct {
	Prompt  string   // question plus any context (command, diff, plan)
	Options []string // ordered option labels, "N. " prefix stripped
	details []string // per-option secondary text (AskUserQuestion descriptions)
}

// message renders the frame as the plain text an issue consumer can show:
// the prompt, a blank line, then the numbered options.
func (d *inputDialog) message() string {
	var sb strings.Builder
	sb.WriteString(d.Prompt)
	sb.WriteString("\n")
	for i, opt := range d.Options {
		sb.WriteString("\n")
		sb.WriteString(strconv.Itoa(i + 1))
		sb.WriteString(". ")
		sb.WriteString(opt)
		if i < len(d.details) && d.details[i] != "" {
			sb.WriteString(" — ")
			sb.WriteString(d.details[i])
		}
	}
	return sb.String()
}

// signature identifies a frame instance for change detection. Cursor
// movement between options (the ❯ pointer) does not alter it.
func (d *inputDialog) signature() string {
	if d == nil {
		return ""
	}
	return d.message()
}

// inputDialogDroppedRunes extends the trust dialog's chrome set with the
// rule glyphs the approval dialogs draw. Ordinary punctuation is KEPT so
// quoted occurrences stay inexact.
const inputDialogDroppedRunes = trustDialogDroppedRunes + "╌▔▁"

// inputDialogQuestionPrefixes / Suffixes are the normalized question-row
// shapes that open a choice frame (tool permission, plan approval).
var (
	inputDialogQuestionPrefixes = []string{
		"doyouwantto", // "Do you want to proceed?" / "…make this edit to X?"
	}
	inputDialogQuestionSuffixes = []string{
		"wouldyouliketoproceed?", // plan approval, usually wrapped
	}
)

// inputDialogChoiceFooters close a choice frame (exact normalized row).
var inputDialogChoiceFooters = []string{
	"esctocancel·tabtoamend", // tool permission
	"esctocancel",            // older permission dialogs
}

// inputDialogPlanFooterPrefix opens the plan-approval footer. It is a
// PREFIX, not an exact row: the CLI appends the plan file path
// ("ctrl+g to edit in Vim · ~/.claude/plans/<name>.md") and whether that
// path shares the row depends on the terminal width — at 80 columns it
// wraps away, at 100 it does not (captured live, v2.1.322).
const inputDialogPlanFooterPrefix = "ctrl+gtoeditinvim·"

// inputDialogQuestionFooter closes an AskUserQuestion frame.
const inputDialogQuestionFooter = "entertoselect·↑/↓tonavigate·esctocancel"

// inputDialogChatOption is the normalized last option of every
// AskUserQuestion frame.
const inputDialogChatOption = "chataboutthis"

// inputDialogHeaderRunes open an AskUserQuestion frame's header row
// (one checkbox tab per question).
const inputDialogHeaderRunes = "☐☑☒"

type screenRow struct {
	norm string // normalized: lowercase, no whitespace, no chrome
	text string // display text: trimmed, leading pointer/box glyph removed
	rule bool   // row is a horizontal rule (─ or ╌ only)
}

// normalizeScreenRow lowercases a screen row and removes all whitespace,
// control characters and dialog chrome. Returns "" for rows without content.
func normalizeScreenRow(row string) string {
	var sb strings.Builder
	for _, r := range row {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			continue
		}
		if strings.ContainsRune(inputDialogDroppedRunes, r) {
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

// cleanScreenRow trims a screen row for display and strips one leading
// pointer or box glyph (❯, │) plus the single space after it, so a
// command's inner indentation survives.
func cleanScreenRow(row string) string {
	s := strings.TrimSpace(row)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if strings.ContainsRune("❯│┃", r[0]) {
		r = r[1:]
		if len(r) > 0 && r[0] == ' ' {
			r = r[1:]
		}
		s = strings.TrimRightFunc(string(r), unicode.IsSpace)
	}
	return s
}

func isRuleRow(row string) bool {
	s := strings.TrimSpace(row)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '─' && r != '╌' {
			return false
		}
	}
	return true
}

func parseScreenRows(lines []string) []screenRow {
	rows := make([]screenRow, len(lines))
	for i, l := range lines {
		rows[i] = screenRow{norm: normalizeScreenRow(l), text: cleanScreenRow(l), rule: isRuleRow(l)}
	}
	return rows
}

// optionNumber returns n when the normalized row starts with "n." and the
// text after it is non-empty; 0 otherwise.
func optionNumber(norm string) int {
	dot := strings.IndexByte(norm, '.')
	if dot <= 0 || dot > 2 || dot == len(norm)-1 {
		return 0
	}
	n, err := strconv.Atoi(norm[:dot])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// optionLabel strips the "N. " prefix from a display row.
func optionLabel(text string) string {
	if dot := strings.IndexByte(text, '.'); dot > 0 {
		return strings.TrimSpace(text[dot+1:])
	}
	return text
}

// parsedOptions is a run of consecutively numbered option rows starting at
// "1." at rows[start]. Non-option, non-blank rows inside the run attach to
// the preceding option as a continuation (wrapped label or hint).
type parsedOptions struct {
	labels []string
	conts  []string // continuation text per option ("" when none)
	end    int      // index of the first row after the run
}

// parseOptions reads option rows from start until a blank row. Rule rows
// inside the run are skipped (AskUserQuestion draws one before its last
// option). Returns nil unless the run starts with "1." and numbers are
// consecutive.
func parseOptions(rows []screenRow, start int) *parsedOptions {
	if start >= len(rows) || optionNumber(rows[start].norm) != 1 {
		return nil
	}
	po := &parsedOptions{}
	next := 1
	i := start
	for ; i < len(rows); i++ {
		r := rows[i]
		if r.norm == "" {
			if r.rule {
				continue
			}
			break
		}
		if n := optionNumber(r.norm); n > 0 {
			if n != next {
				return nil
			}
			po.labels = append(po.labels, optionLabel(r.text))
			po.conts = append(po.conts, "")
			next++
			continue
		}
		// Continuation of the previous option.
		k := len(po.labels) - 1
		if po.conts[k] != "" {
			po.conts[k] += " "
		}
		po.conts[k] += r.text
	}
	po.end = i
	return po
}

// nextContentRow returns the index of the first row at or after i that is
// neither blank nor a rule, or -1.
func nextContentRow(rows []screenRow, i int) int {
	for ; i < len(rows); i++ {
		if rows[i].norm != "" {
			return i
		}
	}
	return -1
}

func isChoiceQuestion(norm string) bool {
	if !strings.HasSuffix(norm, "?") {
		return false
	}
	for _, p := range inputDialogQuestionPrefixes {
		if strings.HasPrefix(norm, p) {
			return true
		}
	}
	for _, s := range inputDialogQuestionSuffixes {
		if strings.HasSuffix(norm, s) {
			return true
		}
	}
	return false
}

func isChoiceFooter(norm string) bool {
	for _, f := range inputDialogChoiceFooters {
		if norm == f {
			return true
		}
	}
	return strings.HasPrefix(norm, inputDialogPlanFooterPrefix)
}

// detectInputDialog recognizes a dialog frame on the current screen.
// lines are the vt10x rows exactly as Process.snapshotScreen produces them.
func detectInputDialog(lines []string) *inputDialog {
	rows := parseScreenRows(lines)
	if d := detectChoiceDialog(rows); d != nil {
		return d
	}
	return detectQuestionDialog(rows)
}

// detectChoiceDialog matches the tool-permission and plan-approval frames:
// a "Do you want to …?" / "Would you like to proceed?" row (possibly
// wrapped over two rows), the "1. Yes…" option run, and the footer.
func detectChoiceDialog(rows []screenRow) *inputDialog {
	for q := 0; q < len(rows); q++ {
		norm := rows[q].norm
		if norm == "" {
			continue
		}
		joined := norm
		if q > 0 {
			joined = rows[q-1].norm + norm
		}
		if !isChoiceQuestion(norm) && !isChoiceQuestion(joined) {
			continue
		}
		// First option within inputDialogMaxGap blank rows.
		start := -1
		for i, gap := q+1, 0; i < len(rows) && gap <= inputDialogMaxGap; i++ {
			if rows[i].norm == "" {
				gap++
				continue
			}
			start = i
			break
		}
		if start < 0 || !strings.HasPrefix(rows[start].norm, "1.yes") {
			continue
		}
		po := parseOptions(rows, start)
		if po == nil || len(po.labels) < 2 {
			continue
		}
		f := nextContentRow(rows, po.end)
		if f < 0 || !isChoiceFooter(rows[f].norm) {
			continue
		}
		d := &inputDialog{Prompt: choicePrompt(rows, q)}
		for i, l := range po.labels {
			if po.conts[i] != "" {
				l += " " + po.conts[i]
			}
			d.Options = append(d.Options, l)
		}
		return d
	}
	return nil
}

// choicePrompt collects the context above (and including) the question
// row q: everything below the topmost full-width ─ rule on screen, minus
// rules, blank rows and the CLI's auto-mode tip. Scrollback above the frame
// is cut off by that rule; when the frame is taller than the screen (a long
// diff) the context starts at the top row.
func choicePrompt(rows []screenRow, q int) string {
	top := 0
	for i := 0; i < q; i++ {
		if rows[i].rule && strings.ContainsRune(rows[i].text, '─') {
			top = i + 1
			break
		}
	}
	var parts []string
	for i := top; i <= q; i++ {
		r := rows[i]
		if r.norm == "" || r.rule {
			continue
		}
		if strings.HasPrefix(r.norm, "tip:") {
			continue
		}
		parts = append(parts, r.text)
	}
	return strings.Join(parts, "\n")
}

// detectQuestionDialog matches the AskUserQuestion frame: a checkbox
// header row, the question, the "1." option run ending in "Chat about
// this", and the footer.
func detectQuestionDialog(rows []screenRow) *inputDialog {
	for h := 0; h < len(rows); h++ {
		hr := rows[h]
		if hr.norm == "" {
			continue
		}
		if first, _ := utf8.DecodeRuneInString(hr.norm); !strings.ContainsRune(inputDialogHeaderRunes, first) {
			continue
		}
		// Question rows run from the header to the first option row.
		start := -1
		for i := h + 1; i < len(rows); i++ {
			if optionNumber(rows[i].norm) == 1 {
				start = i
				break
			}
		}
		if start < 0 {
			continue
		}
		po := parseOptions(rows, start)
		if po == nil || len(po.labels) < 2 || po.labels[len(po.labels)-1] == "" {
			continue
		}
		if normalizeScreenRow(po.labels[len(po.labels)-1]) != inputDialogChatOption {
			continue
		}
		f := nextContentRow(rows, po.end)
		if f < 0 || rows[f].norm != inputDialogQuestionFooter {
			continue
		}
		var question []string
		for i := h + 1; i < start; i++ {
			if rows[i].norm == "" || rows[i].rule {
				continue
			}
			question = append(question, rows[i].text)
		}
		if len(question) == 0 {
			continue
		}
		header := strings.TrimLeftFunc(hr.text, func(r rune) bool {
			return strings.ContainsRune(inputDialogHeaderRunes, r) || unicode.IsSpace(r)
		})
		d := &inputDialog{
			Prompt:  header + "\n" + strings.Join(question, " "),
			Options: po.labels,
			details: po.conts,
		}
		return d
	}
	return nil
}

// inputDialogState tracks the dialog frame seen on screen for one Process.
// Guarded by issueMu.
type inputDialogState struct {
	sig        string       // signature of the frame in the latest snapshot ("" = none)
	frame      *inputDialog // the frame behind sig
	changedAt  time.Time    // when sig last changed
	timer      *time.Timer  // pending settle check
	appliedSig string       // signature of the input_required issue currently raised
	stopped    bool         // readLoop ended; ignore late timers
	settle     time.Duration
}

func (s *inputDialogState) settleFor() time.Duration {
	if s.settle != 0 {
		return s.settle
	}
	return inputDialogSettle
}

// scanInputDialog is called by readLoop with each screen snapshot. It
// records the frame currently on screen and arms the settle timer; the
// issue itself is raised or cleared by settleInputDialog once the screen
// has been stable for inputDialogSettle.
func (p *Process) scanInputDialog(lines []string) {
	if p.backendName != backend.ClaudeBackendName {
		return
	}
	frame := detectInputDialog(lines)
	sig := frame.signature()

	p.issueMu.Lock()
	defer p.issueMu.Unlock()
	st := &p.inputDialog
	if st.stopped {
		return
	}
	if sig != st.sig {
		st.sig = sig
		st.frame = frame
		st.changedAt = time.Now()
	}
	if st.settle < 0 {
		// Test seam: apply synchronously.
		p.applyInputDialogLocked()
		return
	}
	if st.timer == nil {
		st.timer = time.AfterFunc(st.settleFor(), p.settleInputDialog)
	}
}

// settleInputDialog applies the latest observed frame once it has been
// stable for the settle window, re-arming itself otherwise.
func (p *Process) settleInputDialog() {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()
	st := &p.inputDialog
	st.timer = nil
	if st.stopped {
		return
	}
	if since := time.Since(st.changedAt); since < st.settleFor() {
		st.timer = time.AfterFunc(st.settleFor()-since, p.settleInputDialog)
		return
	}
	p.applyInputDialogLocked()
}

// applyInputDialogLocked raises, updates or clears the input_required
// issue to match the settled frame. Must be called with issueMu held.
func (p *Process) applyInputDialogLocked() {
	st := &p.inputDialog
	current := p.issue != nil && p.issue.Type == AgentIssueInputRequired
	if st.frame == nil {
		if current {
			p.logf("Claude Code dialog gone from screen — clearing input_required")
			p.clearIssueLocked()
		}
		st.appliedSig = ""
		return
	}
	if current && st.appliedSig == st.sig {
		return
	}
	st.appliedSig = st.sig
	p.cleanLineCount = 0
	p.setIssueLocked(&AgentIssue{
		Type:       AgentIssueInputRequired,
		DetectedAt: time.Now(),
		Message:    st.frame.message(),
		Prompt:     st.frame.Prompt,
		Options:    append([]string(nil), st.frame.Options...),
		// Parallel to Options; nil for the choice frames.
		OptionDetails: append([]string(nil), st.frame.details...),
	})
}

// stopInputDialogLocked disarms the detector when the PTY closes. Must be
// called with issueMu held.
func (p *Process) stopInputDialogLocked() {
	st := &p.inputDialog
	st.stopped = true
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
}

// String renders the frame for logs.
func (d *inputDialog) String() string {
	if d == nil {
		return "<none>"
	}
	return fmt.Sprintf("prompt=%q options=%q", d.Prompt, d.Options)
}
