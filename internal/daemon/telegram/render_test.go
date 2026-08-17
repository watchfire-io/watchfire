package telegram

import (
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/daemon/echo"
)

func TestRenderHTMLBlockMapping(t *testing.T) {
	resp := &echo.CommandResponse{
		Blocks: []echo.Block{
			{Type: "header", Text: "Watchfire — current status"},
			{Type: "section", Markdown: true, Text: "*proj* — 2 active task(s)"},
			{Type: "divider"},
			{Type: "context", Text: "As of 2026-08-17"},
		},
	}
	chunks := RenderHTML(resp)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d: %v", len(chunks), chunks)
	}
	want := "<b>Watchfire — current status</b>\n*proj* — 2 active task(s)\n\n<i>As of 2026-08-17</i>"
	if chunks[0] != want {
		t.Fatalf("rendered:\n%q\nwant:\n%q", chunks[0], want)
	}
}

func TestRenderHTMLEscapesUserText(t *testing.T) {
	resp := &echo.CommandResponse{
		Blocks: []echo.Block{
			{Type: "header", Text: `Evil <script>alert("x")</script> & co`},
			{Type: "section", Text: "a < b && b > c"},
			{Type: "context", Text: "<i>not italics</i>"},
		},
	}
	got := strings.Join(RenderHTML(resp), "\n")
	if strings.Contains(got, "<script>") || strings.Contains(got, "</script>") {
		t.Fatalf("unescaped script tag in output: %q", got)
	}
	for _, want := range []string{
		"<b>Evil &lt;script&gt;alert(\"x\")&lt;/script&gt; &amp; co</b>",
		"a &lt; b &amp;&amp; b &gt; c",
		"<i>&lt;i&gt;not italics&lt;/i&gt;</i>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%q", want, got)
		}
	}
}

func TestRenderHTMLFallbackTextAndNil(t *testing.T) {
	if got := RenderHTML(nil); got != nil {
		t.Fatalf("nil response rendered %v", got)
	}
	if got := RenderHTML(&echo.CommandResponse{Text: "  "}); got != nil {
		t.Fatalf("blank response rendered %v", got)
	}
	got := RenderHTML(&echo.CommandResponse{Text: "plain & <simple>"})
	if len(got) != 1 || got[0] != "plain &amp; &lt;simple&gt;" {
		t.Fatalf("fallback text rendered %v", got)
	}
}

func TestRenderHTMLDropsTrailingDividers(t *testing.T) {
	resp := &echo.CommandResponse{
		Blocks: []echo.Block{
			{Type: "section", Text: "body"},
			{Type: "divider"},
			{Type: "divider"},
		},
	}
	got := RenderHTML(resp)
	if len(got) != 1 || got[0] != "body" {
		t.Fatalf("trailing dividers survived: %v", got)
	}
}

func TestChunkLinesSplitsAtLineBoundaries(t *testing.T) {
	line := strings.Repeat("x", 100)
	var lines []string
	for i := 0; i < 120; i++ { // ~12.1KB total
		lines = append(lines, line)
	}
	text := strings.Join(lines, "\n")
	chunks := chunkLines(text)
	if len(chunks) < 3 {
		t.Fatalf("expected ≥3 chunks for %d bytes, got %d", len(text), len(chunks))
	}
	for i, c := range chunks {
		if len(c) > maxMessageLen {
			t.Fatalf("chunk %d is %d bytes, over the %d cap", i, len(c), maxMessageLen)
		}
		// Line boundary preserved: every chunk is whole lines.
		for _, l := range strings.Split(c, "\n") {
			if l != line {
				t.Fatalf("chunk %d contains a partial line %q", i, l)
			}
		}
	}
	if strings.Join(chunks, "\n") != text {
		t.Fatal("joining chunks does not reproduce the input")
	}
}

func TestChunkLinesHardSplitsPathologicalLine(t *testing.T) {
	text := strings.Repeat("y", maxMessageLen*2+10)
	chunks := chunkLines(text)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if strings.Join(chunks, "") != text {
		t.Fatal("hard-split chunks do not reproduce the input")
	}
	for i, c := range chunks {
		if len(c) > maxMessageLen {
			t.Fatalf("chunk %d over cap: %d", i, len(c))
		}
	}
}

func TestChunkLinesShortTextSingleChunk(t *testing.T) {
	if got := chunkLines("hello\nworld"); len(got) != 1 || got[0] != "hello\nworld" {
		t.Fatalf("short text chunked wrong: %v", got)
	}
	if got := chunkLines(""); got != nil {
		t.Fatalf("empty text rendered %v", got)
	}
}
