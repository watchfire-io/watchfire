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
// If Trace is true, a (trace ...) directive is prepended to log denied operations.
func GenerateProfile(homeDir, projectDir string, trace bool) string {
	var sb strings.Builder

	if trace {
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
			// Project is inside this protected dir — skip deny rule
			fmt.Fprintf(&sb, "; (deny skipped for %s — project is inside it)\n", dir)
			continue
		}
		fmt.Fprintf(&sb, "(deny file-read* (subpath %q))\n", protectedPath)
	}
	sb.WriteString("; NOTE: Keychains allowed - required for Claude Code auth\n\n")

	// WRITE ACCESS - Project + Claude config + caches + temp
	sb.WriteString("; WRITE ACCESS - Project + Claude config + caches + temp\n")
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", projectDir)
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", filepath.Join(homeDir, ".claude"))
	fmt.Fprintf(&sb, "(allow file-write* (literal %q))\n", filepath.Join(homeDir, ".claude.json"))
	fmt.Fprintf(&sb, "(allow file-write* (subpath %q))\n", filepath.Join(homeDir, "Library", "Caches", "claude-cli-nodejs"))
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
			filepath.Join(homeDir, "Library", "Caches", "claude-cli-nodejs"),
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
	profile := GenerateProfile(policy.HomeDir, policy.ProjectDir, policy.Trace)

	// Write profile to temp file
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

	// Build: sandbox-exec -f <tmpfile> <command> <args...>
	sandboxArgs := []string{"-f", tmpFile.Name(), command}
	sandboxArgs = append(sandboxArgs, args...)
	cmd := exec.Command("sandbox-exec", sandboxArgs...)

	cmd.Dir = policy.ProjectDir
	env := buildBaseEnv(policy.ProjectDir)

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

// spawnSandboxedWithBackend routes to the requested backend on macOS.
func spawnSandboxedWithBackend(backend string, policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	switch backend {
	case SandboxSeatbelt:
		return spawnSandboxedPlatform(policy, command, args...)
	default:
		log.Printf("[sandbox] Backend %q not available on macOS, falling back to seatbelt", backend)
		return spawnSandboxedPlatform(policy, command, args...)
	}
}
