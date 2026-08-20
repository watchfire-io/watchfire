package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fixtures ---------------------------------------------------------------

func assistantTextLine(text string) string {
	raw, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	})
	return string(raw)
}

func toolUseLine(name string, input map[string]any) string {
	raw, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]any{{"type": "tool_use", "name": name, "input": input}},
		},
	})
	return string(raw)
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

type emissionLog struct {
	mu   sync.Mutex
	list []Emission
}

func (l *emissionLog) add(e Emission) {
	l.mu.Lock()
	l.list = append(l.list, e)
	l.mu.Unlock()
}

func (l *emissionLog) texts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.list))
	for _, e := range l.list {
		out = append(out, e.Text)
	}
	return out
}

func (l *emissionLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.list)
}

// --- Claude JSONL parsing ---------------------------------------------------

// TestClaudeParseLine: assistant text relays verbatim, tool uses become
// one-liners, everything else (tool results, thinking, the custom-title
// line, garbage) is skipped.
func TestClaudeParseLine(t *testing.T) {
	c := &claudeTranscript{}
	cases := []struct {
		name string
		line string
		want []Emission
	}{
		{
			"custom title skipped",
			`{"type":"custom-title","customTitle":"proj:0141"}`,
			nil,
		},
		{
			"assistant text",
			assistantTextLine("Let me look at the code."),
			[]Emission{{Kind: EmissionAssistantText, Text: "Let me look at the code."}},
		},
		{
			"assistant string content",
			`{"type":"assistant","message":{"role":"assistant","content":"plain string"}}`,
			[]Emission{{Kind: EmissionAssistantText, Text: "plain string"}},
		},
		{
			"tool use with file_path",
			toolUseLine("Edit", map[string]any{"file_path": "internal/tui/model.go"}),
			[]Emission{{Kind: EmissionToolUse, Text: "⚒ Edit internal/tui/model.go"}},
		},
		{
			"tool use with command",
			toolUseLine("Bash", map[string]any{"command": "make test"}),
			[]Emission{{Kind: EmissionToolUse, Text: "⚒ Bash: make test"}},
		},
		{
			"tool use with no known input",
			toolUseLine("TodoWrite", map[string]any{"todos": []any{}}),
			[]Emission{{Kind: EmissionToolUse, Text: "⚒ TodoWrite"}},
		},
		{
			"thinking skipped, text kept",
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Done."}]}}`,
			[]Emission{{Kind: EmissionAssistantText, Text: "Done."}},
		},
		{
			"tool result skipped",
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
			nil,
		},
		{
			"whitespace-only text skipped",
			assistantTextLine("   \n  "),
			nil,
		},
		{
			"garbage line skipped",
			`{not json`,
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.ParseLine([]byte(tc.line))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d emissions, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("emission %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestToolOneLinerTruncation: multi-line and overlong inputs compress
// to a single bounded line.
func TestToolOneLinerTruncation(t *testing.T) {
	got := toolOneLiner("Bash", map[string]any{"command": "make build &&\nmake test"})
	if got != "⚒ Bash: make build && …" {
		t.Fatalf("multi-line command: %q", got)
	}
	long := strings.Repeat("x", 300)
	got = toolOneLiner("Bash", map[string]any{"command": long})
	if len([]rune(got)) > 140 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long command not truncated: %q", got)
	}
}

// --- transcript tailer ------------------------------------------------------

// TestTranscriptTailerIncremental drives the tailer against a synthetic
// JSONL file appended line by line: emissions arrive in order, a file
// that does not exist yet is retried, truncation resets the offset, and
// closing done drains the tail before Run returns.
func TestTranscriptTailerIncremental(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	rec := &emissionLog{}
	done := make(chan struct{})
	tailer := &TranscriptTailer{
		Source: &claudeTranscript{locateFn: func() (string, error) { return path, nil }},
		Emit:   rec.add,
		Poll:   2 * time.Millisecond,
	}
	runErr := make(chan error, 1)
	go func() { runErr <- tailer.Run(context.Background(), done) }()

	// The file does not exist yet — give the tailer a few polls on the
	// not-found path, then create it.
	time.Sleep(10 * time.Millisecond)
	appendFile(t, path, `{"type":"custom-title","customTitle":"proj:0141"}`+"\n")
	appendFile(t, path, assistantTextLine("First I'll read the spec.")+"\n")
	waitFor(t, "first assistant emission", func() bool { return rec.len() >= 1 })

	appendFile(t, path, toolUseLine("Edit", map[string]any{"file_path": "internal/tui/model.go"})+"\n")
	appendFile(t, path, toolUseLine("Bash", map[string]any{"command": "make test"})+"\n")
	waitFor(t, "tool emissions", func() bool { return rec.len() >= 3 })

	// A partially written line must not emit until its newline lands.
	partial := assistantTextLine("Now the tests pass.")
	appendFile(t, path, partial[:20])
	time.Sleep(15 * time.Millisecond)
	if rec.len() != 3 {
		t.Fatalf("partial line emitted early: %v", rec.texts())
	}
	appendFile(t, path, partial[20:]+"\n")
	waitFor(t, "completed partial line", func() bool { return rec.len() >= 4 })

	// Truncation: the file is rewritten smaller — the tailer resets to
	// offset zero and picks up the fresh content.
	if err := os.WriteFile(path, []byte(assistantTextLine("Fresh start.")+"\n"), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	waitFor(t, "post-truncation emission", func() bool { return rec.len() >= 5 })

	// Final drain: a line written right before done closes still lands.
	appendFile(t, path, assistantTextLine("Wrapping up.")+"\n")
	close(done)
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tailer did not exit after done closed")
	}

	want := []string{
		"First I'll read the spec.",
		"⚒ Edit internal/tui/model.go",
		"⚒ Bash: make test",
		"Now the tests pass.",
		"Fresh start.",
		"Wrapping up.",
	}
	got := rec.texts()
	if len(got) != len(want) {
		t.Fatalf("emissions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("emission %d = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// TestTranscriptTailerStopsOnContextCancel: bridge shutdown (ctx
// cancel) stops the tailer promptly.
func TestTranscriptTailerStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tailer := &TranscriptTailer{
		Source: &claudeTranscript{locateFn: func() (string, error) { return "", fmt.Errorf("not yet") }},
		Emit:   func(Emission) {},
		Poll:   2 * time.Millisecond,
	}
	runErr := make(chan error, 1)
	go func() { runErr <- tailer.Run(ctx, make(chan struct{})) }()
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tailer did not exit on context cancel")
	}
}

// TestTailerFor: Claude Code (and the empty default) tail transcripts;
// other backends fall through to tier 2.
func TestTailerFor(t *testing.T) {
	if _, ok := TailerFor("claude-code", "/w", time.Now(), "s"); !ok {
		t.Fatal("claude-code must have a tailer")
	}
	if _, ok := TailerFor("", "/w", time.Now(), "s"); !ok {
		t.Fatal("empty backend (claude default) must have a tailer")
	}
	for _, name := range []string{"codex", "opencode", "gemini", "copilot", "cursor"} {
		if _, ok := TailerFor(name, "/w", time.Now(), "s"); ok {
			t.Fatalf("backend %q should fall through to screen deltas", name)
		}
	}
}

// --- screen deltas ----------------------------------------------------------

// TestScreenDeltaDebounce: unchanged content is never resent; a change
// emits exactly one new snapshot.
func TestScreenDeltaDebounce(t *testing.T) {
	var mu sync.Mutex
	lines := []string{"", "$ make test  ", "ok        ", ""}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}

	rec := &emissionLog{}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runScreenDeltas(context.Background(), done, snapshot, rec.add, 2*time.Millisecond)
	}()

	waitFor(t, "first snapshot", func() bool { return rec.len() >= 1 })
	// Many polls over unchanged content — no resend.
	time.Sleep(40 * time.Millisecond)
	if rec.len() != 1 {
		t.Fatalf("unchanged screen resent: %d emissions", rec.len())
	}
	if got := rec.texts()[0]; got != "$ make test\nok" {
		t.Fatalf("normalized snapshot = %q", got)
	}

	mu.Lock()
	lines = []string{"", "$ make test  ", "ok        ", "$ done", ""}
	mu.Unlock()
	waitFor(t, "changed snapshot", func() bool { return rec.len() >= 2 })
	time.Sleep(20 * time.Millisecond)
	if rec.len() != 2 {
		t.Fatalf("changed screen emitted more than once: %d", rec.len())
	}

	close(done)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("screen-delta loop did not exit after done closed")
	}
}

// TestNormalizeScreen: ANSI stripped, \r overwrites resolved, trailing
// padding trimmed, blank edges dropped.
func TestNormalizeScreen(t *testing.T) {
	got := normalizeScreen([]string{
		"",
		"   ",
		"\x1b[31mred text\x1b[0m   ",
		"spinner1\rspinner2\rfinal",
		"middle",
		"",
	})
	// \r resolution picks the last non-empty segment of each line.
	want := "red text\nfinal\nmiddle"
	if got != want {
		t.Fatalf("normalizeScreen = %q, want %q", got, want)
	}
	if normalizeScreen([]string{"", "   ", ""}) != "" {
		t.Fatal("blank screen should normalize to empty")
	}
}

// TestTailerRelocatesWhenFileGoesStale: a tailer that locked onto the
// wrong file (e.g. a just-killed predecessor session's transcript,
// located before the live session's file existed) re-runs Locate after
// consecutive no-growth polls and switches to the fresh path, draining
// it from the top — instead of tailing a dead file forever.
func TestTailerRelocatesWhenFileGoesStale(t *testing.T) {
	dir := t.TempDir()
	stalePath := filepath.Join(dir, "stale.jsonl")
	livePath := filepath.Join(dir, "live.jsonl")
	appendFile(t, stalePath, assistantTextLine("Old conversation.")+"\n")

	var mu sync.Mutex
	current := stalePath
	rec := &emissionLog{}
	done := make(chan struct{})
	tailer := &TranscriptTailer{
		Source: &claudeTranscript{locateFn: func() (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return current, nil
		}},
		Emit: rec.add,
		Poll: 2 * time.Millisecond,
	}
	runErr := make(chan error, 1)
	go func() { runErr <- tailer.Run(context.Background(), done) }()

	// The stale file's content is replayed once...
	waitFor(t, "stale replay", func() bool { return rec.len() >= 1 })

	// ...then the live file appears and Locate starts returning it. The
	// stale file never grows, so after staleRelocateAfter polls the
	// tailer must switch and deliver the live content.
	appendFile(t, livePath, assistantTextLine("Fresh session reply.")+"\n")
	mu.Lock()
	current = livePath
	mu.Unlock()
	waitFor(t, "live content after relocate", func() bool {
		for _, txt := range rec.texts() {
			if txt == "Fresh session reply." {
				return true
			}
		}
		return false
	})

	close(done)
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tailer did not exit after done closed")
	}
}

// TestParseLineUserTurnBreak: a real user turn (typed text) emits a
// TurnBreak; tool_result-carrying "user" entries are mid-turn plumbing
// and emit nothing.
func TestParseLineUserTurnBreak(t *testing.T) {
	c := &claudeTranscript{}

	typed := `{"type":"user","message":{"role":"user","content":"so what'sup?"}}`
	got := c.ParseLine([]byte(typed))
	if len(got) != 1 || got[0].Kind != EmissionTurnBreak {
		t.Fatalf("typed user turn should emit a TurnBreak: %+v", got)
	}

	blockTurn := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`
	got = c.ParseLine([]byte(blockTurn))
	if len(got) != 1 || got[0].Kind != EmissionTurnBreak {
		t.Fatalf("text-block user turn should emit a TurnBreak: %+v", got)
	}

	toolResult := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1"}]}}`
	if got := c.ParseLine([]byte(toolResult)); len(got) != 0 {
		t.Fatalf("tool_result carrier must emit nothing: %+v", got)
	}
}
