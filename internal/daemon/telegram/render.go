// Telegram HTML rendering for router responses (v10.0 Torch, task
// 0137). The echo router speaks transport-agnostic CommandResponse
// blocks; this file is the Telegram counterpart of the Slack/Discord
// renderers — blocks map onto Telegram's small HTML dialect
// (parse_mode=HTML) and long output is chunked under the Bot API's
// 4096-character message cap.
package telegram

import (
	"strings"

	"github.com/watchfire-io/watchfire/internal/daemon/echo"
)

// maxMessageLen is Telegram's hard cap on sendMessage text length.
const maxMessageLen = 4096

// htmlEscaper escapes exactly the three characters Telegram's HTML
// parse mode requires (&, <, >). Not html.EscapeString — that also
// emits numeric entities for quotes, which Telegram's dialect does
// not guarantee to resolve.
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// EscapeHTML escapes user-controlled text for parse_mode=HTML.
func EscapeHTML(s string) string { return htmlEscaper.Replace(s) }

// RenderHTML converts a router CommandResponse into one or more
// Telegram HTML messages, each within the 4096-char cap. Block
// mapping: header → <b>…</b>, section → plain paragraph, context →
// <i>…</i>, divider → blank line. All block/fallback text is treated
// as user-controlled and escaped. When the response carries no
// blocks, the fallback Text is rendered instead. Tags never span
// lines, so the line-boundary chunking below can't produce unbalanced
// HTML (which Telegram rejects outright).
func RenderHTML(resp *echo.CommandResponse) []string {
	if resp == nil {
		return nil
	}
	if len(resp.Blocks) == 0 {
		if strings.TrimSpace(resp.Text) == "" {
			return nil
		}
		return chunkLines(EscapeHTML(resp.Text))
	}
	var lines []string
	for _, blk := range resp.Blocks {
		switch blk.Type {
		case "header":
			lines = append(lines, "<b>"+EscapeHTML(blk.Text)+"</b>")
		case "context":
			lines = append(lines, "<i>"+EscapeHTML(blk.Text)+"</i>")
		case "divider":
			lines = append(lines, "")
		default:
			// "section" — and any future block type degrades to a
			// plain paragraph rather than being dropped.
			lines = append(lines, EscapeHTML(blk.Text))
		}
	}
	// Trailing dividers would render as dangling blank lines.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return chunkLines(strings.Join(lines, "\n"))
}

// chunkLines splits text into chunks of at most maxMessageLen bytes,
// breaking only at line boundaries. Joining the chunks back with "\n"
// reproduces the input. A single line longer than the cap (no
// boundary to break at) is hard-split mid-line as a last resort.
func chunkLines(text string) []string {
	if text == "" {
		return nil
	}
	if len(text) <= maxMessageLen {
		return []string{text}
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
	}
	for _, line := range strings.Split(text, "\n") {
		for len(line) > maxMessageLen {
			flush()
			chunks = append(chunks, line[:maxMessageLen])
			line = line[maxMessageLen:]
		}
		need := len(line)
		if cur.Len() > 0 {
			need += cur.Len() + 1 // +1 for the joining newline
		}
		if need > maxMessageLen {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	flush()
	return chunks
}
