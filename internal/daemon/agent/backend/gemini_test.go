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

func TestGeminiBuildCommandArgs(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	g := &Gemini{}
	cmd, err := g.BuildCommand(CommandOpts{
		SessionName:   "proj:task:#0001-foo",
		SystemPrompt:  "SYS",
		InitialPrompt: "do the thing",
		ExtraArgs:     []string{"--model", "gemini-2.5-pro"},
	})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if cmd.PasteInitialPrompt {
		t.Errorf("PasteInitialPrompt = true, want false")
	}

	want := []string{
		"--yolo",
		"--prompt", "do the thing",
		"--model", "gemini-2.5-pro",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", cmd.Args, want)
	}
	for i, a := range want {
		if cmd.Args[i] != a {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], a)
		}
	}

	expected := filepath.Join(home, ".watchfire", "gemini-home", "proj:task:#0001-foo", "system.md")
	envKey := "GEMINI_SYSTEM_MD=" + expected
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

func TestGeminiBuildCommandNoInitialPrompt(t *testing.T) {
	g := &Gemini{}
	cmd, err := g.BuildCommand(CommandOpts{SessionName: "proj:chat"})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--yolo" {
		t.Errorf("Args = %v, want [--yolo]", cmd.Args)
	}
}

func TestGeminiResolveExecutableFromSettings(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gemini")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	g := &Gemini{}
	s := &models.Settings{
		Agents: map[string]*models.AgentConfig{
			GeminiBackendName: {Path: bin},
		},
	}

	got, err := g.ResolveExecutable(s)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestGeminiResolveExecutableNotFound(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	_ = os.Setenv("PATH", t.TempDir())

	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", t.TempDir())

	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/gemini"); err == nil {
			t.Skip("skipping: system-wide /opt/homebrew/bin/gemini exists")
		}
		if _, err := os.Stat("/usr/local/bin/gemini"); err == nil {
			t.Skip("skipping: system-wide /usr/local/bin/gemini exists")
		}
	}

	g := &Gemini{}
	if _, err := g.ResolveExecutable(nil); err == nil {
		t.Fatal("expected error when gemini not found, got nil")
	}
}

func TestGeminiSandboxExtras(t *testing.T) {
	g := &Gemini{}
	e := g.SandboxExtras()
	if len(e.WritableSubpaths) == 0 || e.WritableSubpaths[0] != "~/.watchfire/gemini-home" {
		t.Errorf("WritableSubpaths = %v, want [~/.watchfire/gemini-home]", e.WritableSubpaths)
	}
	wantCaches := map[string]bool{"~/.gemini": true, "~/.config/gcloud": true}
	for _, c := range e.CachePatterns {
		delete(wantCaches, c)
	}
	if len(wantCaches) != 0 {
		t.Errorf("CachePatterns missing entries: %v (got %v)", wantCaches, e.CachePatterns)
	}
}

func TestGeminiInstallSystemPrompt(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	g := &Gemini{}
	prompt := "## Watchfire system prompt\n\nHello."
	if err := g.InstallSystemPromptForSession("proj:task:#0001", prompt); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	sessionHome := filepath.Join(home, ".watchfire", "gemini-home", "proj:task:#0001")
	got, err := os.ReadFile(filepath.Join(sessionHome, "system.md"))
	if err != nil {
		t.Fatalf("read system.md: %v", err)
	}
	if string(got) != prompt {
		t.Errorf("system.md = %q, want %q", got, prompt)
	}

	// Running again overwrites cleanly.
	prompt2 := "updated"
	if err := g.InstallSystemPromptForSession("proj:task:#0001", prompt2); err != nil {
		t.Fatalf("second install: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(sessionHome, "system.md"))
	if string(got) != prompt2 {
		t.Errorf("system.md after reinstall = %q, want %q", got, prompt2)
	}
}

func TestGeminiInstallSystemPromptEmptySessionName(t *testing.T) {
	g := &Gemini{}
	if err := g.InstallSystemPromptForSession("", "P"); err == nil {
		t.Error("expected error on empty session name, got nil")
	}
}

func TestGeminiLocateTranscriptJSONL(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	tmpRoot := filepath.Join(home, ".gemini", "tmp", "abc123", "chats")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldFile := filepath.Join(tmpRoot, "session-old.jsonl")
	newFile := filepath.Join(tmpRoot, "session-new.jsonl")
	if err := os.WriteFile(oldFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	g := &Gemini{}
	// started one hour ago — oldFile is too old, newFile qualifies.
	got, err := g.LocateTranscript("/anywhere", time.Now().Add(-time.Hour), "sess1")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if got != newFile {
		t.Errorf("LocateTranscript = %q, want %q", got, newFile)
	}
}

func TestGeminiLocateTranscriptLegacyLogs(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	tmpRoot := filepath.Join(home, ".gemini", "tmp", "abc123")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logs := filepath.Join(tmpRoot, "logs.json")
	if err := os.WriteFile(logs, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	g := &Gemini{}
	got, err := g.LocateTranscript("/anywhere", time.Now().Add(-time.Minute), "sess1")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if got != logs {
		t.Errorf("LocateTranscript = %q, want %q", got, logs)
	}
}

func TestGeminiLocateTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	g := &Gemini{}
	if _, err := g.LocateTranscript("/anywhere", time.Now(), "sess-missing"); err == nil {
		t.Error("expected error when tmp root missing, got nil")
	}
}

func TestGeminiLocateTranscriptSkipsStaleFiles(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	tmpRoot := filepath.Join(home, ".gemini", "tmp", "abc", "chats")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(tmpRoot, "session-stale.jsonl")
	if err := os.WriteFile(stale, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	past := time.Now().Add(-24 * time.Hour)
	_ = os.Chtimes(stale, past, past)

	g := &Gemini{}
	_, err := g.LocateTranscript("/anywhere", time.Now(), "sess1")
	if err == nil {
		t.Error("expected error when all files are older than started, got nil")
	}
}

func TestGeminiFormatTranscriptJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// Handcrafted JSONL covering:
	//   - session_metadata (must be skipped)
	//   - user text (via parts[].text)
	//   - assistant text (role "model")
	//   - assistant thought (must be skipped)
	//   - assistant functionCall (must render as [Tool: ...])
	//   - user functionResponse (must be skipped)
	jsonl := strings.Join([]string{
		`{"type":"session_metadata","sessionId":"s","startTime":"2026-04-16T00:00:00Z"}`,
		`{"role":"user","parts":[{"text":"hello"}]}`,
		`{"role":"model","parts":[{"text":"hi there"}]}`,
		`{"role":"model","parts":[{"text":"secret thought","thought":true}]}`,
		`{"role":"model","parts":[{"functionCall":{"name":"run_shell_command","args":{"command":"ls"}}}]}`,
		`{"role":"user","parts":[{"functionResponse":{"name":"run_shell_command"}}]}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	g := &Gemini{}
	out, err := g.FormatTranscript(path)
	if err != nil {
		t.Fatalf("FormatTranscript: %v", err)
	}

	mustContain := []string{
		"## User\n\nhello",
		"## Assistant\n\nhi there",
		"[Tool: run_shell_command]",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q; got:\n%s", s, out)
		}
	}
	if strings.Contains(out, "secret thought") {
		t.Errorf("reasoning leaked into output: %s", out)
	}
	if strings.Contains(out, "session_metadata") || strings.Contains(out, "startTime") {
		t.Errorf("session_metadata leaked into output: %s", out)
	}
	if strings.Contains(out, "functionResponse") {
		t.Errorf("functionResponse leaked into output: %s", out)
	}
}

func TestGeminiFormatTranscriptLegacyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.json")
	// Legacy JSON-array schema.
	arr := `[
  {"role":"user","parts":[{"text":"hey"}]},
  {"role":"model","parts":[{"text":"hola"}]}
]`
	if err := os.WriteFile(path, []byte(arr), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	g := &Gemini{}
	out, err := g.FormatTranscript(path)
	if err != nil {
		t.Fatalf("FormatTranscript: %v", err)
	}
	if !strings.Contains(out, "## User\n\nhey") {
		t.Errorf("missing user line: %s", out)
	}
	if !strings.Contains(out, "## Assistant\n\nhola") {
		t.Errorf("missing assistant line: %s", out)
	}
}

func TestGeminiDisplayName(t *testing.T) {
	g := &Gemini{}
	if g.Name() != GeminiBackendName {
		t.Errorf("Name = %q, want %q", g.Name(), GeminiBackendName)
	}
	if g.DisplayName() != "Gemini CLI" {
		t.Errorf("DisplayName = %q, want %q", g.DisplayName(), "Gemini CLI")
	}
}
