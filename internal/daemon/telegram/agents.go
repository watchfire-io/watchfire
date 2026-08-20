// /agent — inspect and switch the active project's default agent
// backend from Telegram. Pure read + one YAML field write through the
// AgentSelector seam; no PTY, no session surface.
package telegram

import (
	"context"
	"fmt"
	"strings"
)

// AgentChoice describes one known agent backend for /agent.
type AgentChoice struct {
	Name        string // stable backend key, e.g. "claude-code"
	DisplayName string // e.g. "Claude Code"
	Available   bool   // executable resolves on this machine
}

// AgentSelector lets /agent list the known backends and change a
// project's default agent. The production implementation lives in the
// server package (backend registry + project YAML); tests inject stubs.
type AgentSelector interface {
	ListAgents(ctx context.Context) ([]AgentChoice, error)
	// ProjectAgent returns the project's current default agent key.
	ProjectAgent(ctx context.Context, projectID string) (string, error)
	// SetProjectAgent persists the project's default agent.
	SetProjectAgent(ctx context.Context, projectID, agent string) error
}

// cmdAgent shows or sets the active project's default agent backend.
// Bare /agent lists the choices (✓ current, install state); /agent
// <name> matches by key or display name (exact, then unique prefix)
// and persists. The setting applies to NEW sessions — a running chat
// keeps its backend until /new restarts it.
func (b *Bridge) cmdAgent(ctx context.Context, chatID int64, rest string) {
	if b.agents == nil {
		b.reply(ctx, chatID, "Agent selection is not wired up on this daemon.")
		return
	}
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
		return
	}
	choices, err := b.agents.ListAgents(ctx)
	if err != nil {
		b.reply(ctx, chatID, "Failed to list agents: "+EscapeHTML(err.Error()))
		return
	}
	if len(choices) == 0 {
		b.reply(ctx, chatID, "No agent backends are registered on this daemon.")
		return
	}
	current, _ := b.agents.ProjectAgent(ctx, projectID)

	arg := strings.TrimSpace(rest)
	if arg == "" {
		lines := []string{"<b>Agents</b>"}
		for _, c := range choices {
			marker := ""
			if c.Name == current {
				marker = " ✓"
			}
			avail := ""
			if !c.Available {
				avail = " — not installed"
			}
			lines = append(lines, fmt.Sprintf("• %s (%s)%s%s", EscapeHTML(c.DisplayName), EscapeHTML(c.Name), avail, marker))
		}
		lines = append(lines, "", "<i>Send /agent &lt;name&gt; to switch. Applies to new sessions — /new restarts chat with it.</i>")
		b.reply(ctx, chatID, strings.Join(lines, "\n"))
		return
	}

	match, errMsg := pickAgent(choices, arg)
	if errMsg != "" {
		b.reply(ctx, chatID, errMsg)
		return
	}
	if !match.Available {
		b.reply(ctx, chatID, "<b>"+EscapeHTML(match.DisplayName)+"</b> is not installed on this machine.")
		return
	}
	if err := b.agents.SetProjectAgent(ctx, projectID, match.Name); err != nil {
		b.reply(ctx, chatID, "Failed to set the agent: "+EscapeHTML(err.Error()))
		return
	}
	b.reply(ctx, chatID, "🤖 Default agent set to <b>"+EscapeHTML(match.DisplayName)+"</b>. Applies to new sessions — send /new to restart chat with it.")
}

// pickAgent resolves arg against the choices: exact key, exact display
// name, then unique case-insensitive prefix on either. Returns a
// user-facing error message when nothing (or too much) matches.
func pickAgent(choices []AgentChoice, arg string) (AgentChoice, string) {
	lower := strings.ToLower(arg)
	for _, c := range choices {
		if strings.EqualFold(c.Name, arg) || strings.EqualFold(c.DisplayName, arg) {
			return c, ""
		}
	}
	var hits []AgentChoice
	for _, c := range choices {
		if strings.HasPrefix(strings.ToLower(c.Name), lower) || strings.HasPrefix(strings.ToLower(c.DisplayName), lower) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		return AgentChoice{}, "No agent matches " + EscapeHTML(arg) + " — send /agent for the list."
	default:
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			names = append(names, EscapeHTML(h.Name))
		}
		return AgentChoice{}, "Ambiguous — matches: " + strings.Join(names, ", ")
	}
}
