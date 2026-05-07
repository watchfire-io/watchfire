package backend

// Cursor Agent CLI backend notes (research May 2026)
//
// Upstream: https://cursor.com/docs/cli (Cursor's headless agent CLI,
// distinct from the Cursor editor app). The binary is "cursor-agent",
// NOT "cursor" — the latter is the editor's CLI shim shipped by the
// .app bundle. Shipped via:
//   - install script: `curl https://cursor.com/install -fsS | bash`
//   - Homebrew: /opt/homebrew/bin/cursor-agent, /usr/local/bin/cursor-agent
//   - manual extract under ~/.local/bin or ~/.cursor/bin
//
// Yolo / bypass-approvals: the CLI exposes `--force`, which auto-applies
// every file edit and tool action without prompting. This is Cursor's
// direct analogue of Claude's `--dangerously-skip-permissions`, Codex's
// `--dangerously-bypass-approvals-and-sandbox`, Gemini's `--yolo`, and
// Copilot's `--allow-all`. Watchfire's outer sandbox-exec is the real
// security boundary; the agent-internal confirmations are redundant
// inside the PTY.
//
// Initial prompt delivery: headless mode is `-p` / `--print`, with the
// prompt passed as the trailing positional argument:
//
//   cursor-agent -p --force "do the thing"
//
// Without `-p` the CLI starts its interactive REPL, which is what
// Watchfire wants for chat mode. With `-p`, cursor-agent runs the
// request (including multi-turn tool use) and exits — matching how
// task-mode sessions are expected to behave (the agent writes
// `status: done` and exits).
//
// System prompt delivery: Cursor's documented instruction sources are
// (in order of precedence) Team Rules → `.cursor/rules/` →
// `AGENTS.md` → User Rules. Watchfire writes the composed Watchfire
// prompt as `AGENTS.md` into a per-session config dir under
// `~/.watchfire/cursor-home/<session>/` and points `CURSOR_HOME` at it
// so the prompt is loaded without mutating the worktree. The user's
// own project-level `.cursor/rules/` or repo-root `AGENTS.md` (if any)
// still layers on top via normal discovery. CURSOR_HOME is the
// best-guess override variable (analogous to COPILOT_HOME / CODEX_HOME);
// upstream documentation does not formally name a config-dir override,
// so future Cursor releases may require a different env var name. If
// the env var is silently ignored the AGENTS.md just sits unused — the
// session still runs with the user's existing global rules.
//
// Auth: Cursor stores config + credentials under `~/.cursor/`
// (`cli-config.json` for the auth blob, `mcp.json` for MCP servers,
// `permissions.json` for pre-approved MCP tools). Because we override
// the config dir via `CURSOR_HOME`, we symlink those three entries
// into the per-session dir so existing login / MCP / permissions
// continue to work. First-run users who haven't created them yet are
// handled gracefully — missing targets are skipped silently. The
// `CURSOR_API_KEY` env var (if exported by the user) provides a
// secondary auth path that survives the CURSOR_HOME override.
//
// Workspace: Cursor doesn't expose a documented `--workspace` flag;
// it operates on the process working directory. The agent manager
// already sets `cmd.Dir = workDir` (the Watchfire worktree under
// `.watchfire/worktrees/<n>`), so cursor-agent runs inside the
// worktree without needing extra plumbing. This intentionally avoids
// Cursor's own `--worktree`-style layouts (per GH #34): Watchfire
// owns the worktree lifecycle, and mixing the two abstractions would
// race over branch state.
//
// Session storage: Cursor's CLI does not formally document its
// rollout-file layout, but session state is observed under the config
// dir's `session-state/<session-id>/events.jsonl` (mirrors the Codex /
// Copilot shape). Because Watchfire owns `CURSOR_HOME` per session,
// exactly one session directory exists per Watchfire session;
// `LocateTranscript` walks it and picks the newest `events.jsonl` by
// mtime. The schema is best-effort: unknown event types and
// unparseable lines are silently skipped.

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

// CursorBackendName is the registry key for the Cursor Agent CLI backend.
const CursorBackendName = "cursor"

// Cursor implements Backend for the Cursor Agent CLI.
type Cursor struct{}

// Name returns the stable registry identifier.
func (c *Cursor) Name() string { return CursorBackendName }

// DisplayName returns the human-readable label.
func (c *Cursor) DisplayName() string { return "Cursor Agent CLI" }

// ResolveExecutable locates the `cursor-agent` binary: settings map → PATH →
// well-known install locations.
func (c *Cursor) ResolveExecutable(s *models.Settings) (string, error) {
	if s != nil && s.Agents != nil {
		if cfg, ok := s.Agents[CursorBackendName]; ok && cfg != nil && cfg.Path != "" {
			if _, err := os.Stat(cfg.Path); err == nil {
				return cfg.Path, nil
			}
		}
	}

	if path, err := exec.LookPath("cursor-agent"); err == nil {
		return path, nil
	}

	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		filepath.Join(homeDir, ".local", "bin", "cursor-agent"),
		filepath.Join(homeDir, ".cursor", "bin", "cursor-agent"),
	}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/cursor-agent",
			"/usr/local/bin/cursor-agent",
		)
	} else {
		fallbacks = append(fallbacks,
			"/usr/local/bin/cursor-agent",
			"/usr/bin/cursor-agent",
		)
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("cursor-agent binary not found. Install Cursor Agent CLI (`curl https://cursor.com/install -fsS | bash` or `brew install cursor-agent`) or set path in ~/.watchfire/settings.yaml")
}

// cursorSessionHomeForName returns the per-session CURSOR_HOME directory
// path derived deterministically from the session name. The same input yields
// the same path across InstallSystemPrompt, BuildCommand, and LocateTranscript.
func cursorSessionHomeForName(sessionName string) (string, error) {
	if sessionName == "" {
		return "", fmt.Errorf("cursor: session name required")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".watchfire", "cursor-home", sanitizeSessionName(sessionName)), nil
}

// BuildCommand produces the Cursor PTY invocation. `--force` enables yolo
// mode; when an initial prompt is present we pass `-p` plus the prompt as
// the trailing positional argument for headless single-shot execution,
// otherwise the CLI starts its interactive REPL for chat mode. CURSOR_HOME
// points at the per-session home written by InstallSystemPrompt so the
// composed Watchfire prompt (as AGENTS.md) and the symlinked auth files
// are picked up.
func (c *Cursor) BuildCommand(opts CommandOpts) (Command, error) {
	sessionHome, err := cursorSessionHomeForName(opts.SessionName)
	if err != nil {
		return Command{}, err
	}

	args := []string{"--force"}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}
	if opts.InitialPrompt != "" {
		args = append(args, "-p", opts.InitialPrompt)
	}

	env := append(os.Environ(),
		"CURSOR_HOME="+sessionHome,
	)

	return Command{
		Args:               args,
		Env:                env,
		PasteInitialPrompt: false,
	}, nil
}

// SandboxExtras returns the Cursor-specific paths the sandbox policy must
// allow. The per-session home under ~/.watchfire/cursor-home is writable;
// ~/.cursor covers (a) writes cursor-agent performs through the symlinks
// we installed (cli-config.json updates, mcp.json writes) and (b) any
// atomic-write temp files cursor-agent creates alongside its config,
// matching how the Codex backend grants ~/.codex as a subpath rather than
// literal files.
func (c *Cursor) SandboxExtras() SandboxExtras {
	return SandboxExtras{
		WritableSubpaths: []string{
			"~/.watchfire/cursor-home",
			"~/.cursor",
		},
	}
}

// InstallSystemPrompt writes the composed Watchfire prompt as AGENTS.md in a
// per-session CURSOR_HOME dir and symlinks the user's ~/.cursor auth /
// MCP / permissions files into it so existing login, MCP servers, and
// pre-approved tools are reused. workDir is kept to satisfy the Backend
// interface; the manager calls InstallSystemPromptForSession directly.
func (c *Cursor) InstallSystemPrompt(workDir, composedPrompt string) error {
	sessionName := filepath.Base(workDir)
	return c.installForSession(sessionName, composedPrompt)
}

// InstallSystemPromptForSession is the explicit variant the manager uses when
// it knows the session name directly. Prefer this over InstallSystemPrompt.
func (c *Cursor) InstallSystemPromptForSession(sessionName, composedPrompt string) error {
	return c.installForSession(sessionName, composedPrompt)
}

func (c *Cursor) installForSession(sessionName, composedPrompt string) error {
	sessionHome, err := cursorSessionHomeForName(sessionName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sessionHome, 0o755); err != nil {
		return fmt.Errorf("create cursor session home: %w", err)
	}

	agentsPath := filepath.Join(sessionHome, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(composedPrompt), 0o644); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	userCursor := filepath.Join(userHome, ".cursor")
	for _, name := range []string{"cli-config.json", "mcp.json", "permissions.json"} {
		target := filepath.Join(userCursor, name)
		link := filepath.Join(sessionHome, name)
		if _, err := os.Lstat(link); err == nil {
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("remove stale symlink %s: %w", link, err)
			}
		}
		if _, err := os.Stat(target); err != nil {
			// User may not have this file yet; skip silently rather than fail
			// the session. cursor-agent will create it on first login / use.
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
		}
	}
	return nil
}

// LocateTranscript finds the Cursor events.jsonl rollout for a session by
// walking the per-session CURSOR_HOME/session-state tree. In practice there
// is exactly one rollout file per session since we own the dir; if multiple
// exist, the newest by mtime wins.
func (c *Cursor) LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error) {
	if sessionHint == "" {
		return "", fmt.Errorf("sessionHint required for cursor transcript lookup")
	}
	sessionHome, err := cursorSessionHomeForName(sessionHint)
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
		return "", fmt.Errorf("no cursor events.jsonl found under %s", stateRoot)
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

// Cursor events.jsonl schema (best-effort — the schema is not formally
// documented and may evolve). One JSON object per line with a top-level
// "type" discriminator. Fields observed across messages and tool events
// are parsed opportunistically; unknown event types and unparseable lines
// are silently skipped.
type cursorEvent struct {
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

type cursorItemMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	// Cursor also sometimes emits content as a plain string.
	RawText string `json:"text"`
}

type cursorItemToolUse struct {
	Name    string          `json:"name"`
	Tool    string          `json:"tool"`
	Command string          `json:"command"`
	Input   json.RawMessage `json:"input"`
	Args    json.RawMessage `json:"args"`
}

// FormatTranscript reads a Cursor events.jsonl log and renders it in the
// same "## User\n\n..." / "## Assistant\n\n..." style used by the other
// backends so the log viewer renders every backend identically. The
// formatter is intentionally forgiving: any line that doesn't fit the
// best-effort shape is skipped so schema evolution doesn't break rendering.
func (c *Cursor) FormatTranscript(jsonlPath string) (string, error) {
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
		var evt cursorEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "item.message", "message":
			formatCursorMessage(&sb, line, evt)
		case "item.tool_use", "tool_use":
			formatCursorToolUse(&sb, line, evt)
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

func formatCursorMessage(sb *strings.Builder, line []byte, evt cursorEvent) {
	var msg cursorItemMessage
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
	sb.WriteString(cursorRoleLabel(role))
	sb.WriteString("\n\n")
	sb.WriteString(text)
	sb.WriteString("\n\n")
}

func formatCursorToolUse(sb *strings.Builder, line []byte, evt cursorEvent) {
	var tu cursorItemToolUse
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

func cursorRoleLabel(role string) string {
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
	Register(&Cursor{})
}
