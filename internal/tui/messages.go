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

// TasksLoadedMsg carries task list from ListTasks RPC, plus any task files
// that failed to load (ListMalformedTasks) so the status bar can warn that a
// broken task file is sitting on disk instead of silently dropping it.
type TasksLoadedMsg struct {
	Tasks     []*pb.Task
	Malformed []*pb.MalformedTask
}

// AgentStatusMsg carries agent status from GetAgentStatus RPC.
type AgentStatusMsg struct {
	Status *pb.AgentStatus
}

// RawOutputMsg carries a chunk of raw PTY bytes from the daemon's
// SubscribeRawOutput stream. Replaced ScreenUpdateMsg in v6 — the TUI
// now drives its own vt10x emulator so it can layer scrollback on top
// of the visible grid.
type RawOutputMsg struct {
	Data []byte
}

// AgentIssueMsg carries an agent issue notification.
type AgentIssueMsg struct {
	Issue *pb.AgentIssue
}

// TasksBatchCreatedMsg signals a quick-add batch was created (v10 Torch).
type TasksBatchCreatedMsg struct {
	Tasks []*pb.Task
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

// TaskRestoredMsg signals a soft-deleted task was restored to active state.
type TaskRestoredMsg struct{}

// TaskPermanentDeletedMsg signals a task was hard-deleted from disk.
type TaskPermanentDeletedMsg struct{}

// TickMsg is a periodic tick for polling.
type TickMsg struct{}

// ClearErrorMsg clears the error display.
type ClearErrorMsg struct{}

// StatusMsg carries an informational status-bar message that auto-clears
// after a few seconds (same lifetime as ProjectSavedMsg's "Saved" badge).
// Used by danger-zone actions and integration toggles to acknowledge a
// successful RPC without piggy-backing on the error channel.
type StatusMsg struct {
	Text string
}

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

// LogDeletedMsg signals that a delete-log RPC completed.
type LogDeletedMsg struct {
	LogID string
	Err   error
}

// UpdateAvailableMsg signals that a daemon update is available.
type UpdateAvailableMsg struct {
	Version string
}

// GitInfoMsg carries git info from GetGitInfo RPC.
type GitInfoMsg struct {
	Info *pb.GitInfo
}

// SettingsLoadedMsg carries global settings from GetSettings RPC.
type SettingsLoadedMsg struct {
	Settings *pb.Settings
}

// SettingsSavedMsg signals a successful settings update.
type SettingsSavedMsg struct {
	Settings *pb.Settings
}

// IntegrationsLoadedMsg carries the IntegrationsConfig from List /
// Save / Delete RPCs. v7.0 Relay.
type IntegrationsLoadedMsg struct {
	Config *pb.IntegrationsConfig
}

// IntegrationTestedMsg surfaces the result of a TestIntegration RPC so
// the overlay can render an inline "HTTP 200" / "HTTP 4xx" status.
type IntegrationTestedMsg struct {
	OK         bool
	Message    string
	StatusCode int32
}

// TelegramPairingBeganMsg carries the result of a BeginTelegramPairing
// RPC (v10.0 Torch). Err is set on failure — most commonly the
// FailedPrecondition "bridge not running" case, which is a normal
// user-facing state surfaced in the overlay's status line rather than
// as a global error.
type TelegramPairingBeganMsg struct {
	Resp *pb.BeginTelegramPairingResponse
	Err  error
}

// TelegramPairingStatusMsg carries a polled Telegram pairing status.
// The integrations overlay polls every 2s while a code is pending.
type TelegramPairingStatusMsg struct {
	Status *pb.TelegramPairingStatus
}

// telegramPairingPollTickMsg fires 2s after the previous pairing-status
// poll (or after BeginTelegramPairing) to trigger the next one.
type telegramPairingPollTickMsg struct{}

// InboundStatusLoadedMsg carries the v8.0 Echo InboundStatus from
// GetInboundStatus / SaveInboundConfig RPCs. The Inbound tab in the
// integrations overlay reads this to refresh the listening pill +
// last-delivery rows.
type InboundStatusLoadedMsg struct {
	Status *pb.InboundStatus
}

// OAuthBeganMsg carries the result of a BeginOAuth RPC. The TUI
// surfaces the authorize URL so the user can copy it into their
// browser if the daemon's best-effort launch failed.
type OAuthBeganMsg struct {
	Provider     pb.OAuthProvider
	AuthorizeURL string
	RedirectURI  string
}

// OAuthStatusLoadedMsg carries the per-provider OAuth status. The
// integrations overlay polls this on a timer while a flow is in
// progress; the resulting state drives the "Connected as ..." pill.
type OAuthStatusLoadedMsg struct {
	Status *pb.OAuthStatus
}

// ReorderCompletedMsg carries the refreshed task list from a successful
// ReorderTasks RPC. The dispatcher uses the response to lock in the
// optimistic local swap and clears the in-flight flag.
type ReorderCompletedMsg struct {
	Tasks   []*pb.Task
	Focused int32
}

// ReorderFailedMsg signals that the ReorderTasks RPC errored. The
// dispatcher reverts to the pre-swap snapshot and surfaces a one-shot
// toast via the existing error bar.
type ReorderFailedMsg struct {
	Err     error
	Focused int32
}

// McpClientStatusLoadedMsg carries the per-harness MCP onboarding state from
// SettingsService.GetMcpClientStatus (v9.0 Firestorm). The Settings view's MCP
// section fetches this on focus; the daemon is the only source of truth for
// detected / configured — the TUI never reads a client config itself.
type McpClientStatusLoadedMsg struct {
	List *pb.McpClientStatusList
	Err  error
}

// McpClientInstalledMsg carries the post-install state of one harness from
// SettingsService.InstallMcpClient. Install problems are not RPC errors: they
// come back with Configured=false and a Message holding the manual snippet,
// which the MCP section renders inline.
type McpClientInstalledMsg struct {
	Client string
	Status *pb.McpClientStatus
	Err    error
}

// mcpSpinnerTickMsg advances the MCP section's inline install spinner. It is
// separate from spinnerTickMsg (which is gated on a running agent) so the
// spinner keeps animating while an InstallMcpClient RPC is in flight.
type mcpSpinnerTickMsg struct{}

// OAuthHelloPostedMsg carries the result of a PostOAuthHello call.
// The TUI surfaces this as a one-shot status banner.
type OAuthHelloPostedMsg struct {
	Provider pb.OAuthProvider
	OK       bool
	Message  string
}
