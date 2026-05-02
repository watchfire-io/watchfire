// Package metrics — v6.0 Ember per-task metrics capture.
//
// Each backend emits its session-end summary in its own format. The
// daemon writes the agent's scrollback to a session log under
// ~/.watchfire/logs/<projectID>/<logID>.log; this package parses those
// summaries into the (tokensIn, tokensOut, costUSD) tuple consumed by
// the insights rollup.
//
// Parsing is best-effort: a missing or malformed summary returns
// (nil, nil, nil, nil) so the capture pipeline still records duration +
// exit_reason. Concrete implementations live in sibling files.
package metrics

import (
	"strings"
)

// Parser extracts per-session token + cost numbers from a backend's
// session log. All return values are pointers so a backend that exposes
// some-but-not-all fields (opencode, gemini) can leave the missing ones
// nil without conflating "absent" and "zero".
type Parser interface {
	// Parse reads the session log at sessionLogPath and returns the
	// observed token + cost numbers. A best-effort failure (file
	// missing, summary absent, malformed numbers) returns all-nil with
	// no error — a hard I/O error returns the error so the caller can
	// log a WARN and still write duration-only metrics.
	Parse(sessionLogPath string) (tokensIn, tokensOut *int64, costUSD *float64, err error)
}

// nullParser is the fallback for unknown backends. Always returns
// all-nil so the capture pipeline never panics.
type nullParser struct{}

// Parse implements Parser for the null fallback.
func (nullParser) Parse(string) (*int64, *int64, *float64, error) {
	return nil, nil, nil, nil
}

// NullParser returns a parser that always reports no metrics.
func NullParser() Parser { return nullParser{} }

// GetParser returns the registered parser for an agent backend name.
// Defaults to the null parser when name is unrecognised.
func GetParser(agentName string) Parser {
	switch strings.TrimSpace(strings.ToLower(agentName)) {
	case "claude-code", "claude":
		return claudeCodeParser{}
	case "codex", "openai-codex":
		return codexParser{}
	case "opencode":
		return opencodeParser{}
	case "gemini", "gemini-cli":
		return geminiParser{}
	case "copilot", "github-copilot", "copilot-cli":
		return copilotParser{}
	default:
		return nullParser{}
	}
}
