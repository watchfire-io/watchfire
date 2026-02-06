package models

import "time"

// DaemonInfo represents the daemon connection information.
// This corresponds to ~/.watchfire/daemon.yaml.
type DaemonInfo struct {
	Version   int       `yaml:"version"`
	Host      string    `yaml:"host"`
	Port      int       `yaml:"port"`
	PID       int       `yaml:"pid"`
	StartedAt time.Time `yaml:"started_at"`
}

// NewDaemonInfo creates a new daemon info with current values.
func NewDaemonInfo(host string, port, pid int) *DaemonInfo {
	return &DaemonInfo{
		Version:   1,
		Host:      host,
		Port:      port,
		PID:       pid,
		StartedAt: time.Now().UTC(),
	}
}

// AgentState represents the running agents file.
// This corresponds to ~/.watchfire/agents.yaml.
type AgentState struct {
	Version int                `yaml:"version"`
	Agents  []RunningAgentInfo `yaml:"agents"`
}

// RunningAgentInfo represents a running agent entry in agents.yaml.
type RunningAgentInfo struct {
	ProjectID    string `yaml:"project_id"`
	ProjectName  string `yaml:"project_name"`
	ProjectPath  string `yaml:"project_path"`
	Mode         string `yaml:"mode"` // "chat" | "task" | "start-all" | "wildfire"
	TaskNumber   int    `yaml:"task_number,omitempty"`
	TaskTitle    string `yaml:"task_title,omitempty"`
	IssueType    string `yaml:"issue_type,omitempty"`    // "auth_required" | "rate_limited" | ""
	IssueMessage string `yaml:"issue_message,omitempty"` // Original error message
}

// NewAgentState creates a new empty agent state.
func NewAgentState() *AgentState {
	return &AgentState{
		Version: 1,
		Agents:  []RunningAgentInfo{},
	}
}
