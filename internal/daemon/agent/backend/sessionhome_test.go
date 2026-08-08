package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSessionHomeProviders verifies every non-Claude backend exposes a
// per-session home that is a direct child of its home root, and that the
// Claude backend (CLI-flag prompt delivery) exposes none.
func TestSessionHomeProviders(t *testing.T) {
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)

	providers := map[string]struct {
		provider SessionHomeProvider
		rootDir  string
	}{
		CodexBackendName:    {&Codex{}, "codex-home"},
		CopilotBackendName:  {&Copilot{}, "copilot-home"},
		CursorBackendName:   {&Cursor{}, "cursor-home"},
		GeminiBackendName:   {&Gemini{}, "gemini-home"},
		OpencodeBackendName: {&Opencode{}, "opencode-home"},
	}

	for name, tc := range providers {
		provider, rootDir := tc.provider, tc.rootDir

		root, err := provider.SessionHomeRoot()
		if err != nil {
			t.Fatalf("%s: SessionHomeRoot: %v", name, err)
		}
		wantRoot := filepath.Join(home, ".watchfire", rootDir)
		if root != wantRoot {
			t.Errorf("%s: SessionHomeRoot = %q, want %q", name, root, wantRoot)
		}

		dir, err := provider.SessionHome("myproj:task:#0001-fix bug/x")
		if err != nil {
			t.Fatalf("%s: SessionHome: %v", name, err)
		}
		if filepath.Dir(dir) != root {
			t.Errorf("%s: SessionHome %q is not a direct child of root %q", name, dir, root)
		}
		// Awkward characters must be sanitized out of the leaf name.
		if base := filepath.Base(dir); base != "myproj:task:#0001-fix_bug_x" {
			t.Errorf("%s: sanitized leaf = %q, want %q", name, base, "myproj:task:#0001-fix_bug_x")
		}

		if _, err := provider.SessionHome(""); err == nil {
			t.Errorf("%s: SessionHome(\"\") should error", name)
		}
	}

	var claude Backend = &Claude{}
	if _, isProvider := claude.(SessionHomeProvider); isProvider {
		t.Error("claude backend should NOT implement SessionHomeProvider — it has no session home to clean")
	}
}
