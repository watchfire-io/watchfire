//go:build linux

package agent

import (
	"log"
	"os"
	"os/exec"
)

// platformDefaults returns Linux-specific path additions.
func platformDefaults(homeDir string) PlatformDefaults {
	return PlatformDefaults{
		// Linux doesn't need the macOS Library paths, but no extra denied either
	}
}

// spawnSandboxedPlatform tries Landlock → bwrap → unsandboxed.
func spawnSandboxedPlatform(policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	// 1. Try Landlock (kernel 5.13+)
	if landlockAvailable() {
		log.Println("[sandbox] Using Landlock (kernel LSM)")
		return spawnWithLandlock(policy, command, args...)
	}

	// 2. Try bubblewrap
	if bwrapPath, err := exec.LookPath("bwrap"); err == nil {
		log.Println("[sandbox] Using bubblewrap (bwrap)")
		return spawnWithBwrap(bwrapPath, policy, command, args...)
	}

	// 3. Unsandboxed fallback
	log.Println("[sandbox] WARNING: no sandbox available — install bubblewrap or use kernel 5.13+ for Landlock")
	return spawnUnsandboxed(policy, command, args...)
}

// spawnWithBwrap creates a sandboxed exec.Cmd using bubblewrap.
func spawnWithBwrap(bwrapPath string, policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	// Pre-create all directories that bwrap will mount.
	// bwrap cannot mkdir mount points after --ro-bind / / has made the fs read-only.
	dirsToCreate := make([]string, 0, len(policy.WritablePaths)+len(policy.DeniedPaths))
	for _, p := range policy.WritablePaths {
		dirsToCreate = append(dirsToCreate, p)
	}
	for _, p := range policy.DeniedPaths {
		dirsToCreate = append(dirsToCreate, p)
	}
	dirsToCreate = append(dirsToCreate, policy.ProjectDir)
	for _, d := range dirsToCreate {
		_ = os.MkdirAll(d, 0o755)
	}

	bwrapArgs := []string{
		// Base: read-only bind of the entire root filesystem.
		"--ro-bind", "/", "/",
		// Overlay special filesystems.
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}

	// Hide credential directories (replace with empty tmpfs).
	for _, denied := range policy.DeniedPaths {
		// Only use --tmpfs for directories; files use --ro-bind /dev/null
		info, err := os.Stat(denied)
		if err == nil && info.IsDir() {
			bwrapArgs = append(bwrapArgs, "--tmpfs", denied)
		} else if err == nil {
			bwrapArgs = append(bwrapArgs, "--ro-bind", "/dev/null", denied)
		}
		// If path doesn't exist, skip — it's already hidden by ro-bind
	}

	// Writable: project directory.
	bwrapArgs = append(bwrapArgs, "--bind", policy.ProjectDir, policy.ProjectDir)

	// Writable: agent extras, package manager caches, dev tool caches.
	for _, writable := range policy.WritablePaths {
		if writable == "/tmp" {
			continue // Already handled above with --tmpfs
		}
		bwrapArgs = append(bwrapArgs, "--bind-try", writable, writable)
	}

	// Network access + safety.
	bwrapArgs = append(bwrapArgs,
		"--share-net",
		"--die-with-parent",
		"--new-session",
		"--chdir", policy.ProjectDir,
		"--",
		command,
	)
	bwrapArgs = append(bwrapArgs, args...)

	cmd := exec.Command(bwrapPath, bwrapArgs...)
	cmd.Dir = policy.ProjectDir
	cmd.Env = buildBaseEnv(policy)

	return cmd, "", nil // No temp file cleanup needed for bwrap
}

// spawnSandboxedWithBackend routes to the requested backend on Linux.
func spawnSandboxedWithBackend(sandboxBackend string, policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	switch sandboxBackend {
	case SandboxLandlock:
		if landlockAvailable() {
			return spawnWithLandlock(policy, command, args...)
		}
		log.Printf("[sandbox] Landlock not available on this kernel, falling back to auto")
		return spawnSandboxedPlatform(policy, command, args...)
	case SandboxBwrap:
		if bwrapPath, err := exec.LookPath("bwrap"); err == nil {
			return spawnWithBwrap(bwrapPath, policy, command, args...)
		}
		log.Printf("[sandbox] bwrap not found, falling back to auto")
		return spawnSandboxedPlatform(policy, command, args...)
	case SandboxSeatbelt:
		log.Printf("[sandbox] Seatbelt not available on Linux, falling back to auto")
		return spawnSandboxedPlatform(policy, command, args...)
	default:
		return spawnSandboxedPlatform(policy, command, args...)
	}
}
