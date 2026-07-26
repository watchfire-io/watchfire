// Package install registers the Watchfire MCP server (`watchfire mcp serve`)
// with known MCP clients by merging a `watchfire` entry into each client's
// config file. It is shared by the CLI (`watchfire mcp install`), the
// daemon's SettingsService onboarding RPCs, and — through those RPCs — the
// TUI and GUI Settings surfaces, so it must stay dependency-light: stdlib
// plus config parsing only. No cobra, no MCP runtime, no daemon-internal
// imports.
//
// Every installer is best-effort and idempotent. A missing client or an
// unparseable config file degrades to a Manual result carrying the snippet
// to paste and where to put it; existing config content is never clobbered.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ServerName is the key every client registers the server under.
const ServerName = "watchfire"

// serverCommand/serverArgs describe how clients launch the server. The MCP
// server is stdio-only: the client spawns this command on the local machine
// and speaks MCP over its stdin/stdout. It never opens a TCP socket.
var (
	serverCommand = "watchfire"
	serverArgs    = []string{"mcp", "serve"}
)

// Action reports what an Install call did.
type Action string

const (
	// ActionInstalled means a new watchfire entry was written.
	ActionInstalled Action = "installed"
	// ActionUpdated means an existing watchfire entry was rewritten.
	ActionUpdated Action = "updated"
	// ActionUnchanged means the entry was already configured correctly.
	ActionUnchanged Action = "unchanged"
	// ActionManual means the installer could not safely write the config
	// (client missing, file unparseable, unsupported layout). Result.Snippet
	// and Result.ConfigPath tell the user what to paste where.
	ActionManual Action = "manual"
)

// Status describes a client's onboarding state on this machine.
type Status struct {
	// Detected reports whether the client appears to be installed (its CLI
	// is on PATH or its config directory exists).
	Detected bool
	// Configured reports whether the client's config already has a
	// watchfire MCP entry.
	Configured bool
	// ConfigPath is where the client's MCP config lives (informational;
	// the file may not exist yet).
	ConfigPath string
}

// Result describes the outcome of an Install call.
type Result struct {
	Action     Action
	ConfigPath string
	// Reason explains a Manual result (or carries a note otherwise).
	Reason string
	// Snippet is the exact text to paste for a Manual result.
	Snippet string
}

// Message renders the outcome of an Install call as human-readable text for
// the given client display name. It is the single source of truth for
// onboarding copy: the CLI prints it, and the daemon's InstallMcpClient RPC
// returns it as McpClientStatus.message — which is the only channel the TUI
// and GUI have for explaining a Manual result and the snippet to paste.
func (r Result) Message(displayName string) string {
	switch r.Action {
	case ActionInstalled:
		return fmt.Sprintf("Registered the Watchfire MCP server with %s.\n  Config: %s\nRestart %s to pick it up.",
			displayName, r.ConfigPath, displayName)
	case ActionUpdated:
		return fmt.Sprintf("Updated the existing watchfire entry for %s.\n  Config: %s\nRestart %s to pick it up.",
			displayName, r.ConfigPath, displayName)
	case ActionUnchanged:
		return fmt.Sprintf("%s is already configured — nothing to do.\n  Config: %s",
			displayName, r.ConfigPath)
	case ActionManual:
		return fmt.Sprintf("Could not register automatically: %s\n\n%s", r.Reason, r.Snippet)
	default:
		return fmt.Sprintf("Unknown install result %q for %s.", r.Action, displayName)
	}
}

// Client is one known MCP client Watchfire can register itself with.
type Client struct {
	// ID is the stable identifier used on the command line and in RPCs
	// (matches the agent-backend names where one exists).
	ID string
	// DisplayName is the human-readable client name.
	DisplayName string

	detect  func() bool
	status  func() Status
	install func() Result
	snippet func() string
}

// Detect reports whether the client appears to be installed on this machine.
func (c Client) Detect() bool { return c.detect() }

// Status reports the client's onboarding state.
func (c Client) Status() Status { return c.status() }

// Install registers the Watchfire MCP server with the client. It never
// returns an error: failures degrade to a Manual result.
func (c Client) Install() Result { return c.install() }

// Snippet returns the client-specific config block to paste by hand.
func (c Client) Snippet() string { return c.snippet() }

// Clients returns the known clients in display order.
func Clients() []Client {
	return []Client{
		claudeCodeClient(),
		codexClient(),
		geminiClient(),
		opencodeClient(),
		copilotClient(),
	}
}

// Get returns the client with the given ID.
func Get(id string) (Client, bool) {
	for _, c := range Clients() {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// CustomSnippet returns the generic JSON block for any other MCP client.
func CustomSnippet() string {
	return `{
  "command": "watchfire",
  "args": ["mcp", "serve"]
}`
}

// onPath reports whether an executable is on PATH.
func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// homePath joins elem under the user's home directory. It returns "" when
// the home directory cannot be determined.
func homePath(elem ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{home}, elem...)...)
}

// fileExists reports whether path exists (file or directory).
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// writeConfig writes content to path, creating parent directories.
func writeConfig(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// manualResult builds the degraded "paste this yourself" result.
func manualResult(configPath, reason, snippet string) Result {
	return Result{Action: ActionManual, ConfigPath: configPath, Reason: reason, Snippet: snippet}
}

// jsonEntryConfigured reports whether the JSON file at path parses and has
// an entry at keys... + ServerName (e.g. mcpServers.watchfire).
func jsonEntryConfigured(path string, keys ...string) bool {
	_, ok := readJSONEntry(path, keys...)
	return ok
}

// readJSONEntry returns the watchfire entry object from the JSON file at
// path under the object path keys... (nil, false when the file is missing,
// unparseable, or has no such entry — or the entry is not an object).
func readJSONEntry(path string, keys ...string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false
	}
	node := root
	for _, k := range keys {
		child, ok := node[k].(map[string]any)
		if !ok {
			return nil, false
		}
		node = child
	}
	entry, ok := node[ServerName].(map[string]any)
	return entry, ok
}
