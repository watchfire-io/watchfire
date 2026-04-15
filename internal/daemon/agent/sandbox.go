package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// SandboxBackend identifies the sandbox implementation to use.
const (
	SandboxAuto     = "auto"     // Platform picks best available
	SandboxSeatbelt = "seatbelt" // macOS sandbox-exec
	SandboxLandlock = "landlock" // Linux Landlock LSM (kernel 5.13+)
	SandboxBwrap    = "bwrap"    // Linux bubblewrap
	SandboxNone     = "none"     // No sandbox
)

// SandboxPolicy defines the filesystem access policy for sandboxed agents.
// All platform backends translate this policy into their native controls.
type SandboxPolicy struct {
	HomeDir    string
	ProjectDir string

	// Writable paths (besides ProjectDir which is always writable).
	WritablePaths []string

	// Paths denied for both read and write (hidden from the agent).
	DeniedPaths []string

	// Regex patterns for write-protected files (e.g. .env, .git/hooks).
	// Only supported by Seatbelt; other backends ignore these.
	WriteProtectedPatterns []string

	// Extras is the set of paths (and env-var strips) contributed by the
	// active agent backend. The sandbox policy renders these generically
	// alongside the base allow-list, keeping the policy itself free of
	// agent-specific knowledge. Path entries are already home-expanded.
	Extras backend.SandboxExtras

	// Enable trace logging of denied operations (debug only).
	Trace bool
}

// PlatformDefaults holds OS-specific path additions returned by platformDefaults().
type PlatformDefaults struct {
	ExtraWritable []string
	ExtraDenied   []string
}

// DefaultPolicy builds the shared sandbox policy, then merges OS-specific
// extras and the caller-supplied backend extras. Path entries in extras
// that begin with "~/" are expanded against homeDir.
func DefaultPolicy(homeDir, projectDir string, extras backend.SandboxExtras) SandboxPolicy {
	expanded := expandExtras(extras, homeDir)

	// Merge extras into WritablePaths so non-seatbelt backends (bwrap,
	// Landlock) see the same writable surface as the macOS profile.
	writable := []string{
		"/tmp",
		// Package manager caches
		filepath.Join(homeDir, ".npm"),
		filepath.Join(homeDir, ".yarn"),
		filepath.Join(homeDir, ".pnpm-store"),
		filepath.Join(homeDir, ".cache"),
		filepath.Join(homeDir, ".local", "share", "pnpm"),
		filepath.Join(homeDir, ".local"),
		// Dev tool caches
		filepath.Join(homeDir, ".cargo"),
		filepath.Join(homeDir, "go"),
		filepath.Join(homeDir, ".rustup"),
	}
	writable = append(writable, expanded.WritableSubpaths...)
	writable = append(writable, expanded.WritableLiterals...)
	writable = append(writable, expanded.CachePatterns...)

	policy := SandboxPolicy{
		HomeDir:       homeDir,
		ProjectDir:    projectDir,
		WritablePaths: writable,
		DeniedPaths: []string{
			filepath.Join(homeDir, ".ssh"),
			filepath.Join(homeDir, ".aws"),
			filepath.Join(homeDir, ".gnupg"),
			filepath.Join(homeDir, ".netrc"),
			filepath.Join(homeDir, ".npmrc"),
		},
		WriteProtectedPatterns: []string{
			`/\.env($|[^/]*)`,
			filepath.Join(projectDir, ".git", "hooks"),
		},
		Extras: expanded,
		Trace:  os.Getenv("WATCHFIRE_SANDBOX_TRACE") == "1",
	}

	// Merge platform-specific extras
	pd := platformDefaults(homeDir)
	policy.WritablePaths = append(policy.WritablePaths, pd.ExtraWritable...)
	policy.DeniedPaths = append(policy.DeniedPaths, pd.ExtraDenied...)

	return policy
}

// expandExtras returns a copy of extras with any leading "~/" in paths
// replaced by homeDir. A bare "~" is treated as homeDir.
func expandExtras(extras backend.SandboxExtras, homeDir string) backend.SandboxExtras {
	expand := func(paths []string) []string {
		if len(paths) == 0 {
			return nil
		}
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			switch {
			case p == "~":
				p = homeDir
			case strings.HasPrefix(p, "~/"):
				p = filepath.Join(homeDir, p[2:])
			}
			out = append(out, p)
		}
		return out
	}
	return backend.SandboxExtras{
		WritableSubpaths: expand(extras.WritableSubpaths),
		WritableLiterals: expand(extras.WritableLiterals),
		CachePatterns:    expand(extras.CachePatterns),
		StripEnv:         append([]string(nil), extras.StripEnv...),
	}
}

// SpawnSandboxed creates an exec.Cmd that runs the given command inside a
// sandbox. The sandbox backend is chosen automatically based on the
// platform. Extras are the agent backend's contributed paths/env strips.
// Returns the command, an optional temp file path for cleanup, and an error.
func SpawnSandboxed(homeDir, projectDir string, extras backend.SandboxExtras, command string, args ...string) (*exec.Cmd, string, error) {
	policy := DefaultPolicy(homeDir, projectDir, extras)
	return spawnSandboxedPlatform(policy, command, args...)
}

// SpawnSandboxedWith creates an exec.Cmd using a specific sandbox backend.
// Pass SandboxNone to run unsandboxed, SandboxAuto for platform default.
func SpawnSandboxedWith(sandboxBackend, homeDir, projectDir string, extras backend.SandboxExtras, command string, args ...string) (*exec.Cmd, string, error) {
	if sandboxBackend == SandboxNone {
		log.Println("[sandbox] Running agent without sandbox (--no-sandbox or sandbox=none)")
		policy := DefaultPolicy(homeDir, projectDir, extras)
		return spawnUnsandboxed(policy, command, args...)
	}
	if sandboxBackend == "" || sandboxBackend == SandboxAuto {
		return SpawnSandboxed(homeDir, projectDir, extras, command, args...)
	}
	policy := DefaultPolicy(homeDir, projectDir, extras)
	return spawnSandboxedWithBackend(sandboxBackend, policy, command, args...)
}

// spawnUnsandboxed creates a plain exec.Cmd with no sandboxing.
func spawnUnsandboxed(policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = policy.ProjectDir
	cmd.Env = buildBaseEnv(policy)
	return cmd, "", nil
}

// buildBaseEnv creates the common environment for sandboxed/unsandboxed agents.
// Any variables listed in policy.Extras.StripEnv are removed (e.g. a backend's
// own nested-session detection variable).
func buildBaseEnv(policy SandboxPolicy) []string {
	env := os.Environ()
	for _, key := range policy.Extras.StripEnv {
		env = removeEnv(env, key)
	}
	env = setEnv(env, "TERM", "xterm-256color")
	env = setEnv(env, "COLORTERM", "truecolor")
	return env
}

// removeEnv removes an environment variable from a slice.
func removeEnv(env []string, key string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			return append(env[:i], env[i+1:]...)
		}
	}
	return env
}

// setEnv sets or replaces an environment variable in a slice.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// ValidSandboxBackends returns the list of valid sandbox backend names.
func ValidSandboxBackends() []string {
	return []string{SandboxAuto, SandboxSeatbelt, SandboxLandlock, SandboxBwrap, SandboxNone}
}

// ValidateSandboxBackend checks if the given backend name is valid.
func ValidateSandboxBackend(name string) error {
	for _, b := range ValidSandboxBackends() {
		if name == b {
			return nil
		}
	}
	return fmt.Errorf("invalid sandbox backend %q; valid options: %s", name, strings.Join(ValidSandboxBackends(), ", "))
}
