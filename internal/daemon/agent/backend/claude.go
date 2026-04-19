package backend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// ClaudeBackendName is the registry key for the Claude Code backend.
const ClaudeBackendName = "claude-code"

// Claude implements Backend for the Claude Code CLI.
type Claude struct{}

// Name returns the stable registry identifier.
func (c *Claude) Name() string { return ClaudeBackendName }

// DisplayName returns the human-readable label.
func (c *Claude) DisplayName() string { return "Claude Code" }

// ResolveExecutable locates the `claude` binary: settings map → PATH → fallbacks.
func (c *Claude) ResolveExecutable(s *models.Settings) (string, error) {
	if s != nil && s.Agents != nil {
		if cfg, ok := s.Agents[ClaudeBackendName]; ok && cfg != nil && cfg.Path != "" {
			if _, err := os.Stat(cfg.Path); err == nil {
				return cfg.Path, nil
			}
		}
	}

	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	homeDir, _ := os.UserHomeDir()
	fallbacks := []string{
		filepath.Join(homeDir, ".claude", "local", "claude"),
		filepath.Join(homeDir, ".local", "bin", "claude"),
		filepath.Join(homeDir, ".npm-global", "bin", "claude"),
	}
	if runtime.GOOS == "darwin" {
		fallbacks = append(fallbacks,
			"/opt/homebrew/bin/claude",
			"/usr/local/bin/claude",
		)
	} else {
		fallbacks = append(fallbacks,
			"/usr/local/bin/claude",
			"/usr/bin/claude",
		)
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("claude binary not found. Install Claude Code or set path in ~/.watchfire/settings.yaml")
}

// BuildCommand produces the Claude Code PTY invocation.
func (c *Claude) BuildCommand(opts CommandOpts) (Command, error) {
	args := []string{
		"--name", opts.SessionName,
		"--append-system-prompt", opts.SystemPrompt,
		"--dangerously-skip-permissions",
	}
	if opts.InitialPrompt != "" {
		args = append(args, opts.InitialPrompt)
	}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}
	return Command{Args: args, PasteInitialPrompt: false}, nil
}

// SandboxExtras returns the Claude-specific paths the sandbox policy must allow.
func (c *Claude) SandboxExtras() SandboxExtras {
	return SandboxExtras{
		WritableSubpaths: []string{"~/.claude"},
		WritableLiterals: []string{"~/.claude.json"},
		CachePatterns:    []string{"~/Library/Caches/claude-cli-nodejs"},
		StripEnv:         []string{"CLAUDECODE"},
	}
}

// InstallSystemPrompt is a no-op for Claude — the prompt is delivered via
// --append-system-prompt on the command line.
func (c *Claude) InstallSystemPrompt(workDir, composedPrompt string) error {
	return nil
}

// LocateTranscript finds the Claude Code JSONL transcript for a session.
// workDir is where claude ran; sessionHint is the --name value (matches
// customTitle in the JSONL first line).
func (c *Claude) LocateTranscript(workDir string, started time.Time, sessionHint string) (string, error) {
	if workDir == "" || sessionHint == "" {
		return "", fmt.Errorf("workDir and sessionHint are required")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	encoded := strings.ReplaceAll(workDir, "/", "-")
	transcriptDir := filepath.Join(homeDir, ".claude", "projects", encoded)

	entries, err := os.ReadDir(transcriptDir)
	if err != nil {
		return "", fmt.Errorf("transcript dir not found: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(transcriptDir, e.Name())
		title, err := readClaudeTranscriptTitle(path)
		if err != nil {
			continue
		}
		if title == sessionHint {
			return path, nil
		}
	}

	return "", fmt.Errorf("no transcript found for session %q in %s", sessionHint, transcriptDir)
}

func readClaudeTranscriptTitle(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return "", fmt.Errorf("empty file")
	}

	var entry struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		return "", err
	}
	if entry.Type != "custom-title" {
		return "", fmt.Errorf("first line is not custom-title")
	}
	return entry.CustomTitle, nil
}

type claudeTranscriptEntry struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type claudeTranscriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input any    `json:"input"`
	ID    string `json:"id"`
}

// FormatTranscript reads a Claude Code JSONL transcript and renders it as
// readable User/Assistant/System text.
func (c *Claude) FormatTranscript(jsonlPath string) (string, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var entry claudeTranscriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		switch entry.Type {
		case "user", "assistant", "system":
			formatClaudeMessage(&sb, entry)
		}
	}

	return sb.String(), scanner.Err()
}

func formatClaudeMessage(sb *strings.Builder, entry claudeTranscriptEntry) {
	var msg claudeTranscriptMessage
	if err := json.Unmarshal(entry.Message, &msg); err != nil || len(msg.Content) == 0 {
		return
	}

	var contentStr string
	if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
		if strings.TrimSpace(contentStr) == "" {
			return
		}
		sb.WriteString("## ")
		sb.WriteString(claudeRoleLabel(msg.Role))
		sb.WriteString("\n\n")
		sb.WriteString(contentStr)
		sb.WriteString("\n\n")
		return
	}

	var blocks []claudeContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}

	hasToolResult := false
	for _, b := range blocks {
		if b.Type == "tool_result" {
			hasToolResult = true
			break
		}
	}
	if hasToolResult {
		return
	}

	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case "tool_use":
			parts = append(parts, fmt.Sprintf("[Tool: %s]", b.Name))
		case "thinking":
			// skip — internal reasoning
		}
	}

	if len(parts) == 0 {
		return
	}

	sb.WriteString("## ")
	sb.WriteString(claudeRoleLabel(msg.Role))
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(parts, "\n\n"))
	sb.WriteString("\n\n")
}

func claudeRoleLabel(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "system":
		return "System"
	default:
		return strings.Title(role) //nolint:staticcheck
	}
}

func init() {
	Register(&Claude{})
}
