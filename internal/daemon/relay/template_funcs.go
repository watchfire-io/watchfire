package relay

import (
	"encoding/json"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"
)

// fallbackEmbedColor is the muted slate used when a project's hex color
// cannot be parsed. Matches Discord's neutral-grey embed accent so an
// invalid swatch never renders an alarming red bar.
const fallbackEmbedColor = 0x6B7280 // tailwind slate-500

// hexToInt converts a CSS-style hex color (`#RRGGBB`) into a 24-bit int
// suitable for Discord's embed `color` field. Empty strings or malformed
// values fall back to fallbackEmbedColor so a misconfigured project
// theme never breaks an outbound notification.
func hexToInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallbackEmbedColor
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return fallbackEmbedColor
	}
	v, err := strconv.ParseInt(s, 16, 32)
	if err != nil || v < 0 {
		return fallbackEmbedColor
	}
	return int(v)
}

// rfc3339 formats a time in UTC RFC3339, the format both Discord and
// Slack accept for embed `timestamp` / context block fields.
func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// jsonStr renders a Go string as a JSON-encoded string literal (including
// the surrounding quotes). Templates that build JSON by hand call this
// to safely interpolate user-supplied values that may contain newlines,
// quotes, or control characters.
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// digestSnippet returns the first n runes of s, appending "…" when the
// truncation actually drops content. Used by the weekly-digest template
// to keep Discord's 4096-char description budget safe with margin.
func digestSnippet(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if cut == n {
			return s[:i] + "…"
		}
		cut++
	}
	return s
}

// TemplateFuncs returns the FuncMap shared by every relay adapter
// template. Centralised so adding a new helper only requires editing
// one file.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"hexToInt":      hexToInt,
		"rfc3339":       rfc3339,
		"jsonStr":       jsonStr,
		"digestSnippet": digestSnippet,
		"slackEmoji":    slackEmojiForColor,
	}
}
