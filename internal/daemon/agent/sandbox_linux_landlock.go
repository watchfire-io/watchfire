//go:build linux

package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// landlockAvailable probes the kernel for Landlock ABI support.
func landlockAvailable() bool {
	// Try to create a minimal Landlock ruleset; if the syscall succeeds, Landlock is available.
	attr := llsyscall.RulesetAttr{
		HandledAccessFS: llsyscall.AccessFSReadFile,
	}
	fd, err := llsyscall.LandlockCreateRuleset(&attr, 0)
	if err != nil {
		return false
	}
	// Close the fd — we were just probing.
	syscall.Close(fd)
	return true
}

// landlockConfig is the JSON structure passed from daemon to the helper subprocess.
type landlockConfig struct {
	WritablePaths []string `json:"writable_paths"`
	DeniedPaths   []string `json:"denied_paths"`
	ProjectDir    string   `json:"project_dir"`
	Command       string   `json:"command"`
	Args          []string `json:"args"`
}

// spawnWithLandlock creates a sandboxed exec.Cmd by re-invoking the daemon binary
// with --sandbox-exec, which applies Landlock restrictions then exec()s the target.
func spawnWithLandlock(policy SandboxPolicy, command string, args ...string) (*exec.Cmd, string, error) {
	cfg := landlockConfig{
		WritablePaths: append([]string{policy.ProjectDir}, policy.WritablePaths...),
		DeniedPaths:   policy.DeniedPaths,
		ProjectDir:    policy.ProjectDir,
		Command:       command,
		Args:          args,
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal landlock config: %w", err)
	}

	// Write config to a temp file
	tmpFile, err := os.CreateTemp("", "watchfire-landlock-*.json")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create landlock config file: %w", err)
	}
	if _, err := tmpFile.Write(cfgJSON); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return nil, "", fmt.Errorf("failed to write landlock config: %w", err)
	}
	_ = tmpFile.Close()

	// Re-invoke the daemon binary with --sandbox-exec <config-path>
	daemonPath, err := os.Executable()
	if err != nil {
		_ = os.Remove(tmpFile.Name())
		return nil, "", fmt.Errorf("failed to resolve daemon path: %w", err)
	}

	cmd := exec.Command(daemonPath, "--sandbox-exec", tmpFile.Name())
	cmd.Dir = policy.ProjectDir
	cmd.Env = buildBaseEnv(policy)

	return cmd, tmpFile.Name(), nil
}

// RunLandlockHelper is the entry point called when the daemon detects --sandbox-exec.
// It reads the config, applies Landlock restrictions, then exec()s the target command.
// This function never returns on success (exec replaces the process).
func RunLandlockHelper(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "watchfired --sandbox-exec: missing config path\n")
		os.Exit(1)
	}

	configPath := args[0]
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watchfired --sandbox-exec: failed to read config: %v\n", err)
		os.Exit(1)
	}

	// Clean up config file immediately
	_ = os.Remove(configPath)

	var cfg landlockConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "watchfired --sandbox-exec: failed to parse config: %v\n", err)
		os.Exit(1)
	}

	// Build Landlock rules
	rules := make([]landlock.Rule, 0, len(cfg.WritablePaths)+1)

	// Read-only access to the entire filesystem
	rules = append(rules, landlock.RODirs("/"))

	// Read-write access to writable paths
	for _, p := range cfg.WritablePaths {
		if _, err := os.Stat(p); err == nil {
			rules = append(rules, landlock.RWDirs(p))
		}
	}

	// Apply Landlock restrictions
	// Use BestEffort to degrade gracefully on older ABI versions
	if err := landlock.V5.BestEffort().RestrictPaths(rules...); err != nil {
		log.Printf("[sandbox] WARNING: Landlock restriction failed: %v — running unsandboxed", err)
		// Fall through to exec anyway — better to run unsandboxed than fail
	}

	// Resolve command path before exec
	cmdPath, err := exec.LookPath(cfg.Command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watchfired --sandbox-exec: command not found: %s\n", cfg.Command)
		os.Exit(1)
	}

	// exec() replaces this process with the target command
	argv := append([]string{cfg.Command}, cfg.Args...)
	if err := syscall.Exec(cmdPath, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "watchfired --sandbox-exec: exec failed: %v\n", err)
		os.Exit(1)
	}
}
