//go:build !darwin && !linux

package agent

import (
	"log"
	"os/exec"
)

// platformDefaults returns empty defaults for unsupported platforms.
func platformDefaults(homeDir string) PlatformDefaults {
	return PlatformDefaults{}
}

// spawnSandboxedPlatform logs a warning and runs unsandboxed on unsupported platforms.
func spawnSandboxedPlatform(policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	log.Println("[sandbox] WARNING: no sandbox available on this platform — running unsandboxed")
	return spawnUnsandboxed(policy, command, args...)
}

// spawnSandboxedWithBackend falls back to unsandboxed on unsupported platforms.
func spawnSandboxedWithBackend(sandboxBackend string, policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	log.Printf("[sandbox] Backend %q not available on this platform — running unsandboxed", sandboxBackend)
	return spawnUnsandboxed(policy, command, args...)
}
