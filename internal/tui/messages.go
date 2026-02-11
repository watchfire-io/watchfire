package tui

import (
	"google.golang.org/grpc"

	pb "github.com/watchfire-io/watchfire/proto"
)

// DaemonConnectedMsg signals a successful gRPC connection.
type DaemonConnectedMsg struct {
	Conn *grpc.ClientConn
}

// DaemonDisconnectedMsg signals the daemon connection was lost.
type DaemonDisconnectedMsg struct{}

// ProjectLoadedMsg carries project data from GetProject RPC.
type ProjectLoadedMsg struct {
	Project *pb.Project
}

// TasksLoadedMsg carries task list from ListTasks RPC.
type TasksLoadedMsg struct {
	Tasks []*pb.Task
}

// AgentStatusMsg carries agent status from GetAgentStatus RPC.
type AgentStatusMsg struct {
	Status *pb.AgentStatus
}

// ScreenUpdateMsg carries rendered ANSI screen content from SubscribeScreen stream.
type ScreenUpdateMsg struct {
	AnsiContent string
}

// AgentIssueMsg carries an agent issue notification.
type AgentIssueMsg struct {
	Issue *pb.AgentIssue
}

// TaskSavedMsg signals a task was created or updated.
type TaskSavedMsg struct {
	Task *pb.Task
}

// ProjectSavedMsg signals project was updated.
type ProjectSavedMsg struct {
	Project *pb.Project
}

// ErrorMsg carries an error to display.
type ErrorMsg struct {
	Err error
}

// AgentStartedMsg signals the agent was successfully started.
type AgentStartedMsg struct {
	Status *pb.AgentStatus
}

// AgentStoppedMsg signals the agent was stopped.
type AgentStoppedMsg struct{}

// ScreenEndedMsg signals the screen subscription stream ended.
type ScreenEndedMsg struct{}

// TaskDeletedMsg signals a task was deleted.
type TaskDeletedMsg struct{}

// TickMsg is a periodic tick for polling.
type TickMsg struct{}

// ClearErrorMsg clears the error display.
type ClearErrorMsg struct{}

// ClearSavedMsg clears the "Saved" indicator.
type ClearSavedMsg struct{}

// ReconnectMsg triggers a reconnection attempt.
type ReconnectMsg struct{}

// EditorFinishedMsg carries the result of an external editor session.
type EditorFinishedMsg struct {
	Content string
	Err     error
}

// spinnerTickMsg advances the animated spinner for active tasks.
type spinnerTickMsg struct{}

// LogsLoadedMsg carries the list of session logs.
type LogsLoadedMsg struct {
	Logs []*pb.LogEntry
}

// LogContentMsg carries a single log's content.
type LogContentMsg struct {
	Entry   *pb.LogEntry
	Content string
}
