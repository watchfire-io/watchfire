//go:build darwin

package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// protectedUserDirs lists macOS directories that are denied by default to prevent TCC prompts.
// A deny rule is skipped if the project directory is inside that protected directory.
var protectedUserDirs = []string{"Desktop", "Documents", "Downloads", "Music", "Movies", "Pictures"}

// GenerateProfile generates a macOS sandbox-exec profile for the given policy.
// Agent-specific writable paths are read from policy.Extras.
func GenerateProfile(policy SandboxPolicy) string {
	var sb strings.Builder

	homeDir := policy.HomeDir
	projectDir := policy.ProjectDir

	if policy.Trace {
		sb.WriteString("(trace \"/tmp/watchfire-sandbox-trace.sb\")\n")
	}

	sb.WriteString("(version 1)\n(deny default)\n\n")
	sb.WriteString("; READ ACCESS - Allow all, deny sensitive\n")
	sb.WriteString("(allow file-read* (subpath \"/\"))\n\n")

	// DENY sensitive credential paths
	sb.WriteString("; DENY sensitive credential paths\n")
	for _, dir := range []string{".ssh", ".aws", ".gnupg"} {
		fmt.Fprintf(&sb, "(deny file-read* (subpath %q))\n", filepath.Join(homeDir, dir))
	}
	for _, file := range []string{".netrc", ".npmrc"} {
		fmt.Fprintf(&sb, "(deny file-read* (literal %q))\n", filepath.Join(homeDir, file))
	}
	sb.WriteString("\n")

	// DENY protected user folders (skip if project is inside the folder)
	sb.WriteString("; DENY protected user folders (prevents TCC prompts)\n")
	for _, dir := range protectedUserDirs {
		protectedPath := filepath.Join(homeDir, dir)
		if strings.HasPrefix(projectDir, protectedPath+"/") || projectDir == protectedPath {
			fmt.Fprintf(&sb, "; (deny skipped for %s — project is inside it)\n", dir)
			continue
		}
		fmt.Fprintf(&sb, "(deny file-read* (subpath %q))\n", protectedPath)
	}
	sb.WriteString("; NOTE: Keychains allowed - required for agent auth\n\n")

	// WRITE ACCESS - Project + agent extras + caches + temp
	sb.WriteString("; WRITE ACCESS - Project + agent extras + caches + temp\n")
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", projectDir)
	for _, p := range policy.Extras.WritableSubpaths {
		fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", p)
	}
	for _, p := range policy.Extras.WritableLiterals {
		fmt.Fprintf(&sb, "(allow file-write* (literal %q))\n", p)
	}
	for _, p := range policy.Extras.CachePatterns {
		fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", p)
	}
	sb.WriteString("(allow file-write* (subpath \"/tmp\"))\n")
	sb.WriteString("(allow file-write* (subpath \"/private/tmp\"))\n")
	sb.WriteString("(allow file-write* (subpath \"/var/folders\"))\n")
	sb.WriteString("(allow file-write* (subpath \"/private/var/folders\"))\n\n")

	// PACKAGE MANAGER CACHES
	sb.WriteString("; PACKAGE MANAGER CACHES\n")
	for _, dir := range []string{".npm", ".yarn", ".pnpm-store", ".cache", ".local/share/pnpm"} {
		fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", filepath.Join(homeDir, dir))
	}
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", filepath.Join(homeDir, "Library", "Caches", "npm"))
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", filepath.Join(homeDir, "Library", "Caches", "yarn"))
	sb.WriteString("\n")

	// CLI TOOL CONFIG
	sb.WriteString("; CLI TOOL CONFIG (Vercel, Firebase, gcloud, etc.)\n")
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n\n", filepath.Join(homeDir, "Library", "Application Support"))

	// KEYCHAIN - Agent auth token persistence
	// Agents like Claude Code refresh OAuth tokens via macOS's Security
	// framework on startup, which writes to the login keychain's SQLite DB.
	// Without this, refresh fails and the agent falls through to API-key
	// billing precedence, producing spurious "out of extra usage" errors
	// on active Max/Pro subscriptions. Scoped to the login keychain (and
	// its SQLite WAL/SHM sidecars) — other keychains stay protected.
	sb.WriteString("; KEYCHAIN - Agent auth token persistence\n")
	keychainDir := filepath.Join(homeDir, "Library", "Keychains")
	for _, suffix := range []string{"login.keychain-db", "login.keychain-db-shm", "login.keychain-db-wal"} {
		fmt.Fprintf(&sb, "(allow file-write* (literal %q))\n", filepath.Join(keychainDir, suffix))
	}
	sb.WriteString("\n")

	// DEV TOOL CACHES
	sb.WriteString("; DEV TOOL CACHES\n")
	for _, dir := range []string{".cargo", "go", ".rustup"} {
		fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", filepath.Join(homeDir, dir))
	}
	sb.WriteString("\n")

	// PROTECTED - Block writes even in project
	sb.WriteString("; PROTECTED - Block writes even in project\n")
	sb.WriteString("(deny file-write* (regex #\"/\\.env($|[^/]*)\"))\n")
	fmt.Fprintf(&sb, "(deny file-write* (subpath %q))\n\n", filepath.Join(projectDir, ".git", "hooks"))

	// NETWORK, DEVICES, PROCESS, IPC
	sb.WriteString("; NETWORK, DEVICES, PROCESS, IPC\n")
	sb.WriteString("(allow network*)\n")
	sb.WriteString("(allow file-read* (subpath \"/dev\"))\n")
	sb.WriteString("(allow file-write* (subpath \"/dev\"))\n")
	sb.WriteString("(allow process-exec*)\n")
	sb.WriteString("(allow process-fork)\n")
	sb.WriteString("(allow process-info*)\n")
	sb.WriteString("(allow signal)\n")
	sb.WriteString("(allow mach*)\n")
	sb.WriteString("(allow sysctl*)\n")
	sb.WriteString("(allow ipc*)\n")
	sb.WriteString("(allow file-ioctl)\n")

	return sb.String()
}

// platformDefaults returns macOS-specific path additions.
// projectDir is used to avoid denying access to protected dirs that contain the project.
func platformDefaults(homeDir string) PlatformDefaults {
	return PlatformDefaults{
		ExtraWritable: []string{
			"/private/tmp",
			"/var/folders",
			"/private/var/folders",
			filepath.Join(homeDir, "Library", "Caches", "npm"),
			filepath.Join(homeDir, "Library", "Caches", "yarn"),
			filepath.Join(homeDir, "Library", "Application Support"),
		},
		ExtraDenied: []string{
			filepath.Join(homeDir, "Desktop"),
			filepath.Join(homeDir, "Documents"),
			filepath.Join(homeDir, "Downloads"),
			filepath.Join(homeDir, "Music"),
			filepath.Join(homeDir, "Movies"),
			filepath.Join(homeDir, "Pictures"),
		},
	}
}

// spawnSandboxedPlatform creates a sandboxed exec.Cmd using macOS sandbox-exec.
func spawnSandboxedPlatform(policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	profile := GenerateProfile(policy)

	tmpFile, err := os.CreateTemp("", "watchfire-sandbox-*.sb")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create sandbox profile: %w", err)
	}
	if _, err := tmpFile.WriteString(profile); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return nil, "", fmt.Errorf("failed to write sandbox profile: %w", err)
	}
	_ = tmpFile.Close()

	sandboxArgs := []string{"-f", tmpFile.Name(), command}
	sandboxArgs = append(sandboxArgs, args...)
	cmd := exec.Command("sandbox-exec", sandboxArgs...)

	cmd.Dir = policy.ProjectDir
	env := buildBaseEnv(policy)

	path := os.Getenv("PATH")
	homeDir := policy.HomeDir
	for _, p := range []string{
		filepath.Join(homeDir, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	} {
		if !strings.Contains(path, p) {
			path = p + ":" + path
		}
	}
	env = setEnv(env, "PATH", path)
	cmd.Env = env

	return cmd, tmpFile.Name(), nil
}

// spawnSandboxedWithBackend routes to the requested backend on macOS.
func spawnSandboxedWithBackend(sandboxBackend string, policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	switch sandboxBackend {
	case SandboxSeatbelt:
		return spawnSandboxedPlatform(policy, command, args...)
	default:
		log.Printf("[sandbox] Backend %q not available on macOS, falling back to seatbelt", sandboxBackend)
		return spawnSandboxedPlatform(policy, command, args...)
	}
}
