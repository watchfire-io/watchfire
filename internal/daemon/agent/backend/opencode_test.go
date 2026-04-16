package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestOpencodeBuildCommandArgs(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	o := &Opencode{}
	cmd, err := o.BuildCommand(CommandOpts{
		SessionName:   "proj:task:#0001-foo",
		SystemPrompt:  "SYS",
		InitialPrompt: "do the thing",
		ExtraArgs:     []string{"--model", "anthropic/claude-3-5-sonnet"},
	})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if cmd.PasteInitialPrompt {
		t.Errorf("PasteInitialPrompt = true, want false")
	}

	want := []string{
		"run",
		"--model", "anthropic/claude-3-5-sonnet",
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

	expectedCfg := filepath.Join(home, ".watchfire", "opencode-home", "proj:task:#0001-foo", "config")
	expectedData := filepath.Join(home, ".watchfire", "opencode-home", "proj:task:#0001-foo", "data")
	envWants := map[string]bool{
		"OPENCODE_CONFIG_DIR=" + expectedCfg:   true,
		"OPENCODE_DATA_DIR=" + expectedData:    true,
		`OPENCODE_PERMISSION={"*":"allow"}`:    true,
	}
	for _, e := range cmd.Env {
		delete(envWants, e)
	}
	if len(envWants) != 0 {
		t.Errorf("Env missing entries: %v; got %v", envWants, cmd.Env)
	}
}

func TestOpencodeBuildCommandNoInitialPrompt(t *testing.T) {
	o := &Opencode{}
	cmd, err := o.BuildCommand(CommandOpts{SessionName: "proj:chat"})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "run" {
		t.Errorf("Args = %v, want [run]", cmd.Args)
	}
}

func TestOpencodeResolveExecutableFromSettings(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	o := &Opencode{}
	s := &models.Settings{
		Agents: map[string]*models.AgentConfig{
			OpencodeBackendName: {Path: bin},
		},
	}

	got, err := o.ResolveExecutable(s)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestOpencodeResolveExecutableNotFound(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	_ = os.Setenv("PATH", t.TempDir())

	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", t.TempDir())

	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/opencode"); err == nil {
			t.Skip("skipping: system-wide /opt/homebrew/bin/opencode exists")
		}
		if _, err := os.Stat("/usr/local/bin/opencode"); err == nil {
			t.Skip("skipping: system-wide /usr/local/bin/opencode exists")
		}
	}

	o := &Opencode{}
	if _, err := o.ResolveExecutable(nil); err == nil {
		t.Fatal("expected error when opencode not found, got nil")
	}
}

func TestOpencodeSandboxExtras(t *testing.T) {
	o := &Opencode{}
	e := o.SandboxExtras()
	if len(e.WritableSubpaths) == 0 || e.WritableSubpaths[0] != "~/.watchfire/opencode-home" {
		t.Errorf("WritableSubpaths = %v, want [~/.watchfire/opencode-home]", e.WritableSubpaths)
	}
	wantCaches := map[string]bool{"~/.config/opencode": true, "~/.local/share/opencode": true}
	for _, c := range e.CachePatterns {
		delete(wantCaches, c)
	}
	if len(wantCaches) != 0 {
		t.Errorf("CachePatterns missing entries: %v (got %v)", wantCaches, e.CachePatterns)
	}
}

func TestOpencodeInstallSystemPrompt(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	// Seed a real ~/.config/opencode with an auth file, a directory, and an
	// existing opencode.json that carries a model preference we want to
	// preserve.
	userCfg := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(filepath.Join(userCfg, "auth"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authFile := filepath.Join(userCfg, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	userOpencodeJSON := filepath.Join(userCfg, "opencode.json")
	if err := os.WriteFile(userOpencodeJSON, []byte(`{"model":"anthropic/claude-3-5-sonnet","permission":"ask"}`), 0o644); err != nil {
		t.Fatalf("write opencode.json: %v", err)
	}

	o := &Opencode{}
	prompt := "## Watchfire system prompt\n\nHello."
	if err := o.InstallSystemPromptForSession("proj:task:#0001", prompt); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	cfgDir := filepath.Join(home, ".watchfire", "opencode-home", "proj:task:#0001", "config")
	dataDir := filepath.Join(home, ".watchfire", "opencode-home", "proj:task:#0001", "data")

	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data dir not created: %v", err)
	}

	// AGENTS.md must contain our composed prompt.
	got, err := os.ReadFile(filepath.Join(cfgDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(got) != prompt {
		t.Errorf("AGENTS.md = %q, want %q", got, prompt)
	}

	// Symlinked auth.json resolves to the user's real file.
	authLink := filepath.Join(cfgDir, "auth.json")
	target, err := os.Readlink(authLink)
	if err != nil {
		t.Fatalf("readlink auth: %v", err)
	}
	if target != authFile {
		t.Errorf("auth symlink target = %q, want %q", target, authFile)
	}

	// opencode.json must have permission=allow but preserve the model field.
	data, err := os.ReadFile(filepath.Join(cfgDir, "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse opencode.json: %v", err)
	}
	if cfg["permission"] != "allow" {
		t.Errorf("permission = %v, want \"allow\"", cfg["permission"])
	}
	if cfg["model"] != "anthropic/claude-3-5-sonnet" {
		t.Errorf("model = %v, want model preserved from user config", cfg["model"])
	}
	if cfg["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v, want schema set", cfg["$schema"])
	}

	// Running again is idempotent — overwrites cleanly.
	prompt2 := "updated"
	if err := o.InstallSystemPromptForSession("proj:task:#0001", prompt2); err != nil {
		t.Fatalf("second install: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(cfgDir, "AGENTS.md"))
	if string(got) != prompt2 {
		t.Errorf("AGENTS.md after reinstall = %q, want %q", got, prompt2)
	}
}

func TestOpencodeInstallSystemPromptNoUserConfig(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	// No ~/.config/opencode at all — install still succeeds and just
	// writes AGENTS.md + opencode.json.
	o := &Opencode{}
	if err := o.InstallSystemPromptForSession("sess1", "P"); err != nil {
		t.Fatalf("InstallSystemPromptForSession: %v", err)
	}

	cfgDir := filepath.Join(home, ".watchfire", "opencode-home", "sess1", "config")
	if _, err := os.Stat(filepath.Join(cfgDir, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "opencode.json")); err != nil {
		t.Errorf("opencode.json not written: %v", err)
	}
	// No symlinks should exist.
	if _, err := os.Lstat(filepath.Join(cfgDir, "auth.json")); err == nil {
		t.Errorf("auth.json symlink should not exist when user config missing")
	}
}

func TestOpencodeLocateTranscript(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	dataDir := filepath.Join(home, ".watchfire", "opencode-home", "sess1", "data")
	msgDir := filepath.Join(dataDir, "storage", "message", "ses_01J000")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write two messages with lexically ordered IDs so order is predictable.
	m1 := filepath.Join(msgDir, "msg_01J000A.json")
	m2 := filepath.Join(msgDir, "msg_01J000B.json")
	if err := os.WriteFile(m1, []byte(`{
  "role": "user",
  "parts": [{"type":"text","text":"hi"}]
}`), 0o644); err != nil {
		t.Fatalf("write m1: %v", err)
	}
	if err := os.WriteFile(m2, []byte(`{"role":"assistant","parts":[{"type":"text","text":"hello"}]}`), 0o644); err != nil {
		t.Fatalf("write m2: %v", err)
	}

	o := &Opencode{}
	got, err := o.LocateTranscript("/anywhere", time.Now(), "sess1")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	want := filepath.Join(home, ".watchfire", "opencode-home", "sess1", "transcript.jsonl")
	if got != want {
		t.Errorf("LocateTranscript = %q, want %q", got, want)
	}
	// Synthesized JSONL must have exactly two lines, one per message, each
	// a self-contained JSON object.
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("transcript has %d lines, want 2:\n%s", len(lines), data)
	}
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v (%q)", i, err, line)
		}
	}
}

func TestOpencodeLocateTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	o := &Opencode{}
	if _, err := o.LocateTranscript("/anywhere", time.Now(), "sess-missing"); err == nil {
		t.Error("expected error when messages missing, got nil")
	}
}

func TestOpencodeFormatTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	// Handcrafted JSONL covering: user text, assistant text, assistant tool
	// call, reasoning (should be skipped), tool result (should be skipped).
	jsonl := strings.Join([]string{
		`{"role":"user","parts":[{"type":"text","text":"hello"}]}`,
		`{"role":"assistant","parts":[{"type":"text","text":"hi there"}]}`,
		`{"role":"assistant","parts":[{"type":"reasoning","text":"secret thought"}]}`,
		`{"role":"assistant","parts":[{"type":"tool","name":"bash","command":"ls"}]}`,
		`{"role":"assistant","parts":[{"type":"tool_result","text":"output"}]}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	o := &Opencode{}
	out, err := o.FormatTranscript(path)
	if err != nil {
		t.Fatalf("FormatTranscript: %v", err)
	}

	mustContain := []string{
		"## User\n\nhello",
		"## Assistant\n\nhi there",
		"[Tool: bash]",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q; got:\n%s", s, out)
		}
	}
	if strings.Contains(out, "secret thought") {
		t.Errorf("reasoning leaked into output: %s", out)
	}
	if strings.Contains(out, "output") {
		t.Errorf("tool_result leaked into output: %s", out)
	}
}

func TestOpencodeDisplayName(t *testing.T) {
	o := &Opencode{}
	if o.Name() != OpencodeBackendName {
		t.Errorf("Name = %q, want %q", o.Name(), OpencodeBackendName)
	}
	if o.DisplayName() == "" {
		t.Errorf("DisplayName empty")
	}
}
