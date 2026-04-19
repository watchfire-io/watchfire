package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestCopilotBuildCommandArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &Copilot{}
	cmd, err := c.BuildCommand(CommandOpts{
		SessionName:   "proj:task:#0001-foo",
		SystemPrompt:  "SYS",
		InitialPrompt: "do the thing",
		ExtraArgs:     []string{"--model", "gpt-5"},
	})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if cmd.PasteInitialPrompt {
		t.Errorf("PasteInitialPrompt = true, want false")
	}

	want := []string{
		"--allow-all",
		"--model", "gpt-5",
		"-p", "do the thing",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", cmd.Args, want)
	}
	for i, a := range want {
		if cmd.Args[i] != a {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], a)
		}
	}

	expectedHome := filepath.Join(home, ".watchfire", "copilot-home", "proj:task:#0001-foo")
	wantEnv := map[string]bool{
		"COPILOT_HOME=" + expectedHome:                    true,
		"COPILOT_CUSTOM_INSTRUCTIONS_DIRS=" + expectedHome: true,
	}
	for _, e := range cmd.Env {
		delete(wantEnv, e)
	}
	if len(wantEnv) != 0 {
		t.Errorf("Env missing entries: %v; got %v", wantEnv, cmd.Env)
	}
}

func TestCopilotBuildCommandNoInitialPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &Copilot{}
	cmd, err := c.BuildCommand(CommandOpts{SessionName: "proj:chat"})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--allow-all" {
		t.Errorf("Args = %v, want [--allow-all]", cmd.Args)
	}
	for _, a := range cmd.Args {
		if a == "-p" {
			t.Errorf("expected no -p flag when InitialPrompt is empty, got args %v", cmd.Args)
		}
	}
}

func TestCopilotResolveExecutableFromSettings(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "copilot")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	c := &Copilot{}
	s := &models.Settings{
		Agents: map[string]*models.AgentConfig{
			CopilotBackendName: {Path: bin},
		},
	}

	got, err := c.ResolveExecutable(s)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestCopilotResolveExecutableFromPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "copilot")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())

	c := &Copilot{}
	got, err := c.ResolveExecutable(nil)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestCopilotResolveExecutableFromFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", home)

	// Seed ~/.local/bin/copilot as the fallback.
	fallbackDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bin := filepath.Join(fallbackDir, "copilot")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if runtime.GOOS == "darwin" {
		// Skip if a system-wide copilot is installed — the macOS fallbacks
		// for /opt/homebrew/bin and /usr/local/bin are checked after the
		// user-home fallbacks, so this test remains valid, but the
		// ResolveExecutable loop iterates in list order and returns the
		// first hit. Since ~/.local/bin comes first, we're fine.
	}

	c := &Copilot{}
	got, err := c.ResolveExecutable(nil)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestCopilotResolveExecutableNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/copilot"); err == nil {
			t.Skip("skipping: system-wide /opt/homebrew/bin/copilot exists")
		}
		if _, err := os.Stat("/usr/local/bin/copilot"); err == nil {
			t.Skip("skipping: system-wide /usr/local/bin/copilot exists")
		}
	}

	c := &Copilot{}
	_, err := c.ResolveExecutable(nil)
	if err == nil {
		t.Fatal("expected error when copilot not found, got nil")
	}
	// Error message should mention both install channels so users can act on it.
	msg := err.Error()
	if !strings.Contains(msg, "brew install copilot-cli") {
		t.Errorf("error does not mention `brew install copilot-cli`: %q", msg)
	}
	if !strings.Contains(msg, "npm install -g @github/copilot") {
		t.Errorf("error does not mention `npm install -g @github/copilot`: %q", msg)
	}
}

func TestCopilotSandboxExtras(t *testing.T) {
	c := &Copilot{}
	e := c.SandboxExtras()
	wantSub := map[string]bool{
		"~/.watchfire/copilot-home": true,
		"~/.copilot":                true,
	}
	for _, p := range e.WritableSubpaths {
		delete(wantSub, p)
	}
	if len(wantSub) != 0 {
		t.Errorf("WritableSubpaths missing entries: %v (got %v)", wantSub, e.WritableSubpaths)
	}
}

func TestCopilotInstallSystemPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed real ~/.copilot/{config.json,mcp-config.json,session-store.db}.
	userCopilot := filepath.Join(home, ".copilot")
	if err := os.MkdirAll(userCopilot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgPath := filepath.Join(userCopilot, "config.json")
	mcpPath := filepath.Join(userCopilot, "mcp-config.json")
	dbPath := filepath.Join(userCopilot, "session-store.db")
	if err := os.WriteFile(cfgPath, []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(mcpPath, []byte(`{"servers":[]}`), 0o600); err != nil {
		t.Fatalf("write mcp: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("sqlite-bytes"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}

	c := &Copilot{}
	prompt := "## Watchfire system prompt\n\nHello."
	if err := c.InstallSystemPromptForSession("proj:task:#0001", prompt); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "copilot-home", "proj:task:#0001")

	// AGENTS.md contents — exactly the composed prompt (no backend-specific
	// addendum, unlike Codex).
	got, err := os.ReadFile(filepath.Join(sessionHome, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(got) != prompt {
		t.Errorf("AGENTS.md = %q, want %q", got, prompt)
	}

	// Symlinks resolve to the user's real files.
	for _, pair := range []struct{ link, target string }{
		{filepath.Join(sessionHome, "config.json"), cfgPath},
		{filepath.Join(sessionHome, "mcp-config.json"), mcpPath},
		{filepath.Join(sessionHome, "session-store.db"), dbPath},
	} {
		target, err := os.Readlink(pair.link)
		if err != nil {
			t.Fatalf("readlink %s: %v", pair.link, err)
		}
		if target != pair.target {
			t.Errorf("symlink %s -> %s, want %s", pair.link, target, pair.target)
		}
	}

	// Running again is idempotent — stale symlinks replaced, AGENTS.md overwritten.
	prompt2 := "updated"
	if err := c.InstallSystemPromptForSession("proj:task:#0001", prompt2); err != nil {
		t.Fatalf("second install: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(sessionHome, "AGENTS.md"))
	if string(got) != prompt2 {
		t.Errorf("AGENTS.md after reinstall = %q, want %q", got, prompt2)
	}
	// Symlinks still valid after re-run (not duplicated or errored out).
	if target, err := os.Readlink(filepath.Join(sessionHome, "config.json")); err != nil || target != cfgPath {
		t.Errorf("config.json symlink after reinstall: target=%q err=%v", target, err)
	}
}

func TestCopilotInstallSystemPromptSkipsMissingAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No ~/.copilot dir at all — install should still succeed and just write AGENTS.md.
	c := &Copilot{}
	if err := c.InstallSystemPromptForSession("sess1", "P"); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "copilot-home", "sess1")
	if _, err := os.Stat(filepath.Join(sessionHome, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not written: %v", err)
	}
	// No symlinks should exist.
	for _, name := range []string{"config.json", "mcp-config.json", "session-store.db"} {
		if _, err := os.Lstat(filepath.Join(sessionHome, name)); err == nil {
			t.Errorf("%s symlink should not exist when target missing", name)
		}
	}
}

func TestCopilotInstallSystemPromptEmptySessionName(t *testing.T) {
	c := &Copilot{}
	if err := c.InstallSystemPromptForSession("", "P"); err == nil {
		t.Error("expected error on empty session name, got nil")
	}
}

func TestCopilotLocateTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessionHome := filepath.Join(home, ".watchfire", "copilot-home", "sess1")
	nested := filepath.Join(sessionHome, "session-state", "abc-123")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldFile := filepath.Join(nested, "events.jsonl")
	if err := os.WriteFile(oldFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}

	newDir := filepath.Join(sessionHome, "session-state", "def-456")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	newFile := filepath.Join(newDir, "events.jsonl")
	if err := os.WriteFile(newFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}

	// Make oldFile older so newFile wins on mtime.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	c := &Copilot{}
	got, err := c.LocateTranscript("/anywhere", time.Now(), "sess1")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if got != newFile {
		t.Errorf("LocateTranscript = %q, want %q", got, newFile)
	}
}

func TestCopilotLocateTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &Copilot{}
	_, err := c.LocateTranscript("/anywhere", time.Now(), "sess-missing")
	if err == nil {
		t.Error("expected error when rollout missing, got nil")
	}
}

func TestCopilotLocateTranscriptEmptySessionHint(t *testing.T) {
	c := &Copilot{}
	_, err := c.LocateTranscript("/anywhere", time.Now(), "")
	if err == nil {
		t.Error("expected error on empty sessionHint, got nil")
	}
}

func TestCopilotFormatTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	// Handcrafted JSONL — covers:
	//   - user message
	//   - assistant message with content blocks
	//   - tool-use event
	//   - reasoning event (must be skipped)
	//   - malformed line (must be skipped)
	//   - unknown-type event (must be skipped)
	//   - error event
	jsonl := strings.Join([]string{
		`{"type":"session.started"}`,
		`{"type":"item.message","role":"user","content":[{"type":"input_text","text":"hello"}]}`,
		`{"type":"item.message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}`,
		`{"type":"item.reasoning","text":"secret thought"}`,
		`{"type":"item.tool_use","name":"shell","command":"ls"}`,
		`this is not json`,
		`{"type":"some.unknown.event","foo":"bar"}`,
		`{"type":"error","text":"boom"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := &Copilot{}
	out, err := c.FormatTranscript(path)
	if err != nil {
		t.Fatalf("FormatTranscript: %v", err)
	}

	mustContain := []string{
		"## User\n\nhello",
		"## Assistant\n\nhi there",
		"[Tool: shell]",
		"## Error\n\nboom",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q; got:\n%s", s, out)
		}
	}
	if strings.Contains(out, "secret thought") {
		t.Errorf("reasoning leaked into output: %s", out)
	}
	if strings.Contains(out, "this is not json") {
		t.Errorf("malformed line leaked into output: %s", out)
	}
	if strings.Contains(out, "some.unknown.event") || strings.Contains(out, "\"foo\"") {
		t.Errorf("unknown event type leaked into output: %s", out)
	}
}

func TestCopilotDisplayName(t *testing.T) {
	c := &Copilot{}
	if c.Name() != CopilotBackendName {
		t.Errorf("Name = %q, want %q", c.Name(), CopilotBackendName)
	}
	if c.DisplayName() != "GitHub Copilot CLI" {
		t.Errorf("DisplayName = %q, want %q", c.DisplayName(), "GitHub Copilot CLI")
	}
}
