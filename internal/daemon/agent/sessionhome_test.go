package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
	_ = os.Setenv("HOME", home)
	return home
}

// mkSessionDir creates ~/.watchfire/<agentHome>/<name> with a marker file.
func mkSessionDir(t *testing.T, home, agentHome, name string) string {
	t.Helper()
	dir := filepath.Join(home, ".watchfire", agentHome, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCleanupSessionHome(t *testing.T) {
	home := setTestHome(t)
	dir := mkSessionDir(t, home, "codex-home", "myproj:chat")
	keep := mkSessionDir(t, home, "codex-home", "myproj:task:#0001-x")

	cleanupSessionHome("pid", "codex", "myproj:chat")

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("session home %s should have been removed", dir)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("unrelated session home %s should survive: %v", keep, err)
	}

	// No-ops must not panic or remove anything.
	cleanupSessionHome("pid", "claude-code", "myproj:chat")
	cleanupSessionHome("pid", "codex", "")
	cleanupSessionHome("pid", "no-such-backend", "myproj:chat")
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("session home %s should still survive no-op calls: %v", keep, err)
	}
}

func TestCleanupProjectSessionHomes(t *testing.T) {
	home := setTestHome(t)

	gone1 := mkSessionDir(t, home, "codex-home", "my-project:chat")
	gone2 := mkSessionDir(t, home, "codex-home", "my-project:task:#0001-fix")
	gone3 := mkSessionDir(t, home, "opencode-home", "my-project:chat")
	// Shares the "my-project" prefix but is a different slug — the ':'
	// terminator must keep it safe.
	keep := mkSessionDir(t, home, "codex-home", "my-project-two:chat")

	m := NewManager()
	m.CleanupProjectSessionHomes("My Project")

	for _, dir := range []string{gone1, gone2, gone3} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("dir %s should have been removed", dir)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("dir %s belongs to another project and should survive: %v", keep, err)
	}
}

func TestCleanupProjectSessionHomesSkipsActiveSession(t *testing.T) {
	home := setTestHome(t)

	running := mkSessionDir(t, home, "codex-home", "my-project:chat")
	stale := mkSessionDir(t, home, "codex-home", "my-project:task:#0002-y")

	m := NewManager()
	m.agents["other-project-id"] = &RunningAgent{
		BackendName: "codex",
		SessionName: "my-project:chat",
	}
	m.CleanupProjectSessionHomes("My Project")

	if _, err := os.Stat(running); err != nil {
		t.Errorf("active session dir %s should survive the sweep: %v", running, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale dir %s should have been removed", stale)
	}
}

func TestIsDirectChildOf(t *testing.T) {
	root := filepath.Join("/home", "u", ".watchfire", "codex-home")
	cases := []struct {
		dir  string
		want bool
	}{
		{filepath.Join(root, "proj:chat"), true},
		{root, false},
		{filepath.Join(root, "a", "b"), false},
		{filepath.Join("/home", "u", ".watchfire"), false},
		{"", false},
	}
	for _, c := range cases {
		if got := isDirectChildOf(root, c.dir); got != c.want {
			t.Errorf("isDirectChildOf(%q, %q) = %v, want %v", root, c.dir, got, c.want)
		}
	}
}
