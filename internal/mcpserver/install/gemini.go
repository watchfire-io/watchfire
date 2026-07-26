package install

// Gemini CLI reads MCP servers from the top-level mcpServers object in
// ~/.gemini/settings.json (user scope).

func geminiClient() Client {
	return Client{
		ID:          "gemini",
		DisplayName: "Gemini CLI",
		detect:      geminiDetect,
		status: func() Status {
			path := geminiConfigPath()
			return Status{
				Detected:   geminiDetect(),
				Configured: jsonEntryConfigured(path, "mcpServers"),
				ConfigPath: path,
			}
		},
		install: geminiInstall,
		snippet: geminiSnippet,
	}
}

func geminiDetect() bool {
	return onPath("gemini") || fileExists(homePath(".gemini"))
}

func geminiConfigPath() string {
	return homePath(".gemini", "settings.json")
}

func geminiEntry() map[string]any {
	return map[string]any{
		"command": serverCommand,
		"args":    serverArgs,
	}
}

func geminiSnippet() string {
	return `{
  "mcpServers": {
    "watchfire": {
      "command": "watchfire",
      "args": ["mcp", "serve"]
    }
  }
}`
}

func geminiInstall() Result {
	path := geminiConfigPath()
	if !geminiDetect() {
		return manualResult(path, "the gemini CLI was not found on this machine; once Gemini CLI is installed, merge this into "+path, geminiSnippet())
	}
	return installJSONClient(path, []string{"mcpServers"}, geminiEntry(), geminiSnippet())
}
