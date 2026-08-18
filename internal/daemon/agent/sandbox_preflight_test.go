//go:build darwin

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempHome creates a resolved temp home dir (macOS TempDir lives behind the
// /var → /private/var symlink; resolving keeps string comparisons honest).
func tempHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCheckProjectPath(t *testing.T) {
	home := tempHome(t)

	tests := []struct {
		name       string
		path       string
		wantDenied bool
		wantRoot   string // expected Display when denied
	}{
		{"desktop root itself", filepath.Join(home, "Desktop"), true, "~/Desktop"},
		{"nested under desktop", filepath.Join(home, "Desktop", "projects", "app"), true, "~/Desktop"},
		{"documents", filepath.Join(home, "Documents", "proj"), true, "~/Documents"},
		{"downloads", filepath.Join(home, "Downloads", "proj"), true, "~/Downloads"},
		{"pictures", filepath.Join(home, "Pictures", "proj"), true, "~/Pictures"},
		{"ssh credential dir", filepath.Join(home, ".ssh", "proj"), true, "~/.ssh"},
		{"allowed under source", filepath.Join(home, "source", "proj"), false, ""},
		{"home itself", home, false, ""},
		{"sibling containing the word Desktop", filepath.Join(home, "DesktopApps", "proj"), false, ""},
		{"allowed dir named Desktop deeper down", filepath.Join(home, "source", "Desktop"), false, ""},
		{"path containing Desktop as substring", filepath.Join(home, "my-Desktop-backup"), false, ""},
		{"outside home entirely", "/opt/projects/app", false, ""},
		{"empty path", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			denial := CheckProjectPath(home, tt.path)
			if tt.wantDenied {
				if denial == nil {
					t.Fatalf("CheckProjectPath(%q) = nil, want denial under %s", tt.path, tt.wantRoot)
				}
				if denial.Root.Display != tt.wantRoot {
					t.Errorf("denied root = %q, want %q", denial.Root.Display, tt.wantRoot)
				}
				msg := denial.Error()
				if !strings.Contains(msg, tt.wantRoot) || !strings.Contains(msg, "sandbox blocks") {
					t.Errorf("message %q must name the root and the sandbox rule", msg)
				}
				if !strings.Contains(msg, "choose another folder") {
					t.Errorf("message %q must be actionable", msg)
				}
			} else if denial != nil {
				t.Fatalf("CheckProjectPath(%q) = %v, want nil", tt.path, denial)
			}
		})
	}
}

func TestCheckProjectPathSymlinkIntoDeniedRoot(t *testing.T) {
	home := tempHome(t)

	target := filepath.Join(home, "Desktop", "real-proj")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "source", "proj-link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	denial := CheckProjectPath(home, link)
	if denial == nil {
		t.Fatal("symlink into ~/Desktop was not denied")
	}
	if denial.Root.Display != "~/Desktop" {
		t.Errorf("denied root = %q, want ~/Desktop", denial.Root.Display)
	}

	// A symlink to an allowed location stays allowed.
	okTarget := filepath.Join(home, "elsewhere", "proj")
	if err := os.MkdirAll(okTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	okLink := filepath.Join(home, "source", "ok-link")
	if err := os.Symlink(okTarget, okLink); err != nil {
		t.Fatal(err)
	}
	if denial := CheckProjectPath(home, okLink); denial != nil {
		t.Fatalf("symlink to allowed dir denied: %v", denial)
	}
}

// TestStartAgentSandboxPreflight verifies a pre-existing project under a
// denied root is refused at StartAgent preflight — before any PTY spawn —
// and that the refusal is recorded as a sandbox_denied issue for the
// agent-issue plumbing (AgentStatus.issue while not running).
func TestStartAgentSandboxPreflight(t *testing.T) {
	home := tempHome(t)
	t.Setenv("HOME", home)

	projPath := filepath.Join(home, "Desktop", "legacy-proj")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	_, err := m.StartAgent(StartOptions{
		ProjectID:   "proj-under-desktop",
		ProjectName: "legacy",
		ProjectPath: projPath,
		Mode:        ModeChat,
	})
	if err == nil {
		t.Fatal("StartAgent under ~/Desktop succeeded, want preflight refusal")
	}
	if !strings.Contains(err.Error(), "~/Desktop") || !strings.Contains(err.Error(), "sandbox blocks") {
		t.Errorf("error %q must carry the actionable message", err)
	}

	issue := m.PreflightIssue("proj-under-desktop")
	if issue == nil {
		t.Fatal("no preflight issue recorded")
	}
	if issue.Type != AgentIssueSandboxDenied {
		t.Errorf("issue type = %q, want %q", issue.Type, AgentIssueSandboxDenied)
	}
	if issue.Message != err.Error() {
		t.Errorf("issue message %q != returned error %q", issue.Message, err.Error())
	}

	if got := m.PreflightIssue("other-project"); got != nil {
		t.Errorf("unrelated project has preflight issue: %v", got)
	}
}

// TestStartAgentPreflightSkipsUnsandboxed — an explicit sandbox=none run is
// exempt from the preflight (nothing is denied to an unsandboxed agent).
// The start still fails later for unrelated reasons in this bare test env,
// but it must NOT fail with the sandbox denial, and no preflight issue may
// be recorded.
func TestStartAgentPreflightSkipsUnsandboxed(t *testing.T) {
	home := tempHome(t)
	t.Setenv("HOME", home)
	// Point PATH at an empty dir so no real agent binary can be found —
	// the start fails fast at backend resolution, never spawning a PTY.
	t.Setenv("PATH", t.TempDir())

	projPath := filepath.Join(home, "Desktop", "legacy-proj")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	_, err := m.StartAgent(StartOptions{
		ProjectID:   "unsandboxed-desktop",
		ProjectName: "legacy",
		ProjectPath: projPath,
		Mode:        ModeChat,
		Sandbox:     SandboxNone,
	})
	if err != nil && strings.Contains(err.Error(), "sandbox blocks") {
		t.Fatalf("sandbox=none run hit the preflight denial: %v", err)
	}
	if issue := m.PreflightIssue("unsandboxed-desktop"); issue != nil {
		t.Errorf("preflight issue recorded for sandbox=none run: %v", issue)
	}
}
