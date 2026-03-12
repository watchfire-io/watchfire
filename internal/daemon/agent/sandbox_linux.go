//go:build linux

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SpawnSandboxed creates an exec.Cmd that runs the given command.
// On Linux, this currently runs without sandboxing (bwrap setup needs more work).
func SpawnSandboxed(homeDir, projectDir, command string, args ...string) (*exec.Cmd, string, error) {
	cmd := exec.Command(command, args...)

	cmd.Dir = projectDir

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
	cmd.Env = env

	return cmd, "", nil
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
