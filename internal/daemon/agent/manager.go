// Package agent handles agent lifecycle management for the daemon.
package agent

import (
	"fmt"
	"log"
	"sync"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// Mode defines the mode an agent runs in.
type Mode string

// Agent modes.
const (
	ModeChat     Mode = "chat"
	ModeTask     Mode = "task"
	ModeWildfire Mode = "wildfire"
)

// RunningAgent tracks a currently running agent session.
type RunningAgent struct {
	ProjectID   string
	ProjectName string
	ProjectPath string
	Mode        Mode
	TaskNumber  int
	TaskTitle   string
}

// StartOptions contains options for starting an agent.
type StartOptions struct {
	ProjectID   string
	ProjectName string
	ProjectPath string
	Mode        Mode
	TaskNumber  int
	TaskTitle   string
}

// Manager handles agent lifecycle operations.
type Manager struct {
	mu     sync.RWMutex
	agents map[string]*RunningAgent // keyed by ProjectID
}

// NewManager creates a new agent manager.
func NewManager() *Manager {
	return &Manager{
		agents: make(map[string]*RunningAgent),
	}
}

// StartAgent starts an agent for the given project.
// This is a stub that will be implemented when PTY/sandbox spawning is ready.
func (m *Manager) StartAgent(opts StartOptions) error {
	return fmt.Errorf("agent spawning not yet implemented")
}

// StopAgent stops a running agent for the given project.
func (m *Manager) StopAgent(projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.agents[projectID]; !ok {
		return fmt.Errorf("no agent running for project: %s", projectID)
	}

	delete(m.agents, projectID)
	m.persistStateLocked()
	return nil
}

// GetAgent returns a running agent by project ID.
func (m *Manager) GetAgent(projectID string) (*RunningAgent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[projectID]
	return agent, ok
}

// ListAgents returns all running agents.
func (m *Manager) ListAgents() []*RunningAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agents := make([]*RunningAgent, 0, len(m.agents))
	for _, a := range m.agents {
		agents = append(agents, a)
	}
	return agents
}

// ActiveCount returns the number of running agents.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.agents)
}

// persistStateLocked writes the current agent state to ~/.watchfire/agents.yaml.
// Must be called while holding m.mu.
func (m *Manager) persistStateLocked() {
	state := models.NewAgentState()
	for _, a := range m.agents {
		state.Agents = append(state.Agents, models.RunningAgentInfo{
			ProjectID:   a.ProjectID,
			ProjectName: a.ProjectName,
			ProjectPath: a.ProjectPath,
			Mode:        string(a.Mode),
			TaskNumber:  a.TaskNumber,
			TaskTitle:   a.TaskTitle,
		})
	}
	if err := config.SaveAgentState(state); err != nil {
		log.Printf("Failed to persist agent state: %v", err)
	}
}
