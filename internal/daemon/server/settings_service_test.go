package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
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
