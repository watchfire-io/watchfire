package backend

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

// CodexBackendName is the registry key for the OpenAI Codex backend.
const CodexBackendName = "codex"

// codexCommitAddendum is appended to the AGENTS.md every Codex session reads.
// Codex has been observed marking a task `status: done` without running
// `git commit`, which causes Watchfire to see no diff on the branch and
// silently discard the edits when the worktree gets cleaned up. The base
// Watchfire prompt already says "Commit all changes," but Codex doesn't
// consistently follow that; this louder, codex-specific addendum repeats
// the rule at the end where it's least likely to be overlooked. The
// MergeWorktree auto-commit safety net in worktree.go is the belt; this
// is the suspenders.
const codexCommitAddendum = `

---

# CRITICAL: Commit before marking a task done

Before you set ` + "`status: done`" + ` in a task YAML, you MUST run:

` + "```" + `
git add -A
git commit -m "<short description of what you did>"
` + "```" + `

from inside the worktree. If you skip this, Watchfire will try to merge your
branch, find no new commits, and your file edits will be DISCARDED. This has
actually happened and lost work. Commit first, mark done second. No exceptions.
`

// Codex implements Backend for the OpenAI Codex CLI.
type Codex struct{}

// Name returns the stable registry identifier.
func (c *Codex) Name() string { return CodexBackendName }

// DisplayName returns the human-readable label.
func (c *Codex) DisplayName() string { return "OpenAI Codex" }

// ResolveExecutable locates the `codex` binary: settings map → PATH → fallbacks.
func (c *Codex) ResolveExecutable(s *models.Settings) (string, error) {
	if s != nil && s.Agents != nil {
		if cfg, ok := s.Agents[CodexBackendName]; ok && cfg != nil && cfg.Path != "" {
			if _, err := os.Stat(cfg.Path); err == nil {
				return cfg.Path, nil
			}
		}
	}

	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}

	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		filepath.Join(homeDir, ".local", "bin", "codex"),
		filepath.Join(homeDir, ".npm-global", "bin", "codex"),
	}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/codex",
			"/usr/local/bin/codex",
		)
	} else {
		// Linux distro packages (Fedora dnf, Debian apt) land in /usr/bin;
		// manual/universal installers land in /usr/local/bin. Fixes #29
		// where a Fedora install was invisible to the picker.
		fallbacks = append(fallbacks,
			"/usr/local/bin/codex",
			"/usr/bin/codex",
		)
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("codex binary not found. Install Codex or set path in ~/.watchfire/settings.yaml")
}

// codexSessionHomeForName returns the per-session CODEX_HOME directory path
// derived deterministically from the session name. The same input yields the
// same path across InstallSystemPrompt, BuildCommand, and LocateTranscript.
func codexSessionHomeForName(sessionName string) (string, error) {
	if sessionName == "" {
		return "", fmt.Errorf("codex: session name required")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".watchfire", "codex-home", sanitizeSessionName(sessionName)), nil
}

// sanitizeSessionName replaces characters that are awkward in directory names
// with '_'. Session names like "proj:task:#0001-foo" stay readable.
func sanitizeSessionName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		" ", "_",
	)
	return replacer.Replace(name)
}

// BuildCommand produces the Codex PTY invocation.
func (c *Codex) BuildCommand(opts CommandOpts) (Command, error) {
	sessionHome, err := codexSessionHomeForName(opts.SessionName)
	if err != nil {
		return Command{}, err
	}

	args := []string{"--dangerously-bypass-approvals-and-sandbox"}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}
	if opts.InitialPrompt != "" {
		args = append(args, opts.InitialPrompt)
	}

	env := append(os.Environ(), "CODEX_HOME="+sessionHome)

	return Command{
		Args:               args,
		Env:                env,
		PasteInitialPrompt: false,
	}, nil
}

// SandboxExtras returns the Codex-specific paths the sandbox policy must allow.
//
// Codex persists config atomically (temp file + rename) and writes to several
// sibling files under ~/.codex (history.jsonl, logs_*.sqlite, sessions/, ...),
// so literal allowlists for auth.json/config.toml aren't enough — the temp
// files fall outside the literal rule. Granting subpath write on ~/.codex
// matches the Claude backend's ~/.claude approach and lets atomic writes and
// Codex's own metadata persistence work under the sandbox.
func (c *Codex) SandboxExtras() SandboxExtras {
	return SandboxExtras{
		WritableSubpaths: []string{"~/.watchfire/codex-home", "~/.codex"},
	}
}

// InstallSystemPrompt writes the composed Watchfire prompt as AGENTS.md in a
// per-session CODEX_HOME dir and symlinks the user's ~/.codex/auth.json and
// ~/.codex/config.toml into it so the existing login is reused. The dir is
// later passed to the Codex process via the CODEX_HOME env var.
//
// workDir derives the session identity via its basename matching the session
// name convention; however we key off opts.SessionName at BuildCommand time,
// so InstallSystemPrompt here uses the workDir's parent-derived session
// (caller must ensure the session name used here matches the one passed to
// BuildCommand). To avoid that coupling, the manager calls InstallSystemPrompt
// with the same session name used for BuildCommand via the sessionName in
// workDir is not relied upon; instead callers should use
// InstallSystemPromptForSession. The workDir param is kept to satisfy the
// Backend interface.
func (c *Codex) InstallSystemPrompt(workDir, composedPrompt string) error {
	sessionName := filepath.Base(workDir)
	return c.installForSession(sessionName, composedPrompt)
}

// InstallSystemPromptForSession is the explicit variant the manager uses when
// it knows the session name directly. Prefer this over InstallSystemPrompt.
func (c *Codex) InstallSystemPromptForSession(sessionName, composedPrompt string) error {
	return c.installForSession(sessionName, composedPrompt)
}

func (c *Codex) installForSession(sessionName, composedPrompt string) error {
	sessionHome, err := codexSessionHomeForName(sessionName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sessionHome, 0o755); err != nil {
		return fmt.Errorf("create codex session home: %w", err)
	}

	agentsPath := filepath.Join(sessionHome, "AGENTS.md")
	agentsContent := composedPrompt + codexCommitAddendum
	if err := os.WriteFile(agentsPath, []byte(agentsContent), 0o644); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	userCodex := filepath.Join(userHome, ".codex")
	for _, name := range []string{"auth.json", "config.toml"} {
		target := filepath.Join(userCodex, name)
		link := filepath.Join(sessionHome, name)
		if _, err := os.Lstat(link); err == nil {
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("remove stale symlink %s: %w", link, err)
			}
		}
		if _, err := os.Stat(target); err != nil {
			// User may not have this file yet; skip silently rather than fail
			// the session. Codex will create it on first auth.
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
		}
	}
	return nil
}

// LocateTranscript finds the Codex JSONL rollout for a session by globbing the
// per-session CODEX_HOME/sessions tree. In practice there is exactly one
// rollout file per session since we own the dir; if multiple exist, the newest
// by mtime wins.
func (c *Codex) LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error) {
	if sessionHint == "" {
		return "", fmt.Errorf("sessionHint required for codex transcript lookup")
	}
	sessionHome, err := codexSessionHomeForName(sessionHint)
	if err != nil {
		return "", err
	}

	matches, err := filepath.Glob(filepath.Join(sessionHome, "sessions", "**", "rollout-*.jsonl"))
	if err != nil {
		return "", err
	}
	// Go's filepath.Glob doesn't expand ** as recursive; walk manually.
	if len(matches) == 0 {
		matches = nil
		_ = filepath.Walk(filepath.Join(sessionHome, "sessions"), func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			base := filepath.Base(path)
			if strings.HasPrefix(base, "rollout-") && strings.HasSuffix(base, ".jsonl") {
				matches = append(matches, path)
			}
			return nil
		})
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no codex rollout found under %s", sessionHome)
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

// Codex JSONL schema (best-effort — Codex rollouts use one JSON object per
// line with a top-level "type" discriminator). We render messages, reasoning
// text, and tool calls, skipping raw tool outputs which are noisy.
type codexEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	// Fields observed on several event types — parsed opportunistically.
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Tool    string          `json:"tool"`
	Command string          `json:"command"`
}

type codexItemMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	// Codex also sometimes emits content as a plain string.
	RawText string `json:"text"`
}

type codexItemToolUse struct {
	Name    string          `json:"name"`
	Tool    string          `json:"tool"`
	Command string          `json:"command"`
	Input   json.RawMessage `json:"input"`
	Args    json.RawMessage `json:"args"`
}

// FormatTranscript reads a Codex JSONL rollout and renders it into the same
// "## User\n\n..." / "## Assistant\n\n..." style used by the Claude backend so
// the log viewer renders both identically.
func (c *Codex) FormatTranscript(jsonlPath string) (string, error) {
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
		var evt codexEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "item.message", "message":
			formatCodexMessage(&sb, line, evt)
		case "item.tool_use", "tool_use":
			formatCodexToolUse(&sb, line, evt)
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

func formatCodexMessage(sb *strings.Builder, line []byte, evt codexEvent) {
	// Payload form: some Codex builds nest the message under "payload".
	var msg codexItemMessage
	source := line
	if len(evt.Payload) > 0 {
		source = evt.Payload
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
	sb.WriteString(codexRoleLabel(role))
	sb.WriteString("\n\n")
	sb.WriteString(text)
	sb.WriteString("\n\n")
}

func formatCodexToolUse(sb *strings.Builder, line []byte, evt codexEvent) {
	var tu codexItemToolUse
	source := line
	if len(evt.Payload) > 0 {
		source = evt.Payload
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

func codexRoleLabel(role string) string {
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
	Register(&Codex{})
}
