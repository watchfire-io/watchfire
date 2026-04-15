package backend

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

type fakeBackend struct{ name string }

func (f *fakeBackend) Name() string                                    { return f.name }
func (f *fakeBackend) DisplayName() string                             { return f.name }
func (f *fakeBackend) ResolveExecutable(*models.Settings) (string, error) { return "", nil }
func (f *fakeBackend) BuildCommand(CommandOpts) (Command, error)       { return Command{}, nil }
func (f *fakeBackend) SandboxExtras() SandboxExtras                    { return SandboxExtras{} }
func (f *fakeBackend) InstallSystemPrompt(string, string) error        { return nil }
func (f *fakeBackend) LocateTranscript(string, time.Time, string) (string, error) {
	return "", nil
}
func (f *fakeBackend) FormatTranscript(string) (string, error) { return "", nil }

func TestRegisterAndGet(t *testing.T) {
	reset()
	b := &fakeBackend{name: "fake"}
	Register(b)

	got, ok := Get("fake")
	if !ok {
		t.Fatal("Get(\"fake\") returned ok=false after Register")
	}
	if got != b {
		t.Fatalf("Get returned %v, want %v", got, b)
	}
}

func TestGetUnknownReturnsFalse(t *testing.T) {
	reset()
	if _, ok := Get("does-not-exist"); ok {
		t.Fatal("Get for unknown name returned ok=true")
	}
}

func TestListSortedByName(t *testing.T) {
	reset()
	Register(&fakeBackend{name: "zeta"})
	Register(&fakeBackend{name: "alpha"})
	Register(&fakeBackend{name: "mu"})

	list := List()
	if len(list) != 3 {
		t.Fatalf("List length = %d, want 3", len(list))
	}
	want := []string{"alpha", "mu", "zeta"}
	for i, b := range list {
		if b.Name() != want[i] {
			t.Errorf("List[%d].Name() = %q, want %q", i, b.Name(), want[i])
		}
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	reset()
	Register(&fakeBackend{name: "dup"})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate Register, got none")
		}
	}()
	Register(&fakeBackend{name: "dup"})
}

func TestRegisterNilPanics(t *testing.T) {
	reset()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil Register, got none")
		}
	}()
	Register(nil)
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	reset()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty Name, got none")
		}
	}()
	Register(&fakeBackend{name: ""})
}
