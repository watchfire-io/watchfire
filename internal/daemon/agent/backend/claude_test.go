package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestClaudeBuildCommandArgs(t *testing.T) {
	c := &Claude{}

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
		t.Errorf("PasteInitialPrompt = true, want false (Claude embeds prompt in args)")
	}

	want := []string{
		"--name", "proj:task:#0001-foo",
		"--append-system-prompt", "SYS",
		"--dangerously-skip-permissions",
		"do the thing",
		"--flag",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args length = %d, want %d (got %v)", len(cmd.Args), len(want), cmd.Args)
	}
	for i, a := range want {
		if cmd.Args[i] != a {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], a)
		}
	}
}

func TestClaudeBuildCommandNoInitialPrompt(t *testing.T) {
	c := &Claude{}
	cmd, err := c.BuildCommand(CommandOpts{
		SessionName:  "proj:chat",
		SystemPrompt: "SYS",
	})
	if err != nil {
		t.Fatalf("BuildCommand error: %v", err)
	}
	want := []string{
		"--name", "proj:chat",
		"--append-system-prompt", "SYS",
		"--dangerously-skip-permissions",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", cmd.Args, want)
	}
}

func TestClaudeResolveExecutableFromSettings(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	c := &Claude{}
	s := &models.Settings{
		Agents: map[string]*models.AgentConfig{
			ClaudeBackendName: {Path: bin},
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

func TestClaudeResolveExecutableFallbackToPATH(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	if err := os.Setenv("PATH", dir); err != nil {
		t.Fatalf("setenv PATH: %v", err)
	}

	// Point HOME to an empty dir so fallbacks can't accidentally match.
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", t.TempDir())

	c := &Claude{}
	got, err := c.ResolveExecutable(nil)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestClaudeResolveExecutableFallbackHomeLocal(t *testing.T) {
	// Skip on darwin when /opt/homebrew/bin/claude or /usr/local/bin/claude
	// might exist — we can't guarantee the home fallback wins.
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/claude"); err == nil {
			t.Skip("skipping: /opt/homebrew/bin/claude exists and would be picked first")
		}
		if _, err := os.Stat("/usr/local/bin/claude"); err == nil {
			t.Skip("skipping: /usr/local/bin/claude exists and would be picked first")
		}
	}

	home := t.TempDir()
	localDir := filepath.Join(home, ".claude", "local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bin := filepath.Join(localDir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	_ = os.Setenv("PATH", t.TempDir()) // empty PATH dir, LookPath fails

	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	c := &Claude{}
	got, err := c.ResolveExecutable(nil)
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveExecutable = %q, want %q", got, bin)
	}
}

func TestClaudeResolveExecutableNotFound(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	_ = os.Setenv("PATH", t.TempDir())

	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", t.TempDir())

	// Skip on darwin if system-wide claude binaries are installed — can't
	// simulate "not installed" when /opt/homebrew or /usr/local has it.
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/opt/homebrew/bin/claude"); err == nil {
			t.Skip("skipping: system-wide /opt/homebrew/bin/claude exists")
		}
		if _, err := os.Stat("/usr/local/bin/claude"); err == nil {
			t.Skip("skipping: system-wide /usr/local/bin/claude exists")
		}
	}

	c := &Claude{}
	if _, err := c.ResolveExecutable(nil); err == nil {
		t.Fatal("expected error when claude not found, got nil")
	}
}

func TestClaudeSandboxExtras(t *testing.T) {
	c := &Claude{}
	e := c.SandboxExtras()
	if len(e.WritableSubpaths) == 0 || e.WritableSubpaths[0] != "~/.claude" {
		t.Errorf("WritableSubpaths = %v, want [~/.claude]", e.WritableSubpaths)
	}
	if len(e.WritableLiterals) == 0 || e.WritableLiterals[0] != "~/.claude.json" {
		t.Errorf("WritableLiterals = %v, want [~/.claude.json]", e.WritableLiterals)
	}
	if len(e.CachePatterns) == 0 {
		t.Errorf("CachePatterns empty, want cache entries")
	}
}

func TestClaudeInstallSystemPromptIsNoop(t *testing.T) {
	dir := t.TempDir()
	c := &Claude{}
	if err := c.InstallSystemPrompt(dir, "ignored"); err != nil {
		t.Errorf("InstallSystemPrompt returned error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("InstallSystemPrompt wrote %d entries into workDir, want 0", len(entries))
	}
}

// TestLocateTranscriptPicksLiveSessionAmongReusedNames: session names
// are reused across runs ("<slug>:chat" every time), so several
// transcripts can carry the same customTitle. The locator must pick the
// one touched during THIS session — returning an arbitrary match made
// watch mode replay a stale conversation and tail a dead file.
func TestLocateTranscriptPicksLiveSessionAmongReusedNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix paths")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := "/Users/x/proj"
	dir := filepath.Join(home, ".claude", "projects", "-Users-x-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, title string, mtime time.Time) string {
		t.Helper()
		p := filepath.Join(dir, name)
		line := `{"type":"custom-title","customTitle":"` + title + `"}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}

	started := time.Now()
	// Directory order puts the stale transcripts first — the old
	// first-match behavior returned one of them. The 30s-old file is
	// the sharp case: a predecessor session killed moments before this
	// one started (daemon restart) — its mtime is recent, but still
	// before `started`, so it must lose.
	write("aaaa-old.jsonl", "proj:chat", started.Add(-2*time.Hour))
	write("bbbb-just-died.jsonl", "proj:chat", started.Add(-30*time.Second))
	fresh := write("zzzz-live.jsonl", "proj:chat", started.Add(30*time.Second))
	write("mmmm-other.jsonl", "proj:task:#0001", started.Add(time.Minute))

	c := &Claude{}
	got, err := c.LocateTranscript(workDir, started, "proj:chat")
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if got != fresh {
		t.Fatalf("LocateTranscript = %q, want the live transcript %q", got, fresh)
	}

	// When every match predates the session, report not-found — the
	// tailer keeps retrying until the live file appears.
	if _, err := c.LocateTranscript(workDir, started.Add(3*time.Hour), "proj:chat"); err == nil {
		t.Fatalf("want not-found when all matches predate the session")
	}
}
