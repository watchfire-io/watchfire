package install

import (
	"fmt"
	"os/exec"
	"strings"
)

// Claude Code registers MCP servers through its own CLI:
//
//	claude mcp add --scope user watchfire -- watchfire mcp serve
//
// User-scope servers land in the mcpServers object of ~/.claude.json, which
// is what Status reads (no subprocess). Install delegates the write to the
// claude CLI so Claude Code's own config handling stays authoritative.

func claudeCodeClient() Client {
	return Client{
		ID:          "claude-code",
		DisplayName: "Claude Code",
		detect:      claudeCodeDetect,
		status: func() Status {
			path := claudeCodeConfigPath()
			return Status{
				Detected:   claudeCodeDetect(),
				Configured: jsonEntryConfigured(path, "mcpServers"),
				ConfigPath: path,
			}
		},
		install: claudeCodeInstall,
		snippet: claudeCodeSnippet,
	}
}

func claudeCodeDetect() bool {
	return onPath("claude") || fileExists(claudeCodeConfigPath())
}

func claudeCodeConfigPath() string {
	return homePath(".claude.json")
}

func claudeCodeSnippet() string {
	return "claude mcp add --scope user " + ServerName + " -- " + serverCommand + " " + strings.Join(serverArgs, " ")
}

func claudeCodeInstall() Result {
	path := claudeCodeConfigPath()
	if !onPath("claude") {
		return manualResult(path,
			"the claude CLI was not found on this machine; once Claude Code is installed, run",
			claudeCodeSnippet())
	}

	// Already registered with our command? Nothing to do.
	entryCurrent, entryExists := claudeCodeEntryState(path)
	if entryCurrent {
		return Result{Action: ActionUnchanged, ConfigPath: path}
	}

	// `claude mcp add` refuses to overwrite an existing name, so replace a
	// stale watchfire entry by removing it first (best-effort).
	if entryExists {
		_ = exec.Command("claude", "mcp", "remove", "--scope", "user", ServerName).Run()
	}

	addArgs := append([]string{"mcp", "add", "--scope", "user", ServerName, "--", serverCommand}, serverArgs...)
	if out, err := exec.Command("claude", addArgs...).CombinedOutput(); err != nil {
		return manualResult(path,
			fmt.Sprintf("`claude mcp add` failed (%v: %s); run this yourself", err, strings.TrimSpace(string(out))),
			claudeCodeSnippet())
	}

	action := ActionInstalled
	if entryExists {
		action = ActionUpdated
	}
	return Result{Action: action, ConfigPath: path}
}

// claudeCodeEntryState inspects ~/.claude.json: does a watchfire entry
// exist, and does it already launch our command?
func claudeCodeEntryState(path string) (current, exists bool) {
	entry, ok := readJSONEntry(path, "mcpServers")
	if !ok {
		return false, false
	}
	if cmd, _ := entry["command"].(string); cmd != serverCommand {
		return false, true
	}
	args, ok := stringSlice(entry["args"])
	if !ok || len(args) != len(serverArgs) {
		return false, true
	}
	for i := range serverArgs {
		if args[i] != serverArgs[i] {
			return false, true
		}
	}
	return true, true
}
