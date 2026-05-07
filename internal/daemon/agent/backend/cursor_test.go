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

func TestCursorBuildCommandArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &Cursor{}
	cmd, err := c.BuildCommand(CommandOpts{
		SessionName:   "proj:task:#0001-foo",
		SystemPrompt:  "SYS",
		InitialPrompt: "do the thing",
		ExtraArgs:     []string{"--model", "gpt-5.2"},
	})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if cmd.PasteInitialPrompt {
		t.Errorf("PasteInitialPrompt = true, want false")
	}

	want := []string{
		"--force",
		"--model", "gpt-5.2",
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

	expectedHome := filepath.Join(home, ".watchfire", "cursor-home", "proj:task:#0001-foo")
	wantEnv := map[string]bool{
		"CURSOR_HOME=" + expectedHome: true,
	}
	for _, e := range cmd.Env {
		delete(wantEnv, e)
	}
	if len(wantEnv) != 0 {
		t.Errorf("Env missing entries: %v; got %v", wantEnv, cmd.Env)
	}
}

func TestCursorBuildCommandNoInitialPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &Cursor{}
	cmd, err := c.BuildCommand(CommandOpts{SessionName: "proj:chat"})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--force" {
		t.Errorf("Args = %v, want [--force]", cmd.Args)
	}
	for _, a := range cmd.Args {
		if a == "-p" {
			t.Errorf("expected no -p flag when InitialPrompt is empty, got args %v", cmd.Args)
		}
	}
}

func TestCursorResolveExecutableFromSettings(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "cursor-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	c := &Cursor{}
	s := &models.Settings{
		Agents: map[string]*models.AgentConfig{
			CursorBackendName: {Path: bin},
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

func TestCursorResolveExecutableFromPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "cursor-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())

	c := &Cursor{}
	got, err := c.ResolveExecutable(nil)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestCursorResolveExecutableFromFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", home)

	// Seed ~/.local/bin/cursor-agent as the fallback.
	fallbackDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bin := filepath.Join(fallbackDir, "cursor-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := &Cursor{}
	got, err := c.ResolveExecutable(nil)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestCursorResolveExecutableNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/cursor-agent"); err == nil {
			t.Skip("skipping: system-wide /opt/homebrew/bin/cursor-agent exists")
		}
		if _, err := os.Stat("/usr/local/bin/cursor-agent"); err == nil {
			t.Skip("skipping: system-wide /usr/local/bin/cursor-agent exists")
		}
	}

	c := &Cursor{}
	_, err := c.ResolveExecutable(nil)
	if err == nil {
		t.Fatal("expected error when cursor-agent not found, got nil")
	}
	// Error message should mention both install channels so users can act on it.
	msg := err.Error()
	if !strings.Contains(msg, "cursor.com/install") {
		t.Errorf("error does not mention install script: %q", msg)
	}
	if !strings.Contains(msg, "brew install cursor-agent") {
		t.Errorf("error does not mention `brew install cursor-agent`: %q", msg)
	}
}

func TestCursorSandboxExtras(t *testing.T) {
	c := &Cursor{}
	e := c.SandboxExtras()
	wantSub := map[string]bool{
		"~/.watchfire/cursor-home": true,
		"~/.cursor":                true,
	}
	for _, p := range e.WritableSubpaths {
		delete(wantSub, p)
	}
	if len(wantSub) != 0 {
		t.Errorf("WritableSubpaths missing entries: %v (got %v)", wantSub, e.WritableSubpaths)
	}
}

func TestCursorInstallSystemPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed real ~/.cursor/{cli-config.json,mcp.json,permissions.json}.
	userCursor := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(userCursor, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgPath := filepath.Join(userCursor, "cli-config.json")
	mcpPath := filepath.Join(userCursor, "mcp.json")
	permsPath := filepath.Join(userCursor, "permissions.json")
	if err := os.WriteFile(cfgPath, []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatalf("write mcp: %v", err)
	}
	if err := os.WriteFile(permsPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write perms: %v", err)
	}

	c := &Cursor{}
	prompt := "## Watchfire system prompt\n\nHello."
	if err := c.InstallSystemPromptForSession("proj:task:#0001", prompt); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "cursor-home", "proj:task:#0001")

	// AGENTS.md contents — exactly the composed prompt (no backend-specific addendum).
	got, err := os.ReadFile(filepath.Join(sessionHome, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(got) != prompt {
		t.Errorf("AGENTS.md = %q, want %q", got, prompt)
	}

	// Symlinks resolve to the user's real files.
	for _, pair := range []struct{ link, target string }{
		{filepath.Join(sessionHome, "cli-config.json"), cfgPath},
		{filepath.Join(sessionHome, "mcp.json"), mcpPath},
		{filepath.Join(sessionHome, "permissions.json"), permsPath},
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
	if target, err := os.Readlink(filepath.Join(sessionHome, "cli-config.json")); err != nil || target != cfgPath {
		t.Errorf("cli-config.json symlink after reinstall: target=%q err=%v", target, err)
	}
}

func TestCursorInstallSystemPromptSkipsMissingAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No ~/.cursor dir at all — install should still succeed and just write AGENTS.md.
	c := &Cursor{}
	if err := c.InstallSystemPromptForSession("sess1", "P"); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "cursor-home", "sess1")
	if _, err := os.Stat(filepath.Join(sessionHome, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not written: %v", err)
	}
	// No symlinks should exist.
	for _, name := range []string{"cli-config.json", "mcp.json", "permissions.json"} {
		if _, err := os.Lstat(filepath.Join(sessionHome, name)); err == nil {
			t.Errorf("%s symlink should not exist when target missing", name)
		}
	}
}

func TestCursorInstallSystemPromptEmptySessionName(t *testing.T) {
	c := &Cursor{}
	if err := c.InstallSystemPromptForSession("", "P"); err == nil {
		t.Error("expected error on empty session name, got nil")
	}
}

func TestCursorLocateTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessionHome := filepath.Join(home, ".watchfire", "cursor-home", "sess1")
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

	c := &Cursor{}
	got, err := c.LocateTranscript("/anywhere", time.Now(), "sess1")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if got != newFile {
		t.Errorf("LocateTranscript = %q, want %q", got, newFile)
	}
}

func TestCursorLocateTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &Cursor{}
	_, err := c.LocateTranscript("/anywhere", time.Now(), "sess-missing")
	if err == nil {
		t.Error("expected error when rollout missing, got nil")
	}
}

func TestCursorLocateTranscriptEmptySessionHint(t *testing.T) {
	c := &Cursor{}
	_, err := c.LocateTranscript("/anywhere", time.Now(), "")
	if err == nil {
		t.Error("expected error on empty sessionHint, got nil")
	}
}

func TestCursorFormatTranscript(t *testing.T) {
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

	c := &Cursor{}
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

func TestCursorDisplayName(t *testing.T) {
	c := &Cursor{}
	if c.Name() != CursorBackendName {
		t.Errorf("Name = %q, want %q", c.Name(), CursorBackendName)
	}
	if c.DisplayName() != "Cursor Agent CLI" {
		t.Errorf("DisplayName = %q, want %q", c.DisplayName(), "Cursor Agent CLI")
	}
}
