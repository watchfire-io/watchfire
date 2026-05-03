package echo

import (
	"encoding/json"
	"testing"
)

func TestRenderSlackEphemeralByDefault(t *testing.T) {
	resp := &CommandResponse{
		Text: "no projects mapped",
		Blocks: []Block{
			{Type: "section", Text: "no projects mapped"},
		},
		Ephemeral: true,
	}
	got := decodeJSON(t, RenderSlack(resp))
	if got["response_type"] != "ephemeral" {
		t.Errorf("response_type = %v, want ephemeral", got["response_type"])
	}
	if got["text"] != "no projects mapped" {
		t.Errorf("text = %v, want 'no projects mapped'", got["text"])
	}
}

func TestRenderSlackInChannelOverridesEphemeralDefault(t *testing.T) {
	resp := &CommandResponse{
		Text:      "retrying #42",
		InChannel: true,
	}
	got := decodeJSON(t, RenderSlack(resp))
	if got["response_type"] != "in_channel" {
		t.Errorf("response_type = %v, want in_channel", got["response_type"])
	}
}

func TestRenderSlackBlocksTranslation(t *testing.T) {
	resp := &CommandResponse{
		Ephemeral: true,
		Blocks: []Block{
			{Type: "header", Text: "Watchfire status"},
			{Type: "section", Markdown: true, Text: "*alpha* — 1 active"},
			{Type: "context", Text: "as of 2026-05-04"},
			{Type: "divider"},
			{Type: "section", Text: "plain only"},
		},
	}
	got := decodeJSON(t, RenderSlack(resp))
	blocks, ok := got["blocks"].([]any)
	if !ok || len(blocks) != 5 {
		t.Fatalf("blocks count = %d, want 5", len(blocks))
	}
	header := blocks[0].(map[string]any)
	if header["type"] != "header" {
		t.Errorf("blocks[0] type = %v", header["type"])
	}
	headerText := header["text"].(map[string]any)
	if headerText["type"] != "plain_text" || headerText["text"] != "Watchfire status" {
		t.Errorf("header text mismatch: %+v", headerText)
	}

	mrk := blocks[1].(map[string]any)
	mrkText := mrk["text"].(map[string]any)
	if mrkText["type"] != "mrkdwn" {
		t.Errorf("section mrkdwn type = %v", mrkText["type"])
	}

	ctxBlock := blocks[2].(map[string]any)
	if ctxBlock["type"] != "context" {
		t.Errorf("blocks[2] type = %v", ctxBlock["type"])
	}
	elements := ctxBlock["elements"].([]any)
	if len(elements) != 1 {
		t.Errorf("context elements len = %d", len(elements))
	}

	div := blocks[3].(map[string]any)
	if div["type"] != "divider" {
		t.Errorf("blocks[3] type = %v", div["type"])
	}

	plain := blocks[4].(map[string]any)
	plainText := plain["text"].(map[string]any)
	if plainText["type"] != "plain_text" {
		t.Errorf("plain section type = %v", plainText["type"])
	}
}

func TestRenderSlackUpdateCarriesReplaceFlag(t *testing.T) {
	resp := &CommandResponse{
		Text: "task retrying",
		Blocks: []Block{
			{Type: "section", Markdown: true, Text: "*Retrying #0042*"},
		},
	}
	got := decodeJSON(t, RenderSlackUpdate(resp))
	if got["replace_original"] != true {
		t.Errorf("replace_original = %v, want true", got["replace_original"])
	}
	if got["text"] != "task retrying" {
		t.Errorf("text = %v", got["text"])
	}
	blocks := got["blocks"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("blocks len = %d", len(blocks))
	}
}

func TestRenderSlackNilSafe(t *testing.T) {
	got := decodeJSON(t, RenderSlack(nil))
	if got["response_type"] != "ephemeral" {
		t.Errorf("nil resp -> response_type = %v", got["response_type"])
	}
	got = decodeJSON(t, RenderSlackUpdate(nil))
	if got["replace_original"] != true {
		t.Errorf("nil resp -> replace_original = %v", got["replace_original"])
	}
}

func TestCancelReasonModalViewShape(t *testing.T) {
	view := CancelReasonModalView("proj-abc", 42, "Build the Discord adapter")
	if view["type"] != "modal" {
		t.Errorf("view type = %v, want modal", view["type"])
	}
	if view["callback_id"] != cancelModalCallbackID {
		t.Errorf("callback_id = %v, want %s", view["callback_id"], cancelModalCallbackID)
	}
	if view["private_metadata"] != "proj-abc|42" {
		t.Errorf("private_metadata = %v, want proj-abc|42", view["private_metadata"])
	}

	// Title surface should mention the task title.
	rawBlocks, ok := view["blocks"].([]map[string]any)
	if !ok || len(rawBlocks) != 2 {
		t.Fatalf("blocks shape = %+v", view["blocks"])
	}
	intro := rawBlocks[0]
	introText := intro["text"].(map[string]any)
	if want := "Cancel: Build the Discord adapter"; introText["text"] != "*"+want+"*" {
		t.Errorf("intro text = %v, want *%s*", introText["text"], want)
	}

	input := rawBlocks[1]
	if input["block_id"] != cancelModalReasonBlock {
		t.Errorf("input block_id = %v", input["block_id"])
	}
	el := input["element"].(map[string]any)
	if el["action_id"] != cancelModalReasonAction {
		t.Errorf("input action_id = %v", el["action_id"])
	}
	if el["multiline"] != true {
		t.Errorf("input multiline = %v, want true", el["multiline"])
	}
}

func TestCancelReasonModalViewWithoutTitleFallsBackToGenericHeadline(t *testing.T) {
	view := CancelReasonModalView("proj-abc", 7, "")
	rawBlocks := view["blocks"].([]map[string]any)
	intro := rawBlocks[0]
	introText := intro["text"].(map[string]any)
	if introText["text"] != "*Cancel this task?*" {
		t.Errorf("intro fallback = %v", introText["text"])
	}
}

func TestSplitProjectTaskRef(t *testing.T) {
	cases := []struct {
		in        string
		wantProj  string
		wantTask  int
		wantOK    bool
	}{
		{"proj-abc|42", "proj-abc", 42, true},
		{"a|b|7", "a|b", 7, true}, // pipe in project id allowed; only last pipe splits
		{"|42", "", 0, false},     // empty project id is not ok (idx<=0)
		{"proj|", "", 0, false},
		{"proj-only", "", 0, false},
		{"proj|abc", "", 0, false},
	}
	for _, tc := range cases {
		gotProj, gotTask, ok := splitProjectTaskRef(tc.in)
		if ok != tc.wantOK || gotProj != tc.wantProj || gotTask != tc.wantTask {
			t.Errorf("splitProjectTaskRef(%q) = (%q,%d,%v), want (%q,%d,%v)",
				tc.in, gotProj, gotTask, ok, tc.wantProj, tc.wantTask, tc.wantOK)
		}
	}
}

// decodeJSON unmarshals into a map for assertion-by-key. Mirrors the
// helper Discord render tests use.
func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("malformed JSON: %v\n%s", err, body)
	}
	return doc
}

