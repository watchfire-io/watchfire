package install

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustParse(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("merged output is not valid JSON: %v\n%s", err, raw)
	}
	return root
}

func TestMergeJSONEntryFreshFile(t *testing.T) {
	out, action, err := mergeJSONEntry(nil, []string{"mcpServers"}, geminiEntry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %v, want %v", action, ActionInstalled)
	}
	root := mustParse(t, out)
	servers := root["mcpServers"].(map[string]any)
	entry := servers["watchfire"].(map[string]any)
	if entry["command"] != "watchfire" {
		t.Errorf("command = %v, want watchfire", entry["command"])
	}
}

func TestMergeJSONEntryPreservesUnrelatedEntries(t *testing.T) {
	existing := []byte(`{
  "theme": "dark",
  "mcpServers": {
    "github": {"command": "gh-mcp", "args": ["serve"]}
  }
}`)
	out, action, err := mergeJSONEntry(existing, []string{"mcpServers"}, geminiEntry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %v, want %v", action, ActionInstalled)
	}
	root := mustParse(t, out)
	if root["theme"] != "dark" {
		t.Errorf("unrelated top-level key lost: theme = %v", root["theme"])
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("unrelated server entry lost")
	}
	if _, ok := servers["watchfire"]; !ok {
		t.Error("watchfire entry not added")
	}
}

func TestMergeJSONEntryUnchangedWhenAlreadyConfigured(t *testing.T) {
	existing := []byte(`{
  "mcpServers": {
    "watchfire": {"command": "watchfire", "args": ["mcp", "serve"], "trust": true}
  }
}`)
	out, action, err := mergeJSONEntry(existing, []string{"mcpServers"}, geminiEntry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionUnchanged {
		t.Fatalf("action = %v, want %v", action, ActionUnchanged)
	}
	if out != nil {
		t.Errorf("expected nil output for unchanged config, got %s", out)
	}
}

func TestMergeJSONEntryUpdatesStaleEntryPreservingExtraKeys(t *testing.T) {
	existing := []byte(`{
  "mcpServers": {
    "watchfire": {"command": "/old/path/watchfire", "args": ["serve"], "env": {"FOO": "1"}}
  }
}`)
	out, action, err := mergeJSONEntry(existing, []string{"mcpServers"}, geminiEntry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionUpdated {
		t.Fatalf("action = %v, want %v", action, ActionUpdated)
	}
	root := mustParse(t, out)
	entry := root["mcpServers"].(map[string]any)["watchfire"].(map[string]any)
	if entry["command"] != "watchfire" {
		t.Errorf("command = %v, want watchfire", entry["command"])
	}
	if _, ok := entry["env"]; !ok {
		t.Error("user's extra env key lost on update")
	}
}

func TestMergeJSONEntryMalformedFile(t *testing.T) {
	_, _, err := mergeJSONEntry([]byte(`{"mcpServers": {`), []string{"mcpServers"}, geminiEntry())
	if err == nil {
		t.Fatal("expected error for malformed JSON, got none")
	}
}

func TestMergeJSONEntryNonObjectOnPath(t *testing.T) {
	_, _, err := mergeJSONEntry([]byte(`{"mcpServers": "oops"}`), []string{"mcpServers"}, geminiEntry())
	if err == nil {
		t.Fatal("expected error when path element is not an object, got none")
	}
}

func TestMergeJSONEntryOpencodeShape(t *testing.T) {
	out, action, err := mergeJSONEntry(nil, []string{"mcp"}, opencodeEntry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %v, want %v", action, ActionInstalled)
	}
	root := mustParse(t, out)
	entry := root["mcp"].(map[string]any)["watchfire"].(map[string]any)
	if entry["type"] != "local" {
		t.Errorf("type = %v, want local", entry["type"])
	}
	cmd, ok := entry["command"].([]any)
	if !ok || len(cmd) != 3 {
		t.Fatalf("command = %v, want 3-element array", entry["command"])
	}
	if entry["enabled"] != true {
		t.Errorf("enabled = %v, want true", entry["enabled"])
	}
}

func TestCustomSnippetIsValidJSON(t *testing.T) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(CustomSnippet()), &parsed); err != nil {
		t.Fatalf("CustomSnippet is not valid JSON: %v", err)
	}
	if parsed["command"] != "watchfire" {
		t.Errorf("command = %v, want watchfire", parsed["command"])
	}
	args, ok := parsed["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "mcp" || args[1] != "serve" {
		t.Errorf("args = %v, want [mcp serve]", parsed["args"])
	}
}

func TestClientSnippetsAreValid(t *testing.T) {
	for _, c := range Clients() {
		if strings.TrimSpace(c.Snippet()) == "" {
			t.Errorf("client %s has an empty snippet", c.ID)
		}
	}
	for _, id := range []string{"claude-code", "codex", "gemini", "opencode", "copilot"} {
		if _, ok := Get(id); !ok {
			t.Errorf("Get(%q) not found", id)
		}
	}
}
