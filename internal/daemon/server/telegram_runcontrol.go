// v10.0 Torch, task 0142 — the run-control seam the Telegram bridge's
// /run /runall /say verbs act through. Counterpart of the read-only
// agentSessionSource (telegram_sessions.go): this adapter is the only
// place Telegram-originated writes touch the agent manager, and it
// enforces the same contract as the MCP run_task tool — an in-flight
// agent is never queued behind and never replaced.
package server

import (
	"context"
	"fmt"

	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/telegram"
	"github.com/watchfire-io/watchfire/internal/daemon/watcher"
	pb "github.com/watchfire-io/watchfire/proto"
)

// agentRunController adapts the agent manager to telegram.RunController.
type agentRunController struct {
	mgr     *agent.Manager
	watcher *watcher.Watcher
}

// StartTask starts a task-mode session, refusing while an agent runs.
func (c *agentRunController) StartTask(ctx context.Context, projectID string, taskNumber int) (telegram.RunStart, error) {
	return c.start(ctx, projectID, string(agent.ModeTask), taskNumber)
}

// StartRunAll starts run-all mode, refusing while an agent runs.
func (c *agentRunController) StartRunAll(ctx context.Context, projectID string) (telegram.RunStart, error) {
	return c.start(ctx, projectID, string(agent.ModeStartAll), 0)
}

// StartChat starts an interactive chat-mode session — the bridge's
// plain-text conversation path auto-starts one when nothing is
// running. Refuses while an agent runs, like the other starters (and
// the daemon's own chat-over-non-chat refusal backs this up).
func (c *agentRunController) StartChat(ctx context.Context, projectID string) (telegram.RunStart, error) {
	return c.start(ctx, projectID, string(agent.ModeChat), 0)
}

// StartWildfire starts the autonomous wildfire loop (/wildfire on),
// refusing while an agent runs.
func (c *agentRunController) StartWildfire(ctx context.Context, projectID string) (telegram.RunStart, error) {
	return c.start(ctx, projectID, string(agent.ModeWildfire), 0)
}

// StartGenerate starts a generate-definition session (/generate),
// refusing while an agent runs.
func (c *agentRunController) StartGenerate(ctx context.Context, projectID string) (telegram.RunStart, error) {
	return c.start(ctx, projectID, string(agent.ModeGenerateDefinition), 0)
}

// StartPlan starts a generate-tasks session (/plan), refusing while an
// agent runs.
func (c *agentRunController) StartPlan(ctx context.Context, projectID string) (telegram.RunStart, error) {
	return c.start(ctx, projectID, string(agent.ModeGenerateTasks), 0)
}

// RestartChat starts a fresh chat session (/new). Deliberately NO
// manual refusal pre-check: Manager.StartAgent atomically replaces
// chat-with-chat (kill + wait + spawn) and itself refuses a chat start
// that would displace a working non-chat agent or a mid-chain
// transition, so the daemon's own guard is the backstop here.
func (c *agentRunController) RestartChat(ctx context.Context, projectID string) (telegram.RunStart, error) {
	return c.startUnchecked(ctx, projectID, string(agent.ModeChat), 0)
}

// StopAgent user-stops the running agent — /wildfire off. User-stop
// semantics keep a wildfire / run-all chain from continuing.
func (c *agentRunController) StopAgent(projectID string) error {
	return c.mgr.StopAgentByUser(projectID)
}

// start delegates to the agentService StartAgent path — the same code
// the gRPC surface (and therefore MCP run_task / run_all) goes
// through, with the same synthetic-request shape the tray uses: rows/
// cols 0 (daemon default PTY size) and sandbox "auto".
//
// Manager.StartAgent REPLACES a running agent, so the refusal check
// here is mandatory, mirroring the MCP server's requireNoRunningAgent.
// The bridge pre-checks through its SessionSource for a richer chat
// message; this is the authoritative backstop.
func (c *agentRunController) start(ctx context.Context, projectID, mode string, taskNumber int) (telegram.RunStart, error) {
	if ag, running := c.mgr.GetAgent(projectID); running && ag != nil {
		desc := fmt.Sprintf("mode %q", ag.Mode)
		if ag.TaskNumber > 0 {
			desc += fmt.Sprintf(", task #%04d %q", ag.TaskNumber, ag.TaskTitle)
		}
		return telegram.RunStart{}, fmt.Errorf("an agent is already running for this project (%s); runs are never queued or replaced", desc)
	}
	return c.startUnchecked(ctx, projectID, mode, taskNumber)
}

// startUnchecked delegates to StartAgent without the refusal pre-check
// — for RestartChat, where the daemon's own replace/refuse semantics
// are exactly the desired contract.
func (c *agentRunController) startUnchecked(ctx context.Context, projectID, mode string, taskNumber int) (telegram.RunStart, error) {
	svc := &agentService{manager: c.mgr, watcher: c.watcher}
	st, err := svc.StartAgent(ctx, &pb.StartAgentRequest{
		Meta:       &pb.RequestMeta{Origin: "telegram"},
		ProjectId:  projectID,
		Mode:       mode,
		TaskNumber: int32(taskNumber), //nolint:gosec // task numbers are small
		Sandbox:    "auto",
	})
	if err != nil {
		return telegram.RunStart{}, err
	}
	return telegram.RunStart{TaskNumber: int(st.TaskNumber), TaskTitle: st.TaskTitle}, nil
}

// SendInput writes raw bytes to the running agent's PTY — the /say
// path, and the only Telegram-reachable PTY write.
func (c *agentRunController) SendInput(projectID string, data []byte) error {
	ag, ok := c.mgr.GetAgent(projectID)
	if !ok || ag == nil || ag.Process == nil {
		return fmt.Errorf("no agent is running for this project")
	}
	return ag.Process.SendInput(data)
}
