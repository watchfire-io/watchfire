package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// unavailableBackend ResolveExecutable always fails, so it simulates a
// backend whose CLI has not been installed on this host. Every other
// method is a harmless stub.
type unavailableBackend struct{ name string }

func (u *unavailableBackend) Name() string                                    { return u.name }
func (u *unavailableBackend) DisplayName() string                             { return u.name }
func (u *unavailableBackend) ResolveExecutable(*models.Settings) (string, error) {
	return "", errors.New("binary not installed")
}
func (u *unavailableBackend) BuildCommand(backend.CommandOpts) (backend.Command, error) {
	return backend.Command{}, nil
}
func (u *unavailableBackend) SandboxExtras() backend.SandboxExtras        { return backend.SandboxExtras{} }
func (u *unavailableBackend) InstallSystemPrompt(string, string) error    { return nil }
func (u *unavailableBackend) LocateTranscript(string, time.Time, string) (string, error) {
	return "", nil
}
func (u *unavailableBackend) FormatTranscript(string) (string, error) { return "", nil }

// TestListAgentsIncludesUnavailableBackend guards the architectural
// invariant behind the fix for issue #29: ListAgents must return every
// registered backend regardless of binary availability. Hiding agents
// whose binary cannot be resolved is what made freshly-installed CLIs
// invisible in the picker until a daemon restart (and sometimes, if the
// install path was outside the resolver's fallback list, not at all).
// The binary-missing state is reported via AgentInfo.available, not by
// omission.
func TestListAgentsIncludesUnavailableBackend(t *testing.T) {
	const probeName = "listagents-regression-unavailable"
	backend.Register(&unavailableBackend{name: probeName})

	svc := &settingsService{}
	resp, err := svc.ListAgents(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("ListAgents error: %v", err)
	}

	var found bool
	for _, a := range resp.Agents {
		if a.Name == probeName {
			found = true
			if a.Available {
				t.Errorf("AgentInfo[%q].Available = true, want false (ResolveExecutable errors)", probeName)
			}
		}
	}
	if !found {
		t.Fatalf("ListAgents did not include backend %q. Filtering by availability breaks the picker for freshly-installed CLIs (issue #29).", probeName)
	}

	// All production backends (Claude, Codex, Gemini, opencode, Copilot)
	// must also be present. A partial registry silently omits choices.
	for _, required := range []string{"claude-code", "codex", "gemini", "opencode", "copilot"} {
		var ok bool
		for _, a := range resp.Agents {
			if a.Name == required {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("ListAgents missing registered backend %q", required)
		}
	}
}

// TestUpdateSettingsTerminalShellValidation guards the X_OK contract on
// the new defaults.terminal_shell field (issue #32). The daemon must
// reject non-absolute paths, missing files, directories, and non-executable
// regular files; an empty string must be accepted (= "$SHELL autodetect").
// An executable file must round-trip cleanly into settings.yaml.
func TestUpdateSettingsTerminalShellValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	svc := &settingsService{}

	// Helper: build a defaults block with the given terminal shell. We have
	// to populate the rest of the defaults so the daemon doesn't zero them
	// out on save.
	makeReq := func(shell string) *pb.UpdateSettingsRequest {
		return &pb.UpdateSettingsRequest{
			Defaults: &pb.DefaultsConfig{
				AutoMerge:        true,
				AutoDeleteBranch: true,
				AutoStartTasks:   true,
				DefaultSandbox:   "auto",
				DefaultAgent:     "claude-code",
				TerminalShell:    shell,
			},
		}
	}

	// Empty shell = autodetect = always accepted.
	if _, err := svc.UpdateSettings(context.Background(), makeReq("")); err != nil {
		t.Fatalf("empty terminal_shell should round-trip: %v", err)
	}

	// Non-absolute path → rejected.
	if _, err := svc.UpdateSettings(context.Background(), makeReq("zsh")); err == nil {
		t.Errorf("non-absolute terminal_shell should be rejected")
	}

	// Missing file → rejected.
	if _, err := svc.UpdateSettings(context.Background(), makeReq(filepath.Join(dir, "nonexistent"))); err == nil {
		t.Errorf("missing terminal_shell should be rejected")
	}

	// Directory → rejected.
	if _, err := svc.UpdateSettings(context.Background(), makeReq(dir)); err == nil {
		t.Errorf("directory terminal_shell should be rejected")
	}

	// Non-executable regular file → rejected.
	nonExec := filepath.Join(dir, "not-exec.sh")
	if err := os.WriteFile(nonExec, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSettings(context.Background(), makeReq(nonExec)); err == nil {
		t.Errorf("non-executable terminal_shell should be rejected")
	}

	// Executable regular file → accepted, persisted.
	exec := filepath.Join(dir, "fake-shell")
	if err := os.WriteFile(exec, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UpdateSettings(context.Background(), makeReq(exec))
	if err != nil {
		t.Fatalf("executable terminal_shell should be accepted: %v", err)
	}
	if got.Defaults == nil || got.Defaults.TerminalShell != exec {
		t.Errorf("TerminalShell did not round-trip: got=%q want=%q", got.GetDefaults().GetTerminalShell(), exec)
	}

	// Loading the persisted settings reads the same value back.
	loaded, err := svc.GetSettings(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if loaded.Defaults.TerminalShell != exec {
		t.Errorf("TerminalShell not persisted: got=%q want=%q", loaded.Defaults.TerminalShell, exec)
	}
}
