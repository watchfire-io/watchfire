package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Install-level tests run against a scratch HOME so no real client config
// is ever touched. Config dirs are pre-created so detection succeeds
// without the client CLIs on PATH.

func setScratchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("COPILOT_HOME", "")
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGeminiInstallIdempotent(t *testing.T) {
	home := setScratchHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := geminiInstall()
	if res.Action != ActionInstalled {
		t.Fatalf("first install: action = %v (%s), want %v", res.Action, res.Reason, ActionInstalled)
	}

	res = geminiInstall()
	if res.Action != ActionUnchanged {
		t.Fatalf("second install: action = %v, want %v", res.Action, ActionUnchanged)
	}

	st := geminiClient().Status()
	if !st.Configured {
		t.Error("Status().Configured = false after install")
	}
}

func TestGeminiInstallMalformedConfigDegradesToManual(t *testing.T) {
	home := setScratchHome(t)
	path := filepath.Join(home, ".gemini", "settings.json")
	writeFile(t, path, "{not json")

	res := geminiInstall()
	if res.Action != ActionManual {
		t.Fatalf("action = %v, want %v", res.Action, ActionManual)
	}
	if res.Snippet == "" {
		t.Error("manual result has no snippet")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{not json" {
		t.Errorf("malformed config was modified: %q (err %v)", raw, err)
	}
}

func TestGeminiInstallNotDetectedDegradesToManual(t *testing.T) {
	setScratchHome(t)
	t.Setenv("PATH", "")

	res := geminiInstall()
	if res.Action != ActionManual {
		t.Fatalf("action = %v, want %v", res.Action, ActionManual)
	}
	if res.Snippet == "" || res.ConfigPath == "" {
		t.Error("manual result missing snippet or config path")
	}
}

func TestOpencodeInstallIdempotentUnderXDG(t *testing.T) {
	setScratchHome(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := opencodeInstall()
	if res.Action != ActionInstalled {
		t.Fatalf("first install: action = %v (%s), want %v", res.Action, res.Reason, ActionInstalled)
	}
	want := filepath.Join(xdg, "opencode", "opencode.json")
	if res.ConfigPath != want {
		t.Errorf("ConfigPath = %s, want %s", res.ConfigPath, want)
	}

	res = opencodeInstall()
	if res.Action != ActionUnchanged {
		t.Fatalf("second install: action = %v, want %v", res.Action, ActionUnchanged)
	}
}

func TestCopilotInstallIdempotentUnderCopilotHome(t *testing.T) {
	setScratchHome(t)
	copilotHome := t.TempDir()
	t.Setenv("COPILOT_HOME", copilotHome)

	res := copilotInstall()
	if res.Action != ActionInstalled {
		t.Fatalf("first install: action = %v (%s), want %v", res.Action, res.Reason, ActionInstalled)
	}
	want := filepath.Join(copilotHome, "mcp-config.json")
	if res.ConfigPath != want {
		t.Errorf("ConfigPath = %s, want %s", res.ConfigPath, want)
	}

	res = copilotInstall()
	if res.Action != ActionUnchanged {
		t.Fatalf("second install: action = %v, want %v", res.Action, ActionUnchanged)
	}
}

func TestCodexInstallIdempotentAndPreserving(t *testing.T) {
	home := setScratchHome(t)
	path := filepath.Join(home, ".codex", "config.toml")
	writeFile(t, path, "model = \"o4\"\n")

	res := codexInstall()
	if res.Action != ActionInstalled {
		t.Fatalf("first install: action = %v (%s), want %v", res.Action, res.Reason, ActionInstalled)
	}

	res = codexInstall()
	if res.Action != ActionUnchanged {
		t.Fatalf("second install: action = %v, want %v", res.Action, ActionUnchanged)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); !strings.Contains(got, "model = \"o4\"") || !strings.Contains(got, "[mcp_servers.watchfire]") {
		t.Errorf("config content wrong:\n%s", got)
	}

	st := codexClient().Status()
	if !st.Configured {
		t.Error("Status().Configured = false after install")
	}
}

func TestCodexInstallMalformedConfigDegradesToManual(t *testing.T) {
	home := setScratchHome(t)
	path := filepath.Join(home, ".codex", "config.toml")
	writeFile(t, path, "[broken\n")

	res := codexInstall()
	if res.Action != ActionManual {
		t.Fatalf("action = %v, want %v", res.Action, ActionManual)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "[broken\n" {
		t.Errorf("malformed config was modified: %q (err %v)", raw, err)
	}
}
