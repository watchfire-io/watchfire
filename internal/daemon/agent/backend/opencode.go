package backend

// opencode backend notes (research April 2026)
//
// Upstream: https://github.com/sst/opencode (https://opencode.ai)
// Executable: "opencode". Installed by Homebrew (/opt/homebrew/bin/opencode,
// /usr/local/bin/opencode), npm (opencode-ai package, usually on $PATH), the
// curl install script (~/.opencode/bin/opencode), and several others.
//
// Non-interactive invocation is `opencode run [message..]` — the prompt is
// passed as a positional argument. It bootstraps a temporary server, streams
// the response to stdout, and exits. This is what Watchfire uses (the default
// TUI mode is a full-screen Bubbletea-style UI unsuitable for our PTY stream).
//
// Yolo / bypass-approvals: opencode has no CLI flag equivalent to Claude's
// --dangerously-skip-permissions. Approvals are driven by config: we write an
// opencode.json with `"permission": "allow"` into the per-session config dir,
// and also set OPENCODE_PERMISSION='{"*":"allow"}' as a belt-and-braces env
// var (docs describe it as "inlined json permissions config").
//
// System prompt delivery: opencode reads AGENTS.md from the global config
// dir (~/.config/opencode/AGENTS.md) and the project root. OPENCODE_CONFIG_DIR
// overrides the global config dir, so we point it at a per-session directory
// and drop our composed Watchfire prompt there as AGENTS.md. The user's
// project-level AGENTS.md (if present in the worktree) still layers on top,
// matching the Codex backend's behaviour.
//
// Session storage: opencode writes under OPENCODE_DATA_DIR (default
// ~/.local/share/opencode), one JSON file per message in
// storage/message/{sessionID}/*.json plus a session metadata file in
// storage/session/{projectHash}/{sessionID}.json. We set a per-session
// OPENCODE_DATA_DIR so (a) the transcript is trivially discoverable (we own
// the directory) and (b) opencode's SQLite/state doesn't interleave with the
// user's interactive sessions.
//
// Auth: authentication lives under the user's global ~/.config/opencode.
// Because we replace the config dir via OPENCODE_CONFIG_DIR, we symlink the
// user's real config entries into the per-session dir so existing logins
// continue to work. We overwrite AGENTS.md and opencode.json deliberately.
//
// Caveats documented in code:
//  1. opencode's message JSON schema is not formally documented — the
//     transcript formatter is best-effort and intentionally forgiving.
//  2. `opencode run` is a single-turn non-interactive invocation; a task's
//     system + user prompt must drive the agent to completion in one run.
//     That matches how Watchfire's other backends are already expected to
//     behave (the agent writes `status: done` and exits).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// OpencodeBackendName is the registry key for the opencode backend.
const OpencodeBackendName = "opencode"

// Opencode implements Backend for the opencode CLI.
type Opencode struct{}

// Name returns the stable registry identifier.
func (o *Opencode) Name() string { return OpencodeBackendName }

// DisplayName returns the human-readable label.
func (o *Opencode) DisplayName() string { return "opencode" }

// ResolveExecutable locates the `opencode` binary: settings map → PATH →
// well-known install locations.
func (o *Opencode) ResolveExecutable(s *models.Settings) (string, error) {
	if s != nil && s.Agents != nil {
		if cfg, ok := s.Agents[OpencodeBackendName]; ok && cfg != nil && cfg.Path != "" {
			if _, err := os.Stat(cfg.Path); err == nil {
				return cfg.Path, nil
			}
		}
	}

	if path, err := exec.LookPath("opencode"); err == nil {
		return path, nil
	}

	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		filepath.Join(homeDir, ".opencode", "bin", "opencode"),
		filepath.Join(homeDir, ".local", "bin", "opencode"),
	}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/opencode",
			"/usr/local/bin/opencode",
		)
	} else {
		fallbacks = append(fallbacks, "/usr/local/bin/opencode")
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("opencode binary not found. Install opencode (https://opencode.ai) or set path in ~/.watchfire/settings.yaml")
}

// opencodeSessionHomeForName returns the per-session dir (containing config/
// and data/ subdirs) derived deterministically from the session name so
// InstallSystemPrompt, BuildCommand and LocateTranscript all agree.
func opencodeSessionHomeForName(sessionName string) (string, error) {
	if sessionName == "" {
		return "", fmt.Errorf("opencode: session name required")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".watchfire", "opencode-home", sanitizeSessionName(sessionName)), nil
}

func opencodeConfigDir(sessionName string) (string, error) {
	home, err := opencodeSessionHomeForName(sessionName)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config"), nil
}

func opencodeDataDir(sessionName string) (string, error) {
	home, err := opencodeSessionHomeForName(sessionName)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "data"), nil
}

// BuildCommand produces the opencode PTY invocation. Uses the `run` subcommand
// for non-interactive execution with the prompt passed positionally.
func (o *Opencode) BuildCommand(opts CommandOpts) (Command, error) {
	cfgDir, err := opencodeConfigDir(opts.SessionName)
	if err != nil {
		return Command{}, err
	}
	dataDir, err := opencodeDataDir(opts.SessionName)
	if err != nil {
		return Command{}, err
	}

	args := []string{"run"}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}
	if opts.InitialPrompt != "" {
		args = append(args, opts.InitialPrompt)
	}

	env := append(os.Environ(),
		"OPENCODE_CONFIG_DIR="+cfgDir,
		"OPENCODE_DATA_DIR="+dataDir,
		`OPENCODE_PERMISSION={"*":"allow"}`,
	)

	return Command{
		Args:               args,
		Env:                env,
		PasteInitialPrompt: false,
	}, nil
}

// SandboxExtras returns the opencode-specific paths the sandbox policy must
// allow. We own the per-session home under ~/.watchfire/opencode-home; the
// user's real ~/.config/opencode is read-only (we never write there; symlinks
// in the session dir resolve back into it, so the sandbox must permit reads).
func (o *Opencode) SandboxExtras() SandboxExtras {
	return SandboxExtras{
		WritableSubpaths: []string{"~/.watchfire/opencode-home"},
		WritableLiterals: []string{},
		CachePatterns:    []string{"~/.config/opencode", "~/.local/share/opencode"},
	}
}

// InstallSystemPrompt materialises a per-session OPENCODE_CONFIG_DIR populated
// with:
//   - symlinks to every file in the user's real ~/.config/opencode (preserves
//     auth/config) — skipped if that dir doesn't exist yet.
//   - AGENTS.md containing the composed Watchfire system prompt (overwrites
//     any symlinked AGENTS.md from the user's config).
//   - opencode.json enabling yolo permission mode (overwrites any symlinked
//     opencode.json). We try to preserve the user's other settings by
//     reading their opencode.json and merging `"permission": "allow"` into
//     it; if that fails we fall back to a minimal override.
//
// workDir is kept to satisfy the Backend interface but is not used; the
// manager calls InstallSystemPromptForSession instead.
func (o *Opencode) InstallSystemPrompt(workDir, composedPrompt string) error {
	sessionName := filepath.Base(workDir)
	return o.installForSession(sessionName, composedPrompt)
}

// InstallSystemPromptForSession is the explicit variant the manager uses when
// it knows the session name directly. Prefer this over InstallSystemPrompt.
func (o *Opencode) InstallSystemPromptForSession(sessionName, composedPrompt string) error {
	return o.installForSession(sessionName, composedPrompt)
}

func (o *Opencode) installForSession(sessionName, composedPrompt string) error {
	cfgDir, err := opencodeConfigDir(sessionName)
	if err != nil {
		return err
	}
	dataDir, err := opencodeDataDir(sessionName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return fmt.Errorf("create opencode config dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create opencode data dir: %w", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	userCfg := filepath.Join(userHome, ".config", "opencode")

	// Symlink the user's real config entries (auth, providers, agents,
	// commands, skills, etc.) so any existing login is reused. We do this
	// before writing AGENTS.md / opencode.json so the overwrites below win.
	if entries, derr := os.ReadDir(userCfg); derr == nil {
		for _, e := range entries {
			name := e.Name()
			if name == "AGENTS.md" || name == "opencode.json" || name == "opencode.jsonc" {
				// Will be overwritten; skip so the Symlink doesn't fail on
				// "file exists" when we write our own versions below.
				continue
			}
			link := filepath.Join(cfgDir, name)
			if _, lerr := os.Lstat(link); lerr == nil {
				if rerr := os.Remove(link); rerr != nil {
					return fmt.Errorf("remove stale symlink %s: %w", link, rerr)
				}
			}
			target := filepath.Join(userCfg, name)
			if err := os.Symlink(target, link); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
			}
		}
	}

	// Write AGENTS.md with the composed Watchfire system prompt.
	agentsPath := filepath.Join(cfgDir, "AGENTS.md")
	if _, err := os.Lstat(agentsPath); err == nil {
		_ = os.Remove(agentsPath)
	}
	if err := os.WriteFile(agentsPath, []byte(composedPrompt), 0o644); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}

	// Write opencode.json enabling yolo permission mode. Merge with the
	// user's global config if parseable to preserve model/provider choices.
	cfg := map[string]any{}
	if data, rerr := os.ReadFile(filepath.Join(userCfg, "opencode.json")); rerr == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	cfg["$schema"] = "https://opencode.ai/config.json"
	cfg["permission"] = "allow"
	cfgPath := filepath.Join(cfgDir, "opencode.json")
	if _, err := os.Lstat(cfgPath); err == nil {
		_ = os.Remove(cfgPath)
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode.json: %w", err)
	}
	if err := os.WriteFile(cfgPath, out, 0o644); err != nil {
		return fmt.Errorf("write opencode.json: %w", err)
	}

	return nil
}

// LocateTranscript collates opencode's per-message JSON files into a single
// JSONL file under the session home and returns its path. We do the collation
// here (rather than in FormatTranscript) so the existing CopyTranscript
// pipeline copies a single file to the log directory, matching how other
// backends behave.
func (o *Opencode) LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error) {
	if sessionHint == "" {
		return "", fmt.Errorf("sessionHint required for opencode transcript lookup")
	}
	dataDir, err := opencodeDataDir(sessionHint)
	if err != nil {
		return "", err
	}

	messageRoot := filepath.Join(dataDir, "storage", "message")
	var files []string
	_ = filepath.Walk(messageRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		return "", fmt.Errorf("no opencode messages under %s", messageRoot)
	}

	// Sort by filename so messages appear in creation order. opencode uses
	// time-ordered IDs (e.g. msg_01J...) so lexical sort matches chronology.
	sort.Strings(files)

	sessionHome, err := opencodeSessionHomeForName(sessionHint)
	if err != nil {
		return "", err
	}
	out := filepath.Join(sessionHome, "transcript.jsonl")
	f, err := os.Create(out)
	if err != nil {
		return "", fmt.Errorf("create transcript.jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	for _, p := range files {
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		// Strip trailing newline, write as single JSONL line.
		data = []byte(strings.TrimRight(string(data), "\n"))
		// If the file is a pretty-printed JSON, collapse whitespace by
		// re-marshalling. This keeps the .jsonl file one-object-per-line.
		var obj any
		if jerr := json.Unmarshal(data, &obj); jerr == nil {
			if minified, merr := json.Marshal(obj); merr == nil {
				data = minified
			}
		}
		_, _ = w.Write(data)
		_, _ = w.WriteString("\n")
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	return out, nil
}

// opencodeMessage is the best-effort schema for a single message JSON file.
// opencode's actual schema is not formally documented; fields observed:
//   - role: "user" | "assistant" | "system"
//   - parts: [{type, text, name, command, ...}]
//   - content: sometimes a plain string, sometimes an array of parts
//   - text: fallback when parts are absent
type opencodeMessage struct {
	Role    string              `json:"role"`
	Parts   []opencodeMessagePart `json:"parts"`
	Content json.RawMessage     `json:"content"`
	Text    string              `json:"text"`
	// Some opencode builds wrap the message under "info" or "message".
	Info    json.RawMessage `json:"info"`
	Message json.RawMessage `json:"message"`
}

type opencodeMessagePart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Name    string `json:"name"`
	Tool    string `json:"tool"`
	Command string `json:"command"`
}

// FormatTranscript reads the synthesized JSONL (one opencode message per line)
// and renders it in the same "## User" / "## Assistant" format as the Claude
// and Codex backends.
func (o *Opencode) FormatTranscript(jsonlPath string) (string, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg opencodeMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		// Some schemas nest the message under "info" or "message".
		if msg.Role == "" && len(msg.Info) > 0 {
			var inner opencodeMessage
			if err := json.Unmarshal(msg.Info, &inner); err == nil {
				msg = inner
			}
		}
		if msg.Role == "" && len(msg.Message) > 0 {
			var inner opencodeMessage
			if err := json.Unmarshal(msg.Message, &inner); err == nil {
				msg = inner
			}
		}
		renderOpencodeMessage(&sb, msg)
	}

	return sb.String(), scanner.Err()
}

func renderOpencodeMessage(sb *strings.Builder, msg opencodeMessage) {
	role := msg.Role
	var textParts []string
	var toolCalls []string

	for _, p := range msg.Parts {
		switch p.Type {
		case "", "text", "output_text", "input_text":
			if strings.TrimSpace(p.Text) != "" {
				textParts = append(textParts, p.Text)
			}
		case "reasoning", "thinking":
			// skip internal reasoning
		case "tool", "tool_use", "tool-invocation":
			name := p.Name
			if name == "" {
				name = p.Tool
			}
			if name == "" && p.Command != "" {
				name = "shell"
			}
			if name != "" {
				toolCalls = append(toolCalls, name)
			}
		case "tool_result", "tool-result":
			// skip raw tool outputs — noisy and already implied by tool_use
		}
	}

	// Fallback: msg.Content may be a plain string.
	if len(textParts) == 0 && len(msg.Content) > 0 {
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil && strings.TrimSpace(s) != "" {
			textParts = append(textParts, s)
		} else {
			// Or an array of parts.
			var parts []opencodeMessagePart
			if err := json.Unmarshal(msg.Content, &parts); err == nil {
				for _, p := range parts {
					if p.Type == "" || p.Type == "text" || p.Type == "output_text" || p.Type == "input_text" {
						if strings.TrimSpace(p.Text) != "" {
							textParts = append(textParts, p.Text)
						}
					}
				}
			}
		}
	}
	if len(textParts) == 0 && msg.Text != "" {
		textParts = append(textParts, msg.Text)
	}

	if len(textParts) == 0 && len(toolCalls) == 0 {
		return
	}

	sb.WriteString("## ")
	sb.WriteString(opencodeRoleLabel(role))
	sb.WriteString("\n\n")
	if len(textParts) > 0 {
		sb.WriteString(strings.Join(textParts, "\n\n"))
		sb.WriteString("\n\n")
	}
	for _, name := range toolCalls {
		sb.WriteString("[Tool: ")
		sb.WriteString(name)
		sb.WriteString("]\n\n")
	}
}

func opencodeRoleLabel(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "system":
		return "System"
	case "":
		return "Assistant"
	default:
		return strings.Title(role) //nolint:staticcheck
	}
}

func init() {
	Register(&Opencode{})
}
