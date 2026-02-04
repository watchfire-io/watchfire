// Package tray implements the system tray icon and menu for the daemon.
package tray

// DaemonState provides read-only access to daemon state for the tray.
type DaemonState interface {
	Port() int
	ProjectCount() int
	ActiveAgents() []AgentInfo
	RequestShutdown()
}

// AgentInfo describes a running agent for display in the tray menu.
type AgentInfo struct {
	ProjectName string
	Mode        string // "chat", "task", "wildfire"
	TaskNumber  int
	TaskTitle   string
}
