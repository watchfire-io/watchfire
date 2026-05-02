package echo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderInteractionNil(t *testing.T) {
	got := RenderInteraction(nil)
	if !strings.Contains(string(got), `"type":4`) {
		t.Fatalf("expected type 4 ack on nil resp, got %s", got)
	}
}

func TestRenderInteractionHeaderPlusSection(t *testing.T) {
	resp := &CommandResponse{
		Blocks: []Block{
			{Type: "header", Text: "Watchfire status"},
			{Type: "section", Markdown: true, Text: "*alpha* — 2 active task(s)"},
			{Type: "section", Markdown: true, Text: "• `#0042` build the thing"},
			{Type: "context", Text: "As of 2026-05-02"},
		},
		Ephemeral: true,
	}
	body := RenderInteraction(resp)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("rendered JSON malformed: %v", err)
	}
	if doc["type"].(float64) != 4 {
		t.Fatalf("expected type 4")
	}
	data := doc["data"].(map[string]any)
	if data["flags"].(float64) != 64 {
		t.Fatalf("expected ephemeral flag 64")
	}
	embeds := data["embeds"].([]any)
	if len(embeds) == 0 {
		t.Fatalf("expected embeds")
	}
	first := embeds[0].(map[string]any)
	if first["title"] != "Watchfire status" {
		t.Fatalf("expected title from header block, got %v", first["title"])
	}
	desc := first["description"].(string)
	if !strings.Contains(desc, "alpha") || !strings.Contains(desc, "#0042") {
		t.Fatalf("expected description to contain section text, got %q", desc)
	}
	footer := first["footer"].(map[string]any)
	if footer["text"] != "As of 2026-05-02" {
		t.Fatalf("expected context block in footer, got %v", footer["text"])
	}
}

func TestRenderInteractionDividerSplitsEmbeds(t *testing.T) {
	resp := &CommandResponse{
		Blocks: []Block{
			{Type: "header", Text: "Project A"},
			{Type: "section", Text: "task list a"},
			{Type: "divider"},
			{Type: "header", Text: "Project B"},
			{Type: "section", Text: "task list b"},
		},
	}
	body := RenderInteraction(resp)
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	embeds := doc["data"].(map[string]any)["embeds"].([]any)
	if len(embeds) != 2 {
		t.Fatalf("expected 2 embeds, got %d (%s)", len(embeds), body)
	}
	if embeds[0].(map[string]any)["title"] != "Project A" || embeds[1].(map[string]any)["title"] != "Project B" {
		t.Fatalf("expected one embed per project, got %s", body)
	}
}

func TestRenderInteractionTextOnly(t *testing.T) {
	resp := &CommandResponse{Text: "✅ Retrying task #0005", InChannel: true}
	body := RenderInteraction(resp)
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	data := doc["data"].(map[string]any)
	if data["content"] != "✅ Retrying task #0005" {
		t.Fatalf("expected content from text, got %v", data["content"])
	}
	if _, has := data["flags"]; has {
		t.Fatalf("non-ephemeral response should not set flags")
	}
}

func TestRenderInteractionTrimsLongDescription(t *testing.T) {
	long := strings.Repeat("a", discordEmbedDescriptionLimit+200)
	resp := &CommandResponse{
		Blocks: []Block{
			{Type: "header", Text: "T"},
			{Type: "section", Text: long},
		},
	}
	body := RenderInteraction(resp)
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	embeds := doc["data"].(map[string]any)["embeds"].([]any)
	desc := embeds[0].(map[string]any)["description"].(string)
	if !strings.HasSuffix(desc, discordRenderEllipsis) {
		t.Fatalf("expected truncated description to end with ellipsis")
	}
	// Within limit + ellipsis bytes
	if rc := runeLen(desc); rc > discordEmbedDescriptionLimit+1 {
		t.Fatalf("expected rune count ≤ %d, got %d", discordEmbedDescriptionLimit+1, rc)
	}
}

func TestRenderInteractionThreeProjectsThreeEmbeds(t *testing.T) {
	// Mirrors the spec's "3 projects → 3 embeds" assertion. The
	// router's status response uses one divider per project, so the
	// header + section + context for each project lands in its own
	// embed.
	resp := &CommandResponse{
		Blocks: []Block{
			{Type: "header", Text: "Watchfire — current status"},
			{Type: "section", Text: "*one* — 1 active task"},
			{Type: "section", Text: "• #0001 a"},
			{Type: "divider"},
			{Type: "section", Text: "*two* — 1 active task"},
			{Type: "section", Text: "• #0002 b"},
			{Type: "divider"},
			{Type: "section", Text: "*three* — 1 active task"},
			{Type: "section", Text: "• #0003 c"},
			{Type: "context", Text: "As of now"},
		},
	}
	body := RenderInteraction(resp)
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	embeds := doc["data"].(map[string]any)["embeds"].([]any)
	if len(embeds) != 3 {
		t.Fatalf("expected 3 embeds, got %d (%s)", len(embeds), body)
	}
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
