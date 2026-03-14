//go:build linux

package agent

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SpawnSandboxed creates an exec.Cmd that runs the given command inside a bwrap sandbox.
// If bwrap (bubblewrap) is not available it falls back to running without sandboxing.
func SpawnSandboxed(homeDir, projectDir, command string, args ...string) (*exec.Cmd, string, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		log.Println("watchfire: bwrap not found — running agent without sandbox (install 'bubblewrap' for sandboxing)")
		return spawnUnsandboxed(homeDir, projectDir, command, args...)
	}
	return spawnWithBwrap(bwrapPath, homeDir, projectDir, command, args...)
}

// spawnWithBwrap builds a bwrap invocation that mirrors the macOS sandbox-exec policy:
//   - Read-only access to the entire filesystem
//   - Credential dirs (.ssh, .aws, .gnupg) replaced with empty tmpfs
//   - Writable access to the project dir, ~/.claude, and package-manager caches
//   - Full network access
func spawnWithBwrap(bwrapPath, homeDir, projectDir, command string, args ...string) (*exec.Cmd, string, error) {
	// Pre-create all directories that bwrap will mount (--bind-try or --tmpfs).
	// bwrap cannot mkdir mount points after --ro-bind / / has made the fs read-only.
	dirsToCreate := []string{
		// Writable mounts
		filepath.Join(homeDir, ".claude"),
		filepath.Join(homeDir, ".npm"),
		filepath.Join(homeDir, ".yarn"),
		filepath.Join(homeDir, ".cache"),
		filepath.Join(homeDir, ".pnpm-store"),
		filepath.Join(homeDir, ".local"),
		filepath.Join(homeDir, ".cargo"),
		filepath.Join(homeDir, "go"),
		filepath.Join(homeDir, ".rustup"),
		projectDir,
		// Credential dirs masked with tmpfs — must exist as mount points
		filepath.Join(homeDir, ".ssh"),
		filepath.Join(homeDir, ".aws"),
		filepath.Join(homeDir, ".gnupg"),
	}
	for _, d := range dirsToCreate {
		_ = os.MkdirAll(d, 0o755)
	}

	// Pre-create ~/.claude.json so --bind-try can mount the file.
	claudeJSON := filepath.Join(homeDir, ".claude.json")
	if _, err := os.Stat(claudeJSON); os.IsNotExist(err) {
		if f, err := os.Create(claudeJSON); err == nil {
			_ = f.Close()
		}
	}

	bwrapArgs := []string{
		// Base: read-only bind of the entire root filesystem.
		"--ro-bind", "/", "/",
		// Overlay special filesystems.
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		// Hide credential directories (replace with empty tmpfs).
		"--tmpfs", filepath.Join(homeDir, ".ssh"),
		"--tmpfs", filepath.Join(homeDir, ".aws"),
		"--tmpfs", filepath.Join(homeDir, ".gnupg"),
		// Writable: project directory.
		"--bind", projectDir, projectDir,
		// Writable: Claude config.
		"--bind-try", filepath.Join(homeDir, ".claude"), filepath.Join(homeDir, ".claude"),
		"--bind-try", claudeJSON, claudeJSON,
		// Writable: package manager caches.
		"--bind-try", filepath.Join(homeDir, ".npm"), filepath.Join(homeDir, ".npm"),
		"--bind-try", filepath.Join(homeDir, ".yarn"), filepath.Join(homeDir, ".yarn"),
		"--bind-try", filepath.Join(homeDir, ".pnpm-store"), filepath.Join(homeDir, ".pnpm-store"),
		"--bind-try", filepath.Join(homeDir, ".cache"), filepath.Join(homeDir, ".cache"),
		"--bind-try", filepath.Join(homeDir, ".local"), filepath.Join(homeDir, ".local"),
		// Writable: dev tool caches.
		"--bind-try", filepath.Join(homeDir, ".cargo"), filepath.Join(homeDir, ".cargo"),
		"--bind-try", filepath.Join(homeDir, "go"), filepath.Join(homeDir, "go"),
		"--bind-try", filepath.Join(homeDir, ".rustup"), filepath.Join(homeDir, ".rustup"),
		// Network access.
		"--share-net",
		// Working directory inside the sandbox.
		"--chdir", projectDir,
		"--",
		command,
	}
	bwrapArgs = append(bwrapArgs, args...)

	cmd := exec.Command(bwrapPath, bwrapArgs...)
	cmd.Dir = projectDir
	cmd.Env = buildEnv(homeDir)
	return cmd, "", nil
}

func spawnUnsandboxed(homeDir, projectDir, command string, args ...string) (*exec.Cmd, string, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = projectDir
	cmd.Env = buildEnv(homeDir)
	return cmd, "", nil
}

func buildEnv(homeDir string) []string {
	env := os.Environ()
	env = removeEnv(env, "CLAUDECODE")
	env = setEnv(env, "TERM", "xterm-256color")
	env = setEnv(env, "COLORTERM", "truecolor")

	path := os.Getenv("PATH")
	for _, p := range []string{"/usr/local/bin", "/usr/bin", "/bin", filepath.Join(homeDir, ".local", "bin")} {
		if !strings.Contains(path, p) {
			path = p + ":" + path
		}
	}
	env = setEnv(env, "PATH", path)
	return env
}

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			return append(env[:i], env[i+1:]...)
		}
	}
	return env
}

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
