// telegram.AgentSelector implementation (/agent): the backend registry
// plus the project YAML's default_agent field. Sibling of the
// read-only agentSessionSource and the run-control seam — this one
// touches exactly one config field and nothing else.
package server

import (
	"context"
	"fmt"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/daemon/telegram"
)

// telegramAgentSelector adapts backend.List + project.yaml to
// telegram.AgentSelector.
type telegramAgentSelector struct{ srv *Server }

// ListAgents mirrors SettingsService.ListAgents: every registered
// backend with its display name and whether its executable resolves.
func (a *telegramAgentSelector) ListAgents(_ context.Context) ([]telegram.AgentChoice, error) {
	settings, _ := config.LoadSettings()
	backends := backend.List()
	out := make([]telegram.AgentChoice, 0, len(backends))
	for _, b := range backends {
		_, resolveErr := b.ResolveExecutable(settings)
		out = append(out, telegram.AgentChoice{
			Name:        b.Name(),
			DisplayName: b.DisplayName(),
			Available:   resolveErr == nil,
		})
	}
	return out, nil
}

// ProjectAgent reads the project's default_agent from its YAML.
func (a *telegramAgentSelector) ProjectAgent(_ context.Context, projectID string) (string, error) {
	path := a.srv.projectPathForID(projectID)
	if path == "" {
		return "", fmt.Errorf("project not found")
	}
	proj, err := config.LoadProject(path)
	if err != nil {
		return "", err
	}
	if proj == nil {
		return "", fmt.Errorf("project not found")
	}
	return proj.DefaultAgent, nil
}

// SetProjectAgent validates the backend key and persists it as the
// project's default agent. Running sessions keep their backend; the
// change applies from the next StartAgent (resolveBackend reads it).
func (a *telegramAgentSelector) SetProjectAgent(_ context.Context, projectID, agentName string) error {
	if _, ok := backend.Get(agentName); !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	path := a.srv.projectPathForID(projectID)
	if path == "" {
		return fmt.Errorf("project not found")
	}
	proj, err := config.LoadProject(path)
	if err != nil {
		return err
	}
	if proj == nil {
		return fmt.Errorf("project not found")
	}
	proj.DefaultAgent = agentName
	return config.SaveProject(path, proj)
}
