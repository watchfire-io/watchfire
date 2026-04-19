package backend

// GitHub Copilot CLI backend notes (research April 2026)
//
// Upstream: https://github.com/github/copilot-cli
// Executable: "copilot". Shipped via:
//   - Homebrew: /opt/homebrew/bin/copilot, /usr/local/bin/copilot
//   - npm `@github/copilot` (global install lands on $PATH)
//   - install script: `curl -fsSL https://gh.io/copilot-install | bash`
//
// Yolo / bypass-approvals: the CLI exposes `--allow-all` (alias
// `--yolo`), equivalent to `--allow-all-tools --allow-all-paths
// --allow-all-urls`. Required for programmatic invocation — matches
// Claude's `--dangerously-skip-permissions` and Codex's
// `--dangerously-bypass-approvals-and-sandbox`. Watchfire's outer
// sandbox-exec is the real security boundary; the agent-internal
// confirmations are redundant inside the PTY.
//
// Initial prompt delivery: headless mode takes the prompt via
// `-p <prompt>` / `--prompt=<prompt>`. Without `-p` the CLI opens an
// interactive REPL, which is what Watchfire wants for chat mode. With
// `-p`, copilot runs the request (including multi-turn tool use) and
// exits — matching how task-mode sessions are expected to behave (the
// agent writes `status: done` and exits).
//
// System prompt delivery: copilot discovers custom instructions via the
// `COPILOT_CUSTOM_INSTRUCTIONS_DIRS` env var plus repo-level
// `.github/copilot-instructions.md` and root `AGENTS.md`. Watchfire
// writes the composed Watchfire prompt as `AGENTS.md` into a per-session
// `COPILOT_HOME` directory and points `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`
// at it so the prompt is always loaded without mutating the worktree.
// The user's own project-level `.github/copilot-instructions.md` or
// repo-root `AGENTS.md` (if any) still layers on top via normal
// discovery.
//
// Auth: copilot stores credentials under `~/.copilot/config.json`
// (GitHub OAuth via `/login`), with MCP settings in
// `~/.copilot/mcp-config.json` and session history in
// `~/.copilot/session-store.db`. Because we override the config dir via
// `COPILOT_HOME`, we symlink those three entries into the per-session
// dir so existing login / MCP / history continue to work. First-run
// users who haven't created them yet are handled gracefully — missing
// targets are skipped silently.
//
// Session storage: copilot writes session state under
// `$COPILOT_HOME/session-state/<session-id>/` with `events.jsonl`
// (streaming event log), `workspace.yaml`, and a `checkpoints/`
// directory. Because Watchfire owns `COPILOT_HOME` per session, exactly
// one session directory exists per Watchfire session; `LocateTranscript`
// walks it and picks the newest `events.jsonl` by mtime. The JSONL
// schema is not formally documented; the formatter is intentionally
// forgiving (skip-and-continue on any line that doesn't fit the
// best-effort shape).

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

// CopilotBackendName is the registry key for the GitHub Copilot CLI backend.
const CopilotBackendName = "copilot"

// Copilot implements Backend for the GitHub Copilot CLI.
type Copilot struct{}

// Name returns the stable registry identifier.
func (c *Copilot) Name() string { return CopilotBackendName }

// DisplayName returns the human-readable label.
func (c *Copilot) DisplayName() string { return "GitHub Copilot CLI" }

// ResolveExecutable locates the `copilot` binary: settings map → PATH →
// well-known install locations.
func (c *Copilot) ResolveExecutable(s *models.Settings) (string, error) {
	if s != nil && s.Agents != nil {
		if cfg, ok := s.Agents[CopilotBackendName]; ok && cfg != nil && cfg.Path != "" {
			if _, err := os.Stat(cfg.Path); err == nil {
				return cfg.Path, nil
			}
		}
	}

	if path, err := exec.LookPath("copilot"); err == nil {
		return path, nil
	}

	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		filepath.Join(homeDir, ".local", "bin", "copilot"),
		filepath.Join(homeDir, ".npm-global", "bin", "copilot"),
	}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/copilot",
			"/usr/local/bin/copilot",
		)
	} else {
		fallbacks = append(fallbacks,
			"/usr/local/bin/copilot",
			"/usr/bin/copilot",
		)
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("copilot binary not found. Install GitHub Copilot CLI (`brew install copilot-cli` or `npm install -g @github/copilot`) or set path in ~/.watchfire/settings.yaml")
}

// copilotSessionHomeForName returns the per-session COPILOT_HOME directory
// path derived deterministically from the session name. The same input yields
// the same path across InstallSystemPrompt, BuildCommand, and LocateTranscript.
func copilotSessionHomeForName(sessionName string) (string, error) {
	if sessionName == "" {
		return "", fmt.Errorf("copilot: session name required")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".watchfire", "copilot-home", sanitizeSessionName(sessionName)), nil
}

// BuildCommand produces the Copilot PTY invocation. `--allow-all` enables
// yolo mode; when an initial prompt is present we pass it via `-p` for
// headless single-shot execution, otherwise the CLI starts its interactive
// REPL for chat mode. COPILOT_HOME and COPILOT_CUSTOM_INSTRUCTIONS_DIRS
// point at the per-session home written by InstallSystemPrompt.
func (c *Copilot) BuildCommand(opts CommandOpts) (Command, error) {
	sessionHome, err := copilotSessionHomeForName(opts.SessionName)
	if err != nil {
		return Command{}, err
	}

	args := []string{"--allow-all"}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}
	if opts.InitialPrompt != "" {
		args = append(args, "-p", opts.InitialPrompt)
	}

	env := append(os.Environ(),
		"COPILOT_HOME="+sessionHome,
		"COPILOT_CUSTOM_INSTRUCTIONS_DIRS="+sessionHome,
	)

	return Command{
		Args:               args,
		Env:                env,
		PasteInitialPrompt: false,
	}, nil
}

// SandboxExtras returns the Copilot-specific paths the sandbox policy must
// allow. The per-session home under ~/.watchfire/copilot-home is writable;
// ~/.copilot covers (a) writes copilot performs through the symlinks we
// installed (session-store.db updates, mcp-config.json writes) and (b) any
// atomic-write temp files copilot creates alongside its config, matching how
// the Codex backend grants ~/.codex as a subpath rather than literal files.
func (c *Copilot) SandboxExtras() SandboxExtras {
	return SandboxExtras{
		WritableSubpaths: []string{
			"~/.watchfire/copilot-home",
			"~/.copilot",
		},
	}
}

// InstallSystemPrompt writes the composed Watchfire prompt as AGENTS.md in a
// per-session COPILOT_HOME dir and symlinks the user's ~/.copilot auth /
// mcp / session-store files into it so existing login, MCP, and history are
// reused. workDir is kept to satisfy the Backend interface; the manager
// calls InstallSystemPromptForSession directly.
func (c *Copilot) InstallSystemPrompt(workDir, composedPrompt string) error {
	sessionName := filepath.Base(workDir)
	return c.installForSession(sessionName, composedPrompt)
}

// InstallSystemPromptForSession is the explicit variant the manager uses when
// it knows the session name directly. Prefer this over InstallSystemPrompt.
func (c *Copilot) InstallSystemPromptForSession(sessionName, composedPrompt string) error {
	return c.installForSession(sessionName, composedPrompt)
}

func (c *Copilot) installForSession(sessionName, composedPrompt string) error {
	sessionHome, err := copilotSessionHomeForName(sessionName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sessionHome, 0o755); err != nil {
		return fmt.Errorf("create copilot session home: %w", err)
	}

	agentsPath := filepath.Join(sessionHome, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(composedPrompt), 0o644); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	userCopilot := filepath.Join(userHome, ".copilot")
	for _, name := range []string{"config.json", "mcp-config.json", "session-store.db"} {
		target := filepath.Join(userCopilot, name)
		link := filepath.Join(sessionHome, name)
		if _, err := os.Lstat(link); err == nil {
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("remove stale symlink %s: %w", link, err)
			}
		}
		if _, err := os.Stat(target); err != nil {
			// User may not have this file yet; skip silently rather than fail
			// the session. Copilot will create it on first login / use.
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
		}
	}
	return nil
}

// LocateTranscript finds the Copilot events.jsonl rollout for a session by
// walking the per-session COPILOT_HOME/session-state tree. In practice there
// is exactly one rollout file per session since we own the dir; if multiple
// exist, the newest by mtime wins.
func (c *Copilot) LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error) {
	if sessionHint == "" {
		return "", fmt.Errorf("sessionHint required for copilot transcript lookup")
	}
	sessionHome, err := copilotSessionHomeForName(sessionHint)
	if err != nil {
		return "", err
	}

	stateRoot := filepath.Join(sessionHome, "session-state")
	var matches []string
	_ = filepath.Walk(stateRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == "events.jsonl" {
			matches = append(matches, path)
		}
		return nil
	})

	if len(matches) == 0 {
		return "", fmt.Errorf("no copilot events.jsonl found under %s", stateRoot)
	}

	sort.Slice(matches, func(i, j int) bool {
		fi, erri := os.Stat(matches[i])
		fj, errj := os.Stat(matches[j])
		if erri != nil || errj != nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return matches[0], nil
}

// Copilot events.jsonl schema (best-effort — the schema is not formally
// documented and may evolve). One JSON object per line with a top-level
// "type" discriminator. Fields observed across messages and tool events
// are parsed opportunistically; unknown event types and unparseable lines
// are silently skipped.
type copilotEvent struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Tool    string          `json:"tool"`
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload"`
	Data    json.RawMessage `json:"data"`
}

type copilotItemMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	// Copilot also sometimes emits content as a plain string.
	RawText string `json:"text"`
}

type copilotItemToolUse struct {
	Name    string          `json:"name"`
	Tool    string          `json:"tool"`
	Command string          `json:"command"`
	Input   json.RawMessage `json:"input"`
	Args    json.RawMessage `json:"args"`
}

// FormatTranscript reads a Copilot events.jsonl log and renders it in the
// same "## User\n\n..." / "## Assistant\n\n..." style used by the other
// backends so the log viewer renders every backend identically. The
// formatter is intentionally forgiving: any line that doesn't fit the
// best-effort shape is skipped so schema evolution doesn't break rendering.
func (c *Copilot) FormatTranscript(jsonlPath string) (string, error) {
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
		var evt copilotEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "item.message", "message":
			formatCopilotMessage(&sb, line, evt)
		case "item.tool_use", "tool_use":
			formatCopilotToolUse(&sb, line, evt)
		case "item.reasoning", "reasoning":
			// skip internal reasoning
		case "error":
			if evt.Text != "" {
				sb.WriteString("## Error\n\n")
				sb.WriteString(evt.Text)
				sb.WriteString("\n\n")
			}
		}
	}

	return sb.String(), scanner.Err()
}

func formatCopilotMessage(sb *strings.Builder, line []byte, evt copilotEvent) {
	// Payload form: some Copilot builds nest the message under "payload" or
	// "data" (mirroring the Codex envelope). Try each in turn.
	var msg copilotItemMessage
	source := line
	if len(evt.Payload) > 0 {
		source = evt.Payload
	} else if len(evt.Data) > 0 {
		source = evt.Data
	}
	if err := json.Unmarshal(source, &msg); err != nil {
		return
	}
	role := msg.Role
	if role == "" {
		role = evt.Role
	}

	var text string
	if len(msg.Content) > 0 {
		var parts []string
		for _, blk := range msg.Content {
			if blk.Type == "" || blk.Type == "text" || blk.Type == "output_text" || blk.Type == "input_text" {
				if strings.TrimSpace(blk.Text) != "" {
					parts = append(parts, blk.Text)
				}
			}
		}
		text = strings.Join(parts, "\n\n")
	} else if msg.RawText != "" {
		text = msg.RawText
	} else if evt.Text != "" {
		text = evt.Text
	}

	if strings.TrimSpace(text) == "" {
		return
	}

	sb.WriteString("## ")
	sb.WriteString(copilotRoleLabel(role))
	sb.WriteString("\n\n")
	sb.WriteString(text)
	sb.WriteString("\n\n")
}

func formatCopilotToolUse(sb *strings.Builder, line []byte, evt copilotEvent) {
	var tu copilotItemToolUse
	source := line
	if len(evt.Payload) > 0 {
		source = evt.Payload
	} else if len(evt.Data) > 0 {
		source = evt.Data
	}
	_ = json.Unmarshal(source, &tu)

	name := tu.Name
	if name == "" {
		name = tu.Tool
	}
	if name == "" {
		name = evt.Name
	}
	if name == "" {
		name = evt.Tool
	}
	if name == "" && (tu.Command != "" || evt.Command != "") {
		name = "shell"
	}
	if name == "" {
		return
	}

	sb.WriteString("## Assistant\n\n[Tool: ")
	sb.WriteString(name)
	sb.WriteString("]\n\n")
}

func copilotRoleLabel(role string) string {
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
	Register(&Copilot{})
}
