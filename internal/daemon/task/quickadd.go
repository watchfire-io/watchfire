package task

import (
	"regexp"
	"strings"
)

// QuickAddItem is one parsed task from a quick-add text blob. All three
// quick-add surfaces (GUI modal, TUI overlay, `watchfire task quick`) feed
// their input through ParseQuickAdd so they agree on what becomes a task.
type QuickAddItem struct {
	Title              string
	Prompt             string
	AcceptanceCriteria string
}

// quickAddTitleMax is the soft cap for derived titles — truncation backs up
// to a word boundary at or before this many runes.
const quickAddTitleMax = 70

// bulletRe matches a TOP-LEVEL bullet marker at column 0: `- `, `* `, or a
// numbered `1. ` / `1) `. The marker must be followed by whitespace and
// content — a bare `-` line is not a bullet.
var bulletRe = regexp.MustCompile(`^([-*]|\d+[.)])\s+\S`)

// markerRe captures just the marker + trailing whitespace, for stripping.
var markerRe = regexp.MustCompile(`^([-*]|\d+[.)])\s+`)

// acRe matches an acceptance-criteria line inside a bullet body: optional
// leading whitespace and/or nested bullet marker, then "AC:" or
// "Acceptance:" (case-insensitive), then the criteria text.
var acRe = regexp.MustCompile(`(?i)^\s*(?:([-*]|\d+[.)])\s+)?(?:ac|acceptance):\s*(.*)$`)

// ParseQuickAdd splits free text into tasks. Each top-level bullet (`-`,
// `*`, or `1.` / `1)` numbered, at column 0) becomes one item; every line
// under a bullet — nested bullets, continuations, blank lines — folds into
// that item's prompt until the next top-level bullet. Text before the first
// bullet is treated as preamble and ignored. If the input has no bullets at
// all, the whole (non-empty) text becomes a single item.
func ParseQuickAdd(text string) []QuickAddItem {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var blocks [][]string
	var current []string
	inBullet := false
	for _, line := range lines {
		if bulletRe.MatchString(line) {
			if inBullet {
				blocks = append(blocks, current)
			}
			current = []string{line}
			inBullet = true
			continue
		}
		if inBullet {
			current = append(current, line)
		}
	}
	if inBullet {
		blocks = append(blocks, current)
	}

	if len(blocks) == 0 {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil
		}
		return []QuickAddItem{makeQuickAddItem(strings.Split(trimmed, "\n"))}
	}

	items := make([]QuickAddItem, 0, len(blocks))
	for _, block := range blocks {
		items = append(items, makeQuickAddItem(dedentBullet(block)))
	}
	return items
}

// dedentBullet strips the bullet marker from the first line and up to the
// marker's width of leading whitespace from every continuation line, so a
// nested `  - sub` under `- top` reads `- sub` in the prompt body.
func dedentBullet(block []string) []string {
	marker := markerRe.FindString(block[0])
	out := make([]string, 0, len(block))
	out = append(out, block[0][len(marker):])
	for _, line := range block[1:] {
		width := len(marker)
		i := 0
		for i < len(line) && i < width && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		out = append(out, line[i:])
	}
	return out
}

// makeQuickAddItem builds an item from a dedented block: AC lines are
// pulled out of the body into AcceptanceCriteria, the title derives from
// the first remaining line, and the prompt is the full remaining text.
func makeQuickAddItem(lines []string) QuickAddItem {
	var promptLines, acLines []string
	for _, line := range lines {
		if m := acRe.FindStringSubmatch(line); m != nil {
			acLines = append(acLines, strings.TrimSpace(m[2]))
			continue
		}
		promptLines = append(promptLines, line)
	}

	prompt := strings.TrimSpace(strings.Join(promptLines, "\n"))
	titleSource := prompt
	if titleSource == "" {
		// Bullet was nothing but AC lines — fall back so the task still
		// has a meaningful title.
		titleSource = strings.TrimSpace(strings.Join(acLines, "\n"))
	}

	return QuickAddItem{
		Title:              deriveQuickAddTitle(titleSource),
		Prompt:             prompt,
		AcceptanceCriteria: strings.Join(acLines, "\n"),
	}
}

// deriveQuickAddTitle returns the first sentence of the first line,
// truncated at a word boundary around quickAddTitleMax runes.
func deriveQuickAddTitle(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)

	// First sentence: cut at the earliest ". " / "! " / "? " boundary.
	cut := len(line)
	for _, sep := range []string{". ", "! ", "? "} {
		if i := strings.Index(line, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	line = strings.TrimRight(strings.TrimSpace(line[:cut]), ".!?")

	runes := []rune(line)
	if len(runes) <= quickAddTitleMax {
		return line
	}
	truncated := runes[:quickAddTitleMax]
	if i := lastSpaceIndex(truncated); i > 0 {
		truncated = truncated[:i]
	}
	return strings.TrimSpace(string(truncated)) + "…"
}

func lastSpaceIndex(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == ' ' {
			return i
		}
	}
	return -1
}
