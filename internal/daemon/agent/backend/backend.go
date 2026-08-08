// Package backend defines the AgentBackend interface and a process-wide
// registry used by the daemon to dispatch agent-specific behaviour
// (executable resolution, command construction, sandbox extras,
// system-prompt delivery, transcript discovery and formatting).
//
// Concrete backends (Claude Code, Codex, ...) live in sibling files and
// register themselves via package-level Register calls in their init().
package backend

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// ErrUnknownBackend is returned when a backend lookup fails.
var ErrUnknownBackend = errors.New("backend: unknown agent backend")

// Backend is the per-agent abstraction used by the daemon. Every method
// must be safe to call concurrently; backends are typically stateless.
type Backend interface {
	// Name is the stable identifier persisted in project/settings YAML
	// (e.g. "claude-code", "codex").
	Name() string

	// DisplayName is the human-readable label shown in UIs.
	DisplayName() string

	// ResolveExecutable returns the absolute path to the agent binary,
	// consulting user settings first then falling back to PATH and
	// well-known install locations.
	ResolveExecutable(s *models.Settings) (string, error)

	// BuildCommand constructs the PTY invocation for a session.
	BuildCommand(opts CommandOpts) (Command, error)

	// SandboxExtras returns paths the sandbox profile must allow for
	// this backend (e.g. ~/.claude config dir, codex auth dir).
	SandboxExtras() SandboxExtras

	// InstallSystemPrompt delivers the composed Watchfire system prompt
	// to the agent. Some backends use a CLI flag (no-op here); others
	// write a file (e.g. AGENTS.md) into workDir or a per-session dir.
	// Called after the worktree exists but before BuildCommand.
	InstallSystemPrompt(workDir, composedPrompt string) error

	// LocateTranscript finds the JSONL transcript file produced by the
	// agent for a session that started at `started`. sessionHint is the
	// session correlation token (e.g. session name) when available.
	LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error)

	// FormatTranscript renders the JSONL transcript at jsonlPath into
	// the plain-text representation used by the log viewer.
	FormatTranscript(jsonlPath string) (string, error)
}

// SessionHomeProvider is implemented by backends that materialize a
// per-session home directory under ~/.watchfire/<agent>-home/ (the
// composed AGENTS.md / system prompt, config symlinks, and the agent
// CLI's own session state). Backends that deliver the system prompt via
// CLI flags (Claude) do not implement it. The daemon uses this to remove
// session scratch dirs at session end and to sweep a deleted project's
// leftovers.
type SessionHomeProvider interface {
	// SessionHomeRoot returns the backend's home-root directory,
	// e.g. ~/.watchfire/codex-home.
	SessionHomeRoot() (string, error)

	// SessionHome returns the per-session directory for sessionName —
	// always a direct child of SessionHomeRoot().
	SessionHome(sessionName string) (string, error)
}

// sessionHomeRoot returns ~/.watchfire/<dirName> (e.g. "codex-home").
func sessionHomeRoot(dirName string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".watchfire", dirName), nil
}

// CommandOpts carries the inputs BuildCommand needs to assemble a
// concrete invocation.
type CommandOpts struct {
	WorkDir       string
	SessionName   string
	SystemPrompt  string
	InitialPrompt string
	ExtraArgs     []string
}

// Command is the result of BuildCommand: the executable, args, env, and
// a flag indicating whether the manager should paste InitialPrompt to
// the PTY after startup (true) or whether it is already embedded in
// Args (false).
type Command struct {
	Path               string
	Args               []string
	Env                []string
	PasteInitialPrompt bool
}

// SandboxExtras is the set of paths a backend contributes to the
// sandbox-exec policy. Subpaths are directory trees, literals are exact
// file paths, and CachePatterns are glob-friendly cache locations the
// backend writes to. StripEnv lists environment variables that must be
// removed from the child process environment (e.g. nested-session
// detection variables specific to the backend's CLI).
//
// Path entries may use a leading "~/" to refer to the user's home
// directory; the sandbox layer expands this at policy build time.
type SandboxExtras struct {
	WritableSubpaths []string
	WritableLiterals []string
	CachePatterns    []string
	StripEnv         []string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Backend{}
)

// Register installs a Backend in the process-wide registry keyed by
// b.Name(). Registering the same name twice panics — this catches
// double-init bugs at process start rather than silently overwriting a
// backend with another implementation. Backends typically register from
// init(), so a panic here surfaces immediately at startup.
func Register(b Backend) {
	if b == nil {
		panic("backend: Register called with nil Backend")
	}
	name := b.Name()
	if name == "" {
		panic("backend: Register called with empty Name()")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic("backend: duplicate registration for " + name)
	}
	registry[name] = b
}

// Get returns the registered backend for name and whether it was found.
func Get(name string) (Backend, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	b, ok := registry[name]
	return b, ok
}

// List returns all registered backends sorted by Name() for stable
// iteration order in UIs and tests.
func List() []Backend {
	registryMu.RLock()
	out := make([]Backend, 0, len(registry))
	for _, b := range registry {
		out = append(out, b)
	}
	registryMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// reset clears the registry. Test-only; not exported.
func reset() {
	registryMu.Lock()
	registry = map[string]Backend{}
	registryMu.Unlock()
}
