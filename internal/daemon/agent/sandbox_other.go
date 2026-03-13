//go:build !darwin

package agent

import (
	"os"
	"os/exec"
)

// SpawnSandboxed creates an exec.Cmd that runs the given command.
// On non-macOS platforms, no sandbox is applied — the command runs directly.
// The caller is responsible for starting the process (e.g. via PTY).
func SpawnSandboxed(homeDir, projectDir, command string, args ...string) (*exec.Cmd, string, error) {
	cmd := exec.Command(command, args...)

	// Set working directory to the project
	cmd.Dir = projectDir

	// Set environment
	env := os.Environ()
	env = removeEnv(env, "CLAUDECODE") // prevent nested-session detection
	env = setEnv(env, "TERM", "xterm-256color")
	env = setEnv(env, "COLORTERM", "truecolor")
	cmd.Env = env

	return cmd, "", nil
}
