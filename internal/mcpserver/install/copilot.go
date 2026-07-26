package install

import (
	"os"
	"path/filepath"
)

// GitHub Copilot CLI reads MCP servers from the mcpServers object in
// ~/.copilot/mcp-config.json (the directory is overridable via
// COPILOT_HOME). Local servers use type "local"; tools "*" enables every
// tool the server exposes.

func copilotClient() Client {
	return Client{
		ID:          "copilot",
		DisplayName: "GitHub Copilot CLI",
		detect:      copilotDetect,
		status: func() Status {
			path := copilotConfigPath()
			return Status{
				Detected:   copilotDetect(),
				Configured: jsonEntryConfigured(path, "mcpServers"),
				ConfigPath: path,
			}
		},
		install: copilotInstall,
		snippet: copilotSnippet,
	}
}

func copilotDetect() bool {
	return onPath("copilot") || fileExists(copilotConfigDir())
}

func copilotConfigDir() string {
	if custom := os.Getenv("COPILOT_HOME"); custom != "" {
		return custom
	}
	return homePath(".copilot")
}

func copilotConfigPath() string {
	return filepath.Join(copilotConfigDir(), "mcp-config.json")
}

func copilotEntry() map[string]any {
	return map[string]any{
		"type":    "local",
		"command": serverCommand,
		"args":    serverArgs,
		"tools":   []string{"*"},
	}
}

func copilotSnippet() string {
	return `{
  "mcpServers": {
    "watchfire": {
      "type": "local",
      "command": "watchfire",
      "args": ["mcp", "serve"],
      "tools": ["*"]
    }
  }
}`
}

func copilotInstall() Result {
	path := copilotConfigPath()
	if !copilotDetect() {
		return manualResult(path, "the copilot CLI was not found on this machine; once Copilot CLI is installed, merge this into "+path, copilotSnippet())
	}
	return installJSONClient(path, []string{"mcpServers"}, copilotEntry(), copilotSnippet())
}
