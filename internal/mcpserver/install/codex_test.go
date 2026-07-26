package install

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func mustParseTOML(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("merged output is not valid TOML: %v\n%s", err, raw)
	}
	return root
}

func TestMergeCodexTOMLFreshFile(t *testing.T) {
	out, action, err := mergeCodexTOML(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %v, want %v", action, ActionInstalled)
	}
	root := mustParseTOML(t, out)
	entry := root["mcp_servers"].(map[string]any)["watchfire"].(map[string]any)
	if entry["command"] != "watchfire" {
		t.Errorf("command = %v, want watchfire", entry["command"])
	}
	args, _ := stringSlice(entry["args"])
	if len(args) != 2 || args[0] != "mcp" || args[1] != "serve" {
		t.Errorf("args = %v, want [mcp serve]", entry["args"])
	}
}

func TestMergeCodexTOMLPreservesUnrelatedContent(t *testing.T) {
	existing := []byte(`# my codex config
model = "o4"

[mcp_servers.github]
command = "gh-mcp"
args = ["serve"]
`)
	out, action, err := mergeCodexTOML(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %v, want %v", action, ActionInstalled)
	}
	if !strings.Contains(string(out), "# my codex config") {
		t.Error("comment lost in merge")
	}
	root := mustParseTOML(t, out)
	if root["model"] != "o4" {
		t.Errorf("unrelated key lost: model = %v", root["model"])
	}
	servers := root["mcp_servers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("unrelated server table lost")
	}
	if _, ok := servers["watchfire"]; !ok {
		t.Error("watchfire table not added")
	}
}

func TestMergeCodexTOMLUnchangedWhenAlreadyConfigured(t *testing.T) {
	existing := []byte(`[mcp_servers.watchfire]
command = "watchfire"
args = ["mcp", "serve"]
`)
	out, action, err := mergeCodexTOML(existing)
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

func TestMergeCodexTOMLUpdatesStaleEntryPreservingOtherKeys(t *testing.T) {
	existing := []byte(`[mcp_servers.watchfire]
command = "/old/watchfire"
args = [
  "old",
  "args",
]
startup_timeout_ms = 5000

[mcp_servers.watchfire.env]
FOO = "1"

[mcp_servers.other]
command = "other"
`)
	out, action, err := mergeCodexTOML(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionUpdated {
		t.Fatalf("action = %v, want %v", action, ActionUpdated)
	}
	root := mustParseTOML(t, out)
	servers := root["mcp_servers"].(map[string]any)
	entry := servers["watchfire"].(map[string]any)
	if entry["command"] != "watchfire" {
		t.Errorf("command = %v, want watchfire", entry["command"])
	}
	args, _ := stringSlice(entry["args"])
	if len(args) != 2 || args[0] != "mcp" || args[1] != "serve" {
		t.Errorf("args = %v, want [mcp serve]", entry["args"])
	}
	if _, ok := entry["startup_timeout_ms"]; !ok {
		t.Error("user's extra key lost on update")
	}
	env, ok := entry["env"].(map[string]any)
	if !ok || env["FOO"] != "1" {
		t.Error("user's env subtable lost on update")
	}
	if _, ok := servers["other"]; !ok {
		t.Error("unrelated server table lost on update")
	}
}

func TestMergeCodexTOMLMalformedFile(t *testing.T) {
	_, _, err := mergeCodexTOML([]byte("[mcp_servers.broken\ncommand ="))
	if err == nil {
		t.Fatal("expected error for malformed TOML, got none")
	}
}

func TestMergeCodexTOMLInlineTableDegradesToManual(t *testing.T) {
	// An existing entry written as an inline table has no dotted header to
	// rewrite; the merge must refuse rather than risk corrupting it.
	existing := []byte(`[mcp_servers]
watchfire = { command = "/old/watchfire", args = ["x"] }
`)
	_, _, err := mergeCodexTOML(existing)
	if err == nil {
		t.Fatal("expected error for inline-table entry, got none")
	}
}

func TestMergeCodexTOMLIdempotent(t *testing.T) {
	out, action, err := mergeCodexTOML(nil)
	if err != nil || action != ActionInstalled {
		t.Fatalf("first merge: action=%v err=%v", action, err)
	}
	out2, action2, err := mergeCodexTOML(out)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if action2 != ActionUnchanged {
		t.Fatalf("second merge action = %v, want %v", action2, ActionUnchanged)
	}
	if out2 != nil {
		t.Error("second merge rewrote the file")
	}
}
