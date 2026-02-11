package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const profileTemplate = `(version 1)
(deny default)

; READ ACCESS - Allow all, deny sensitive
(allow file-read* (subpath "/"))

; DENY sensitive credential paths
(deny file-read* (subpath "%s/.ssh"))
(deny file-read* (subpath "%s/.aws"))
(deny file-read* (subpath "%s/.gnupg"))
(deny file-read* (literal "%s/.netrc"))
(deny file-read* (literal "%s/.npmrc"))

; DENY protected user folders (prevents TCC prompts)
(deny file-read* (subpath "%s/Desktop"))
(deny file-read* (subpath "%s/Documents"))
(deny file-read* (subpath "%s/Downloads"))
(deny file-read* (subpath "%s/Music"))
(deny file-read* (subpath "%s/Movies"))
(deny file-read* (subpath "%s/Pictures"))
; NOTE: Keychains allowed - required for Claude Code auth

; WRITE ACCESS - Project + Claude config + caches + temp
(allow file-write* (subpath "%s"))
(allow file-write* (subpath "%s/.claude"))
(allow file-write* (literal "%s/.claude.json"))
(allow file-write* (subpath "%s/Library/Caches/claude-cli-nodejs"))
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "/private/tmp"))
(allow file-write* (subpath "/var/folders"))
(allow file-write* (subpath "/private/var/folders"))

; PACKAGE MANAGER CACHES
(allow file-write* (subpath "%s/.npm"))
(allow file-write* (subpath "%s/.yarn"))
(allow file-write* (subpath "%s/.pnpm-store"))
(allow file-write* (subpath "%s/.cache"))
(allow file-write* (subpath "%s/.local/share/pnpm"))
(allow file-write* (subpath "%s/Library/Caches/npm"))
(allow file-write* (subpath "%s/Library/Caches/yarn"))

; DEV TOOL CACHES
(allow file-write* (subpath "%s/.cargo"))
(allow file-write* (subpath "%s/go"))
(allow file-write* (subpath "%s/.rustup"))

; PROTECTED - Block writes even in project
(deny file-write* (regex #"/\.env($|[^/]*)"))
(deny file-write* (subpath "%s/.git/hooks"))

; NETWORK, DEVICES, PROCESS, IPC
(allow network*)
(allow file-read* (subpath "/dev"))
(allow file-write* (subpath "/dev"))
(allow process-exec*)
(allow process-fork)
(allow process-info*)
(allow signal)
(allow mach*)
(allow sysctl*)
(allow ipc*)
(allow file-ioctl)
`

// GenerateProfile generates a macOS sandbox-exec profile for the given home and project directories.
// If trace is true, a (trace ...) directive is prepended to log denied operations for debugging.
func GenerateProfile(homeDir, projectDir string, trace bool) string {
	profile := fmt.Sprintf(profileTemplate,
		// DENY sensitive credential paths (5 args: homeDir)
		homeDir, homeDir, homeDir, homeDir, homeDir,
		// DENY protected user folders (6 args: homeDir)
		homeDir, homeDir, homeDir, homeDir, homeDir, homeDir,
		// WRITE ACCESS - Project + Claude config (4 args: projectDir, homeDir, homeDir, homeDir)
		projectDir, homeDir, homeDir, homeDir,
		// PACKAGE MANAGER CACHES (7 args: homeDir)
		homeDir, homeDir, homeDir, homeDir, homeDir, homeDir, homeDir,
		// DEV TOOL CACHES (3 args: homeDir)
		homeDir, homeDir, homeDir,
		// PROTECTED - .git/hooks (1 arg: projectDir)
		projectDir,
	)
	if trace {
		profile = "(trace \"/tmp/watchfire-sandbox-trace.sb\")\n" + profile
	}
	return profile
}

// SpawnSandboxed creates an exec.Cmd that runs the given command inside a macOS sandbox.
// The caller is responsible for starting the process (e.g. via PTY).
// Set WATCHFIRE_SANDBOX_TRACE=1 to enable trace logging of denied operations to /tmp/watchfire-sandbox-trace.sb.
func SpawnSandboxed(homeDir, projectDir, command string, args ...string) (*exec.Cmd, string, error) {
	trace := os.Getenv("WATCHFIRE_SANDBOX_TRACE") == "1"
	profile := GenerateProfile(homeDir, projectDir, trace)

	// Write profile to temp file
	tmpFile, err := os.CreateTemp("", "watchfire-sandbox-*.sb")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create sandbox profile: %w", err)
	}
	if _, err := tmpFile.WriteString(profile); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, "", fmt.Errorf("failed to write sandbox profile: %w", err)
	}
	tmpFile.Close()

	// Build: sandbox-exec -f <tmpfile> <command> <args...>
	sandboxArgs := []string{"-f", tmpFile.Name(), command}
	sandboxArgs = append(sandboxArgs, args...)
	cmd := exec.Command("sandbox-exec", sandboxArgs...)

	// Set working directory to the project
	cmd.Dir = projectDir

	// Set environment
	env := os.Environ()
	env = setEnv(env, "TERM", "xterm-256color")
	env = setEnv(env, "COLORTERM", "truecolor")

	// Ensure Homebrew paths are in PATH
	path := os.Getenv("PATH")
	for _, p := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(path, p) {
			path = p + ":" + path
		}
	}
	env = setEnv(env, "PATH", path)
	cmd.Env = env

	return cmd, tmpFile.Name(), nil
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
