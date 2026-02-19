// Package tray implements the system tray icon and menu for the daemon.
package tray

// DaemonState provides access to daemon state for the tray.
type DaemonState interface {
	Port() int
	ProjectCount() int
	ActiveAgents() []AgentInfo
	StopAgent(projectID string)
	RequestShutdown()
	UpdateAvailable() (available bool, version string)
}

// AgentInfo describes a running agent for display in the tray menu.
type AgentInfo struct {
	ProjectID    string
	ProjectName  string
	ProjectColor string // Hex color for display
	Mode         string // "chat", "task", "start-all", "wildfire"
	TaskNumber   int
	TaskTitle    string
}
