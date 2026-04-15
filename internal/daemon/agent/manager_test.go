package agent

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
)

// fakeBackend is a minimal Backend used to avoid touching real binaries in
// unit tests. Only Name/ResolveExecutable are exercised here.
type fakeBackend struct{ name string }

func (f *fakeBackend) Name() string                                       { return f.name }
func (f *fakeBackend) DisplayName() string                                { return f.name }
func (f *fakeBackend) ResolveExecutable(*models.Settings) (string, error) { return "/bin/true", nil }
func (f *fakeBackend) BuildCommand(backend.CommandOpts) (backend.Command, error) {
	return backend.Command{}, nil
}
func (f *fakeBackend) SandboxExtras() backend.SandboxExtras        { return backend.SandboxExtras{} }
func (f *fakeBackend) InstallSystemPrompt(string, string) error    { return nil }
func (f *fakeBackend) LocateTranscript(string, time.Time, string) (string, error) {
	return "", nil
}
func (f *fakeBackend) FormatTranscript(string) (string, error) { return "", nil }

// registerFakeBackend registers a backend and returns a cleanup that isn't
// straightforward — the registry has no unregister. Tests use names unlikely
// to collide. We defend against duplicate registration by recovering from
// the panic when the same name is registered twice across test runs.
func registerFakeBackend(t *testing.T, name string) backend.Backend {
	t.Helper()
	defer func() {
		// ignore "duplicate registration" panics from prior test runs in the
		// same process (e.g. when go test caches a package).
		_ = recover()
	}()
	b := &fakeBackend{name: name}
	backend.Register(b)
	return b
}

func TestResolveBackendProjectTakesPrecedence(t *testing.T) {
	registerFakeBackend(t, "fake-proj-1")
	registerFakeBackend(t, "fake-global-1")

	project := &models.Project{DefaultAgent: "fake-proj-1"}
	settings := &models.Settings{Defaults: models.DefaultsConfig{DefaultAgent: "fake-global-1"}}

	be, err := resolveBackend(project, settings)
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if be.Name() != "fake-proj-1" {
		t.Errorf("got backend %q, want fake-proj-1", be.Name())
	}
}

func TestResolveBackendFallsBackToGlobal(t *testing.T) {
	registerFakeBackend(t, "fake-global-2")

	project := &models.Project{DefaultAgent: ""}
	settings := &models.Settings{Defaults: models.DefaultsConfig{DefaultAgent: "fake-global-2"}}

	be, err := resolveBackend(project, settings)
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if be.Name() != "fake-global-2" {
		t.Errorf("got backend %q, want fake-global-2", be.Name())
	}
}

func TestResolveBackendFallsBackToClaudeWhenEverythingEmpty(t *testing.T) {
	// Claude registers itself via init() — so it's always available.
	project := &models.Project{DefaultAgent: ""}
	settings := &models.Settings{Defaults: models.DefaultsConfig{DefaultAgent: ""}}

	be, err := resolveBackend(project, settings)
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if be.Name() != backend.ClaudeBackendName {
		t.Errorf("got backend %q, want %q", be.Name(), backend.ClaudeBackendName)
	}
}

func TestResolveBackendGlobalAskSentinelFallsThroughToClaude(t *testing.T) {
	// Empty string on the global default is the "ask per project" sentinel
	// per the v2 spec — it must NOT be treated as a selected agent.
	project := &models.Project{DefaultAgent: ""}
	settings := &models.Settings{Defaults: models.DefaultsConfig{DefaultAgent: ""}}

	be, err := resolveBackend(project, settings)
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if be.Name() != backend.ClaudeBackendName {
		t.Errorf("sentinel not handled: got %q", be.Name())
	}
}

func TestResolveBackendNilSettingsFallsBack(t *testing.T) {
	project := &models.Project{DefaultAgent: ""}

	be, err := resolveBackend(project, nil)
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if be.Name() != backend.ClaudeBackendName {
		t.Errorf("got backend %q, want %q", be.Name(), backend.ClaudeBackendName)
	}
}

func TestResolveBackendUnknownNameIsError(t *testing.T) {
	project := &models.Project{DefaultAgent: "nope-does-not-exist"}
	_, err := resolveBackend(project, nil)
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !errors.Is(err, backend.ErrUnknownBackend) {
		t.Errorf("error %v is not ErrUnknownBackend", err)
	}
}

func TestResolveBackendUnknownGlobalNameIsError(t *testing.T) {
	project := &models.Project{DefaultAgent: ""}
	settings := &models.Settings{Defaults: models.DefaultsConfig{DefaultAgent: "also-does-not-exist"}}

	_, err := resolveBackend(project, settings)
	if err == nil {
		t.Fatal("expected error for unknown global backend, got nil")
	}
	if !errors.Is(err, backend.ErrUnknownBackend) {
		t.Errorf("error %v is not ErrUnknownBackend", err)
	}
}

func TestMergeBackendEnvAddsNewVar(t *testing.T) {
	sandbox := []string{"TERM=xterm-256color", "PATH=/usr/bin"}
	be := []string{"PATH=/usr/bin", "CODEX_HOME=/tmp/cx"}

	out := mergeBackendEnv(sandbox, be)

	foundCodex := false
	for _, e := range out {
		if e == "CODEX_HOME=/tmp/cx" {
			foundCodex = true
		}
	}
	if !foundCodex {
		t.Errorf("CODEX_HOME not in merged env: %v", out)
	}
}

func TestMergeBackendEnvSkipsUnchangedParentVars(t *testing.T) {
	// Pick an env var that actually exists so the parent-dedup path is
	// exercised deterministically.
	key := "WATCHFIRE_TEST_MERGE"
	_ = os.Setenv(key, "original")
	defer func() { _ = os.Unsetenv(key) }()

	sandbox := []string{"TERM=xterm-256color"}
	be := []string{key + "=original"}

	out := mergeBackendEnv(sandbox, be)
	for _, e := range out {
		if e == key+"=original" {
			t.Errorf("parent-unchanged var %q was re-added to sandbox env", key)
		}
	}
}

func TestMergeBackendEnvOverridesParentWhenValueDiffers(t *testing.T) {
	key := "WATCHFIRE_TEST_OVERRIDE"
	_ = os.Setenv(key, "parent-value")
	defer func() { _ = os.Unsetenv(key) }()

	sandbox := []string{"TERM=xterm"}
	be := []string{key + "=backend-value"}

	out := mergeBackendEnv(sandbox, be)
	found := false
	for _, e := range out {
		if e == key+"=backend-value" {
			found = true
		}
	}
	if !found {
		t.Errorf("backend override missing from merged env: %v", out)
	}
}

func TestMergeBackendEnvEmptyBackendEnv(t *testing.T) {
	sandbox := []string{"TERM=xterm-256color"}
	out := mergeBackendEnv(sandbox, nil)
	if len(out) != 1 || out[0] != "TERM=xterm-256color" {
		t.Errorf("unexpected merge result: %v", out)
	}
}
