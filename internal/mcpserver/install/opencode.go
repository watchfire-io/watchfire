package install

import (
	"os"
	"path/filepath"
)

// opencode reads MCP servers from the mcp object in its global config,
// ~/.config/opencode/opencode.json (XDG_CONFIG_HOME respected). Local
// servers use type "local" with the full command line as one array.
// opencode also accepts JSONC; a commented config fails the strict JSON
// parse and degrades to manual instructions rather than losing comments.

func opencodeClient() Client {
	return Client{
		ID:          "opencode",
		DisplayName: "opencode",
		detect:      opencodeDetect,
		status: func() Status {
			path := opencodeConfigPath()
			return Status{
				Detected:   opencodeDetect(),
				Configured: jsonEntryConfigured(path, "mcp"),
				ConfigPath: path,
			}
		},
		install: opencodeInstall,
		snippet: opencodeSnippet,
	}
}

func opencodeDetect() bool {
	return onPath("opencode") || fileExists(opencodeConfigDir())
}

func opencodeConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return homePath(".config", "opencode")
}

func opencodeConfigPath() string {
	return filepath.Join(opencodeConfigDir(), "opencode.json")
}

func opencodeEntry() map[string]any {
	return map[string]any{
		"type":    "local",
		"command": append([]string{serverCommand}, serverArgs...),
		"enabled": true,
	}
}

func opencodeSnippet() string {
	return `{
  "mcp": {
    "watchfire": {
      "type": "local",
      "command": ["watchfire", "mcp", "serve"],
      "enabled": true
    }
  }
}`
}

func opencodeInstall() Result {
	path := opencodeConfigPath()
	if !opencodeDetect() {
		return manualResult(path, "opencode was not found on this machine; once opencode is installed, merge this into "+path, opencodeSnippet())
	}
	return installJSONClient(path, []string{"mcp"}, opencodeEntry(), opencodeSnippet())
}
