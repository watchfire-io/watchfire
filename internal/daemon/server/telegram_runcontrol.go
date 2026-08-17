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
