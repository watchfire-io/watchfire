package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// OpenAI Codex reads MCP servers from [mcp_servers.<name>] tables in
// ~/.codex/config.toml. The merge is line-based so existing content —
// including comments and unrelated tables — survives verbatim; the parse
// step exists to detect malformed files (degrade to manual) and existing
// entries (idempotency).

func codexClient() Client {
	return Client{
		ID:          "codex",
		DisplayName: "OpenAI Codex",
		detect:      codexDetect,
		status: func() Status {
			path := codexConfigPath()
			return Status{
				Detected:   codexDetect(),
				Configured: codexConfigured(path),
				ConfigPath: path,
			}
		},
		install: codexInstall,
		snippet: codexSnippet,
	}
}

func codexDetect() bool {
	return onPath("codex") || fileExists(homePath(".codex"))
}

func codexConfigPath() string {
	return homePath(".codex", "config.toml")
}

func codexSnippet() string {
	return "[mcp_servers." + ServerName + "]\n" +
		`command = "` + serverCommand + `"` + "\n" +
		`args = ["` + strings.Join(serverArgs, `", "`) + `"]`
}

func codexConfigured(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	servers, ok := parsed["mcp_servers"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = servers[ServerName]
	return ok
}

func codexInstall() Result {
	path := codexConfigPath()
	snippet := codexSnippet()
	if !codexDetect() {
		return manualResult(path, "the codex CLI was not found on this machine; once Codex is installed, add this to "+path, snippet)
	}

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return manualResult(path, fmt.Sprintf("cannot read %s: %v", path, err), snippet)
	}

	merged, action, err := mergeCodexTOML(raw)
	if err != nil {
		return manualResult(path, fmt.Sprintf("cannot safely edit %s: %v", path, err), snippet)
	}
	if action == ActionUnchanged {
		return Result{Action: ActionUnchanged, ConfigPath: path}
	}
	if err := writeConfig(path, merged); err != nil {
		return manualResult(path, fmt.Sprintf("cannot write %s: %v", path, err), snippet)
	}
	return Result{Action: action, ConfigPath: path}
}

// mergeCodexTOML merges the watchfire server table into raw TOML content.
// Unparseable input returns an error (callers degrade to manual). A correct
// existing entry returns ActionUnchanged with nil bytes. Otherwise the
// [mcp_servers.watchfire] section is appended or its command/args lines
// rewritten in place, leaving every other line (comments included) intact.
func mergeCodexTOML(raw []byte) ([]byte, Action, error) {
	var parsed map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := toml.Unmarshal(raw, &parsed); err != nil {
			return nil, ActionManual, fmt.Errorf("parse existing config: %w", err)
		}
	}

	servers, _ := parsed["mcp_servers"].(map[string]any)
	existing, exists := servers[ServerName]
	if exists && codexEntryCurrent(existing) {
		return nil, ActionUnchanged, nil
	}

	var out []byte
	action := ActionInstalled
	if exists {
		rewritten, ok := rewriteCodexSection(string(raw))
		if !ok {
			return nil, ActionManual, errors.New("the existing [mcp_servers.watchfire] entry uses a layout this installer cannot safely rewrite")
		}
		out, action = []byte(rewritten), ActionUpdated
	} else {
		out = bytes.TrimRight(raw, "\n")
		if len(bytes.TrimSpace(out)) > 0 {
			out = append(out, []byte("\n\n")...)
		} else {
			out = nil
		}
		out = append(out, []byte(codexSnippet()+"\n")...)
	}

	// Never write something Codex itself cannot parse.
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return nil, ActionManual, fmt.Errorf("merge produced invalid TOML (not written): %w", err)
	}
	return out, action, nil
}

// codexEntryCurrent reports whether a parsed [mcp_servers.watchfire] table
// already launches the server with our command and args.
func codexEntryCurrent(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if cmd, _ := m["command"].(string); cmd != serverCommand {
		return false
	}
	args, ok := stringSlice(m["args"])
	if !ok || len(args) != len(serverArgs) {
		return false
	}
	for i := range serverArgs {
		if args[i] != serverArgs[i] {
			return false
		}
	}
	return true
}

func stringSlice(v any) ([]string, bool) {
	switch vv := v.(type) {
	case []string:
		return vv, true
	case []any:
		out := make([]string, 0, len(vv))
		for _, e := range vv {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// rewriteCodexSection replaces the command/args lines of the
// [mcp_servers.watchfire] section with the canonical ones, preserving the
// section's other keys, its subtables, and the rest of the file. Returns
// false when no dotted-table header exists (e.g. the entry was written as
// an inline table) — rewriting that safely is not worth the complexity, so
// the caller degrades to manual instructions.
func rewriteCodexSection(content string) (string, bool) {
	lines := strings.Split(content, "\n")

	headerIdx := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "[mcp_servers."+ServerName+"]" || t == `[mcp_servers."`+ServerName+`"]` {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		return "", false
	}

	// The section body runs until the next table header (subtables such as
	// [mcp_servers.watchfire.env] end it too, and are left untouched).
	end := len(lines)
	for i := headerIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			end = i
			break
		}
	}

	var kept []string
	for i := headerIdx + 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if isTOMLKey(t, "command") {
			continue
		}
		if isTOMLKey(t, "args") {
			// Skip a multi-line array value.
			depth := strings.Count(lines[i], "[") - strings.Count(lines[i], "]")
			for depth > 0 && i+1 < end {
				i++
				depth += strings.Count(lines[i], "[") - strings.Count(lines[i], "]")
			}
			continue
		}
		kept = append(kept, lines[i])
	}

	out := make([]string, 0, len(lines))
	out = append(out, lines[:headerIdx+1]...)
	out = append(out, `command = "`+serverCommand+`"`, `args = ["`+strings.Join(serverArgs, `", "`)+`"]`)
	out = append(out, kept...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), true
}

// isTOMLKey reports whether a trimmed line assigns the given bare key.
func isTOMLKey(trimmed, key string) bool {
	rest, ok := strings.CutPrefix(trimmed, key)
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(rest), "=")
}
