package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// unavailableBackend ResolveExecutable always fails, so it simulates a
// backend whose CLI has not been installed on this host. Every other
// method is a harmless stub.
type unavailableBackend struct{ name string }

func (u *unavailableBackend) Name() string        { return u.name }
func (u *unavailableBackend) DisplayName() string { return u.name }
func (u *unavailableBackend) ResolveExecutable(*models.Settings) (string, error) {
	return "", errors.New("binary not installed")
}
func (u *unavailableBackend) BuildCommand(backend.CommandOpts) (backend.Command, error) {
	return backend.Command{}, nil
}
func (u *unavailableBackend) SandboxExtras() backend.SandboxExtras     { return backend.SandboxExtras{} }
func (u *unavailableBackend) InstallSystemPrompt(string, string) error { return nil }
func (u *unavailableBackend) LocateTranscript(string, time.Time, string) (string, error) {
	return "", nil
}
func (u *unavailableBackend) FormatTranscript(string) (string, error) { return "", nil }

// scratchMcpHome points the MCP install writers at a throwaway HOME with no
// client CLIs on PATH, so the onboarding RPC tests can never read or write a
// real ~/.gemini, ~/.codex or ~/.claude.json.
func scratchMcpHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("COPILOT_HOME", "")
	return home
}

// TestGetMcpClientStatus checks the shape the TUI and GUI depend on: one entry
// per known harness keyed by its stable id, a message for every entry, and the
// Custom snippet carried alongside so all surfaces render that option from one
// source of truth.
func TestGetMcpClientStatus(t *testing.T) {
	home := scratchMcpHome(t)
	// Pre-create the Gemini config dir so exactly one harness is "detected"
	// without any CLI on PATH.
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := &settingsService{}
	resp, err := svc.GetMcpClientStatus(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetMcpClientStatus error: %v", err)
	}

	byClient := map[string]*pb.McpClientStatus{}
	for _, c := range resp.Clients {
		byClient[c.Client] = c
	}
	for _, want := range []string{"claude-code", "codex", "gemini", "opencode", "copilot"} {
		got, ok := byClient[want]
		if !ok {
			t.Fatalf("GetMcpClientStatus missing client %q (have %v)", want, byClient)
		}
		if got.DisplayName == "" {
			t.Errorf("client %q has no display_name", want)
		}
		if got.ConfigPath == "" {
			t.Errorf("client %q has no config_path", want)
		}
		if got.Message == "" {
			t.Errorf("client %q has no message", want)
		}
	}

	if !byClient["gemini"].Detected {
		t.Error("gemini should be detected: its config dir exists")
	}
	if byClient["gemini"].Configured {
		t.Error("gemini should not be configured: nothing was installed yet")
	}
	if byClient["claude-code"].Detected {
		t.Error("claude-code should not be detected: no CLI on PATH, no ~/.claude.json")
	}

	if !strings.Contains(resp.CustomSnippet, "mcp") {
		t.Errorf("custom_snippet does not look like the generic server block: %q", resp.CustomSnippet)
	}
}

// TestInstallMcpClientIdempotent covers the happy path: installing a detected
// harness flips configured to true, and re-installing is a no-op that says so
// rather than rewriting or erroring.
func TestInstallMcpClientIdempotent(t *testing.T) {
	home := scratchMcpHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := &settingsService{}
	req := &pb.InstallMcpClientRequest{Client: "gemini"}

	first, err := svc.InstallMcpClient(context.Background(), req)
	if err != nil {
		t.Fatalf("first InstallMcpClient error: %v", err)
	}
	if !first.Configured {
		t.Fatalf("configured = false after install (message: %s)", first.Message)
	}
	if first.ConfigPath != filepath.Join(home, ".gemini", "settings.json") {
		t.Errorf("config_path = %q, want the scratch gemini settings.json", first.ConfigPath)
	}
	if !strings.Contains(first.Message, "Registered") {
		t.Errorf("message does not report the install: %q", first.Message)
	}

	second, err := svc.InstallMcpClient(context.Background(), req)
	if err != nil {
		t.Fatalf("second InstallMcpClient error: %v", err)
	}
	if !second.Configured {
		t.Error("configured = false on re-install")
	}
	if !strings.Contains(second.Message, "already configured") {
		t.Errorf("re-install message should report a no-op: %q", second.Message)
	}

	// The status RPC agrees with the install RPC.
	list, err := svc.GetMcpClientStatus(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetMcpClientStatus error: %v", err)
	}
	for _, c := range list.Clients {
		if c.Client == "gemini" && !c.Configured {
			t.Error("GetMcpClientStatus reports gemini unconfigured after a successful install")
		}
	}
}

// TestInstallMcpClientMissingHarnessReturnsManualInstructions guards the
// contract the UIs render against: a harness that isn't installed must come
// back as a normal response carrying the paste-this-yourself instructions, not
// as a gRPC error.
func TestInstallMcpClientMissingHarnessReturnsManualInstructions(t *testing.T) {
	scratchMcpHome(t)

	svc := &settingsService{}
	for _, client := range []string{"claude-code", "gemini"} {
		resp, err := svc.InstallMcpClient(context.Background(), &pb.InstallMcpClientRequest{Client: client})
		if err != nil {
			t.Fatalf("InstallMcpClient(%q) returned a gRPC error instead of manual instructions: %v", client, err)
		}
		if resp.Detected || resp.Configured {
			t.Errorf("%s: detected=%v configured=%v, want both false", client, resp.Detected, resp.Configured)
		}
		if !strings.Contains(resp.Message, "Could not register automatically") {
			t.Errorf("%s: message lacks the manual fallback: %q", client, resp.Message)
		}
		if !strings.Contains(resp.Message, "watchfire") {
			t.Errorf("%s: message lacks the snippet to paste: %q", client, resp.Message)
		}
	}
}

// TestInstallMcpClientUnknownClient — an unrecognized key is a caller bug, so
// it is the one case that is a gRPC error.
func TestInstallMcpClientUnknownClient(t *testing.T) {
	scratchMcpHome(t)

	svc := &settingsService{}
	_, err := svc.InstallMcpClient(context.Background(), &pb.InstallMcpClientRequest{Client: "emacs"})
	if err == nil {
		t.Fatal("InstallMcpClient with an unknown client should error")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("error code = %v, want %v", got, codes.InvalidArgument)
	}
}

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

	// All production backends (Claude, Codex, Gemini, opencode, Copilot, Cursor)
	// must also be present. A partial registry silently omits choices.
	for _, required := range []string{"claude-code", "codex", "gemini", "opencode", "copilot", "cursor"} {
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
