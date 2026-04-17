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

func TestCodexBuildCommandArgs(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	c := &Codex{}
	cmd, err := c.BuildCommand(CommandOpts{
		SessionName:   "proj:task:#0001-foo",
		SystemPrompt:  "SYS",
		InitialPrompt: "do the thing",
		ExtraArgs:     []string{"--flag"},
	})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if cmd.PasteInitialPrompt {
		t.Errorf("PasteInitialPrompt = true, want false")
	}

	want := []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"--flag",
		"do the thing",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", cmd.Args, want)
	}
	for i, a := range want {
		if cmd.Args[i] != a {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], a)
		}
	}

	expectedHome := filepath.Join(home, ".watchfire", "codex-home", "proj:task:#0001-foo")
	envKey := "CODEX_HOME=" + expectedHome
	found := false
	for _, e := range cmd.Env {
		if e == envKey {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Env missing %q; got %v", envKey, cmd.Env)
	}
}

func TestCodexBuildCommandNoInitialPrompt(t *testing.T) {
	c := &Codex{}
	cmd, err := c.BuildCommand(CommandOpts{SessionName: "proj:chat"})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--dangerously-bypass-approvals-and-sandbox" {
		t.Errorf("Args = %v, want [--dangerously-bypass-approvals-and-sandbox]", cmd.Args)
	}
}

func TestCodexResolveExecutableFromSettings(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	c := &Codex{}
	s := &models.Settings{
		Agents: map[string]*models.AgentConfig{
			CodexBackendName: {Path: bin},
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

func TestCodexResolveExecutableNotFound(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	_ = os.Setenv("PATH", t.TempDir())

	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", t.TempDir())

	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/codex"); err == nil {
			t.Skip("skipping: system-wide /opt/homebrew/bin/codex exists")
		}
		if _, err := os.Stat("/usr/local/bin/codex"); err == nil {
			t.Skip("skipping: system-wide /usr/local/bin/codex exists")
		}
	}

	c := &Codex{}
	if _, err := c.ResolveExecutable(nil); err == nil {
		t.Fatal("expected error when codex not found, got nil")
	}
}

func TestCodexSandboxExtras(t *testing.T) {
	c := &Codex{}
	e := c.SandboxExtras()
	wantSub := map[string]bool{"~/.watchfire/codex-home": true, "~/.codex": true}
	for _, p := range e.WritableSubpaths {
		delete(wantSub, p)
	}
	if len(wantSub) != 0 {
		t.Errorf("WritableSubpaths missing entries: %v (got %v)", wantSub, e.WritableSubpaths)
	}
}

func TestCodexInstallSystemPrompt(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	// Seed real ~/.codex/{auth.json,config.toml}.
	userCodex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(userCodex, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authPath := filepath.Join(userCodex, "auth.json")
	cfgPath := filepath.Join(userCodex, "config.toml")
	if err := os.WriteFile(authPath, []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("key = 1\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c := &Codex{}
	prompt := "## Watchfire system prompt\n\nHello."
	if err := c.InstallSystemPromptForSession("proj:task:#0001", prompt); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "codex-home", "proj:task:#0001")

	// AGENTS.md contents — composed prompt followed by the codex commit addendum.
	got, err := os.ReadFile(filepath.Join(sessionHome, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.HasPrefix(string(got), prompt) {
		t.Errorf("AGENTS.md does not start with composed prompt; got %q", got)
	}
	if !strings.Contains(string(got), "CRITICAL: Commit before marking a task done") {
		t.Errorf("AGENTS.md missing codex commit addendum; got %q", got)
	}

	// auth.json symlink resolves to the user's real auth file.
	authLink := filepath.Join(sessionHome, "auth.json")
	target, err := os.Readlink(authLink)
	if err != nil {
		t.Fatalf("readlink auth: %v", err)
	}
	if target != authPath {
		t.Errorf("auth symlink target = %q, want %q", target, authPath)
	}

	// config.toml symlink likewise.
	cfgLink := filepath.Join(sessionHome, "config.toml")
	target, err = os.Readlink(cfgLink)
	if err != nil {
		t.Fatalf("readlink config: %v", err)
	}
	if target != cfgPath {
		t.Errorf("config symlink target = %q, want %q", target, cfgPath)
	}

	// Running again is idempotent (stale symlinks replaced, file overwritten).
	prompt2 := "updated"
	if err := c.InstallSystemPromptForSession("proj:task:#0001", prompt2); err != nil {
		t.Fatalf("second install: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(sessionHome, "AGENTS.md"))
	if !strings.HasPrefix(string(got), prompt2) {
		t.Errorf("AGENTS.md after reinstall does not start with %q; got %q", prompt2, got)
	}
	if !strings.Contains(string(got), "CRITICAL: Commit before marking a task done") {
		t.Errorf("AGENTS.md after reinstall missing codex commit addendum; got %q", got)
	}
}

func TestCodexInstallSystemPromptSkipsMissingAuth(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	// No ~/.codex dir at all — install should still succeed and just write AGENTS.md.
	c := &Codex{}
	if err := c.InstallSystemPromptForSession("sess1", "P"); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "codex-home", "sess1")
	if _, err := os.Stat(filepath.Join(sessionHome, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not written: %v", err)
	}
	// No symlinks should exist.
	if _, err := os.Lstat(filepath.Join(sessionHome, "auth.json")); err == nil {
		t.Errorf("auth.json symlink should not exist when target missing")
	}
}

func TestCodexLocateTranscript(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	sessionHome := filepath.Join(home, ".watchfire", "codex-home", "sess1")
	nested := filepath.Join(sessionHome, "sessions", "2026", "04", "16")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldFile := filepath.Join(nested, "rollout-old.jsonl")
	newFile := filepath.Join(nested, "rollout-new.jsonl")
	if err := os.WriteFile(oldFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	// Ensure newFile has a later mtime than oldFile.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	c := &Codex{}
	got, err := c.LocateTranscript("/anywhere", time.Now(), "sess1")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if got != newFile {
		t.Errorf("LocateTranscript = %q, want %q", got, newFile)
	}
}

func TestCodexLocateTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	c := &Codex{}
	if _, err := c.LocateTranscript("/anywhere", time.Now(), "sess-missing"); err == nil {
		t.Error("expected error when rollout missing, got nil")
	}
}

func TestCodexFormatTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	// Handcrafted JSONL — covers user message, assistant message with content
	// blocks, tool use, reasoning (should be skipped), and an error.
	jsonl := strings.Join([]string{
		`{"type":"thread.started"}`,
		`{"type":"item.message","role":"user","content":[{"type":"input_text","text":"hello"}]}`,
		`{"type":"item.message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}`,
		`{"type":"item.reasoning","text":"secret thought"}`,
		`{"type":"item.tool_use","name":"shell","command":"ls"}`,
		`{"type":"turn.completed"}`,
		`{"type":"error","text":"boom"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := &Codex{}
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
}

func TestCodexDisplayName(t *testing.T) {
	c := &Codex{}
	if c.Name() != CodexBackendName {
		t.Errorf("Name = %q, want %q", c.Name(), CodexBackendName)
	}
	if c.DisplayName() == "" {
		t.Errorf("DisplayName empty")
	}
}
