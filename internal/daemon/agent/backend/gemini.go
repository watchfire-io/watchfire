package backend

// Gemini CLI backend notes (research April 2026)
//
// Upstream: https://github.com/google-gemini/gemini-cli (docs at
// https://geminicli.com and https://google-gemini.github.io/gemini-cli).
// Executable: "gemini". Shipped via:
//   - npm `@google/gemini-cli` (global install lands on $PATH)
//   - Homebrew `gemini-cli` → /opt/homebrew/bin/gemini or /usr/local/bin/gemini
//   - MacPorts `gemini-cli`
//
// Yolo / bypass-approvals: the CLI exposes `--yolo` / `-y` which
// auto-approves every tool call. This is Gemini's direct analogue of
// Claude's `--dangerously-skip-permissions` and Codex's
// `--dangerously-bypass-approvals-and-sandbox`. Watchfire's outer
// sandbox-exec is the real security boundary; the agent-internal
// confirmations are redundant inside the PTY.
//
// Initial prompt delivery: headless mode takes the prompt via
// `--prompt` / `-p`. Without `-p` the CLI opens an interactive REPL,
// which is what Watchfire wants for chat mode. With `-p`, gemini runs
// the request (including multi-turn tool use) and exits — matching how
// task-mode sessions are expected to behave (agent writes `status: done`
// and exits).
//
// System prompt delivery: gemini-cli has no `--system-prompt` flag.
// The cleanest override is the `GEMINI_SYSTEM_MD=<path>` env var, which
// replaces the built-in system prompt with the contents of the named
// file (tilde expansion is supported; `true` / `1` points at
// `./.gemini/system.md`). Watchfire points it at a per-session file
// under `~/.watchfire/gemini-home/<session>/system.md` so each session
// gets its own Watchfire prompt without touching the user's global
// `~/.gemini/` or the worktree. Hierarchical GEMINI.md context files
// (e.g. user-level `~/.gemini/GEMINI.md` or repo-root `GEMINI.md`) still
// layer on top — GEMINI_SYSTEM_MD only replaces the system prompt, not
// the context-file discovery.
//
// Auth: gemini-cli stores credentials in the shared `~/.gemini/` dir
// (`oauth_creds.json`, `settings.json`, MCP tokens, etc). Because we
// don't override the config directory (no `GEMINI_CONFIG_DIR` exists on
// current versions), we don't symlink anything — the session just reads
// and writes `~/.gemini/` directly. The sandbox permits this subtree.
//
// Session storage: gemini-cli writes chat sessions under
// `~/.gemini/tmp/<project_hash>/chats/session-*.jsonl` (new format) or
// `~/.gemini/tmp/<project_hash>/logs.json` (legacy monolithic JSON array).
// Because this tree is shared across every gemini invocation, Watchfire
// discovers the right transcript by picking the newest matching file
// whose mtime is at or after the session start time. The JSONL schema
// is evolving and not formally documented; the formatter is
// intentionally forgiving.

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

// GeminiBackendName is the registry key for the Gemini CLI backend.
const GeminiBackendName = "gemini"

// Gemini implements Backend for the Gemini CLI.
type Gemini struct{}

// Name returns the stable registry identifier.
func (g *Gemini) Name() string { return GeminiBackendName }

// DisplayName returns the human-readable label.
func (g *Gemini) DisplayName() string { return "Gemini CLI" }

// ResolveExecutable locates the `gemini` binary: settings map → PATH →
// well-known install locations.
func (g *Gemini) ResolveExecutable(s *models.Settings) (string, error) {
	if s != nil && s.Agents != nil {
		if cfg, ok := s.Agents[GeminiBackendName]; ok && cfg != nil && cfg.Path != "" {
			if _, err := os.Stat(cfg.Path); err == nil {
				return cfg.Path, nil
			}
		}
	}

	if path, err := exec.LookPath("gemini"); err == nil {
		return path, nil
	}

	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		filepath.Join(homeDir, ".local", "bin", "gemini"),
		filepath.Join(homeDir, ".npm-global", "bin", "gemini"),
	}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/gemini",
			"/usr/local/bin/gemini",
		)
	} else {
		fallbacks = append(fallbacks, "/usr/local/bin/gemini")
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("gemini binary not found. Install Gemini CLI (https://github.com/google-gemini/gemini-cli) or set path in ~/.watchfire/settings.yaml")
}

// geminiSessionHomeForName returns the per-session dir (containing the
// session's system.md) derived deterministically from the session name
// so InstallSystemPrompt and BuildCommand always agree.
func geminiSessionHomeForName(sessionName string) (string, error) {
	if sessionName == "" {
		return "", fmt.Errorf("gemini: session name required")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".watchfire", "gemini-home", sanitizeSessionName(sessionName)), nil
}

func geminiSystemPromptPath(sessionName string) (string, error) {
	home, err := geminiSessionHomeForName(sessionName)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "system.md"), nil
}

// BuildCommand produces the Gemini CLI PTY invocation. --yolo enables
// auto-approval; when an initial prompt is present we pass it via
// `--prompt` for headless single-shot execution, otherwise the CLI
// starts its interactive REPL for chat mode. GEMINI_SYSTEM_MD points
// at the per-session system prompt written by InstallSystemPrompt.
func (g *Gemini) BuildCommand(opts CommandOpts) (Command, error) {
	sysPromptPath, err := geminiSystemPromptPath(opts.SessionName)
	if err != nil {
		return Command{}, err
	}

	args := []string{"--yolo"}
	if opts.InitialPrompt != "" {
		args = append(args, "--prompt", opts.InitialPrompt)
	}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}

	env := append(os.Environ(), "GEMINI_SYSTEM_MD="+sysPromptPath)

	return Command{
		Args:               args,
		Env:                env,
		PasteInitialPrompt: false,
	}, nil
}

// SandboxExtras returns the Gemini-specific paths the sandbox policy
// must allow. The per-session home under ~/.watchfire/gemini-home is
// writable; ~/.gemini/ is the shared global config/auth/session dir
// (we need reads for auth, writes for session state under tmp/).
func (g *Gemini) SandboxExtras() SandboxExtras {
	return SandboxExtras{
		WritableSubpaths: []string{"~/.watchfire/gemini-home"},
		WritableLiterals: []string{},
		CachePatterns:    []string{"~/.gemini", "~/.config/gcloud"},
	}
}

// InstallSystemPrompt materialises a per-session directory and writes
// the composed Watchfire system prompt as system.md. BuildCommand then
// points GEMINI_SYSTEM_MD at that file so gemini uses it in place of
// the built-in system prompt.
//
// workDir is kept to satisfy the Backend interface; the manager calls
// InstallSystemPromptForSession directly.
func (g *Gemini) InstallSystemPrompt(workDir, composedPrompt string) error {
	sessionName := filepath.Base(workDir)
	return g.installForSession(sessionName, composedPrompt)
}

// InstallSystemPromptForSession is the explicit variant the manager
// uses when it knows the session name directly.
func (g *Gemini) InstallSystemPromptForSession(sessionName, composedPrompt string) error {
	return g.installForSession(sessionName, composedPrompt)
}

func (g *Gemini) installForSession(sessionName, composedPrompt string) error {
	sessionHome, err := geminiSessionHomeForName(sessionName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sessionHome, 0o755); err != nil {
		return fmt.Errorf("create gemini session home: %w", err)
	}
	sysPromptPath := filepath.Join(sessionHome, "system.md")
	if err := os.WriteFile(sysPromptPath, []byte(composedPrompt), 0o644); err != nil {
		return fmt.Errorf("write system.md: %w", err)
	}
	return nil
}

// LocateTranscript finds the most recent gemini chat log produced after
// the session started. gemini writes under a project-hashed path
// (~/.gemini/tmp/<hash>/chats/session-*.jsonl for new builds, or
// <hash>/logs.json for older ones). Because we don't own that tree,
// we pick the newest file with mtime >= started across both schemas.
func (g *Gemini) LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	tmpRoot := filepath.Join(homeDir, ".gemini", "tmp")
	if _, err := os.Stat(tmpRoot); err != nil {
		return "", fmt.Errorf("gemini transcript root not found: %w", err)
	}

	// Allow small clock skew between the stored mtime and our captured
	// started time — some filesystems round mtimes to the nearest second.
	threshold := started.Add(-2 * time.Second)

	var candidates []string
	_ = filepath.Walk(tmpRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		parent := filepath.Base(filepath.Dir(path))
		isSession := parent == "chats" && (strings.HasSuffix(base, ".jsonl") || strings.HasSuffix(base, ".json"))
		isLegacyLogs := base == "logs.json"
		if !isSession && !isLegacyLogs {
			return nil
		}
		if info.ModTime().Before(threshold) {
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})

	if len(candidates) == 0 {
		return "", fmt.Errorf("no gemini transcript found under %s for session started at %v", tmpRoot, started)
	}

	sort.Slice(candidates, func(i, j int) bool {
		fi, erri := os.Stat(candidates[i])
		fj, errj := os.Stat(candidates[j])
		if erri != nil || errj != nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return candidates[0], nil
}

// Gemini transcript schema (best-effort — the JSONL format is evolving
// and not formally documented). We render user/model messages, tool
// calls (functionCall parts), and skip reasoning-style "thought" parts
// and functionResponse payloads.
type geminiMessage struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Parts   []geminiPart    `json:"parts"`
	// Some schemas wrap the payload under "message".
	Message json.RawMessage `json:"message"`
	// Older exports sometimes emit a parallel toolCalls array.
	ToolCalls []geminiToolCall `json:"toolCalls"`
}

type geminiPart struct {
	Text             string              `json:"text"`
	Thought          bool                `json:"thought"`
	FunctionCall     *geminiFunctionCall `json:"functionCall"`
	FunctionResponse *geminiFunctionResp `json:"functionResponse"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFunctionResp struct {
	Name string `json:"name"`
}

type geminiToolCall struct {
	Name string `json:"name"`
}

// FormatTranscript reads a gemini transcript (JSONL session log or
// legacy JSON-array logs.json) and renders it in the same
// "## User" / "## Assistant" shape the other backends produce so the
// log viewer renders every backend identically.
func (g *Gemini) FormatTranscript(jsonlPath string) (string, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	br := bufio.NewReader(f)
	// Skip leading whitespace and detect array-vs-JSONL by the first
	// non-whitespace byte.
	var prefix byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", nil
		}
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		prefix = b
		if err := br.UnreadByte(); err != nil {
			return "", err
		}
		break
	}

	var sb strings.Builder
	if prefix == '[' {
		var msgs []geminiMessage
		if err := json.NewDecoder(br).Decode(&msgs); err != nil {
			return "", err
		}
		for _, m := range msgs {
			formatGeminiMessage(&sb, m)
		}
	} else {
		scanner := bufio.NewScanner(br)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var msg geminiMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if msg.Type == "session_metadata" {
				continue
			}
			// Some schemas nest the payload under "message".
			if msg.Role == "" && len(msg.Parts) == 0 && len(msg.Content) == 0 && len(msg.Message) > 0 {
				var inner geminiMessage
				if err := json.Unmarshal(msg.Message, &inner); err == nil {
					msg = inner
				}
			}
			formatGeminiMessage(&sb, msg)
		}
		if err := scanner.Err(); err != nil {
			return sb.String(), err
		}
	}
	return sb.String(), nil
}

func formatGeminiMessage(sb *strings.Builder, msg geminiMessage) {
	role := msg.Role
	if role == "" {
		// JSONL "type" discriminator sometimes carries the role.
		switch msg.Type {
		case "user", "gemini", "assistant", "model", "system":
			role = msg.Type
		}
	}

	var textParts []string
	var toolCalls []string

	for _, p := range msg.Parts {
		if p.Thought {
			continue
		}
		if p.FunctionCall != nil && p.FunctionCall.Name != "" {
			toolCalls = append(toolCalls, p.FunctionCall.Name)
			continue
		}
		if p.FunctionResponse != nil {
			// Skip raw tool outputs — noisy and already implied by the call.
			continue
		}
		if strings.TrimSpace(p.Text) != "" {
			textParts = append(textParts, p.Text)
		}
	}

	if len(textParts) == 0 && len(msg.Content) > 0 {
		// Content may be a plain string, an array of parts, or an object
		// with a "text" field.
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil && strings.TrimSpace(s) != "" {
			textParts = append(textParts, s)
		} else {
			var parts []geminiPart
			if err := json.Unmarshal(msg.Content, &parts); err == nil && len(parts) > 0 {
				for _, p := range parts {
					if p.Thought {
						continue
					}
					if p.FunctionCall != nil && p.FunctionCall.Name != "" {
						toolCalls = append(toolCalls, p.FunctionCall.Name)
						continue
					}
					if strings.TrimSpace(p.Text) != "" {
						textParts = append(textParts, p.Text)
					}
				}
			} else {
				var obj struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(msg.Content, &obj); err == nil && strings.TrimSpace(obj.Text) != "" {
					textParts = append(textParts, obj.Text)
				}
			}
		}
	}

	if len(textParts) == 0 && strings.TrimSpace(msg.Text) != "" {
		textParts = append(textParts, msg.Text)
	}

	for _, tc := range msg.ToolCalls {
		if tc.Name != "" {
			toolCalls = append(toolCalls, tc.Name)
		}
	}

	if len(textParts) == 0 && len(toolCalls) == 0 {
		return
	}

	sb.WriteString("## ")
	sb.WriteString(geminiRoleLabel(role))
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

func geminiRoleLabel(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant", "model", "gemini":
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
	Register(&Gemini{})
}
