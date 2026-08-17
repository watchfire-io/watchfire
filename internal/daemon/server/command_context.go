// Package server — production CommandContext for the v8.0 Echo inbound
// slash-command router (v10.0 Torch, task 0133).
//
// The echo package's `Route` dispatches `/watchfire status|retry|cancel`
// through five injected callbacks on `echo.CommandContext`
// (FindProjects / LookupTask / ListTopActiveTasks / Retry / Cancel).
// Until v10 those callbacks had no production implementation — the
// Slack/Discord handlers shipped in v5.x were never registered, so the
// router was reachable only from tests. This file is the daemon-side
// implementation; the v10 Telegram bridge routes through the same
// callbacks, so keep them transport-agnostic (the only transport hint
// is the scope's GuildID/TeamID, used for project mapping and the
// default cancel reason).
//
// The implementation lives in `server` (not `echo`) because it reaches
// into the projects index, per-project YAML, the task manager, and the
// agent manager — concerns the echo package deliberately stays clear
// of so the handler tests don't drag the full daemon graph in.
package server

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

// commandScope identifies the calling chat surface for one request.
// Exactly one of GuildID (Discord) / TeamID (Slack) / Telegram is set.
// Telegram carries no id: a Telegram chat only reaches the router
// after pairing, and pairing binds the chat to the daemon owner — so
// the flag alone is the scope (see resolveMappedProjects). UserID is
// carried for audit logging only.
type commandScope struct {
	GuildID  string
	TeamID   string
	Telegram bool
	UserID   string
}

// commandContextDeps is the seam tests use to inject fake loaders /
// managers. The real wiring in `Server.commandContextDeps` plugs the
// production `config.*` calls and the live task / agent managers in.
type commandContextDeps struct {
	LoadIntegrations  func() (*models.IntegrationsConfig, error)
	LoadProjectsIndex func() (*models.ProjectsIndex, error)
	LoadProject       func(projectPath string) (*models.Project, error)
	ListTasks         func(projectPath string, opts task.ListOptions) ([]*models.Task, error)
	GetTask           func(projectPath string, taskNumber int) (*models.Task, error)
	// SetTaskStatus is the interactive bulk set-status path
	// (`task.Manager.BulkUpdateStatus`) — the same path the TUI/GUI use
	// for retry, so moving a task back out of `done` clears the
	// success / failure_reason / completed_at trio consistently.
	SetTaskStatus func(projectPath string, taskNumbers []int, status string) ([]*models.Task, error)
	SaveTask      func(projectPath string, t *models.Task) error
	// AgentTaskNumber reports the task number the project's running
	// agent is working on (0 for chat/generate modes). ok=false when no
	// agent is running for the project.
	AgentTaskNumber func(projectID string) (taskNumber int, ok bool)
	// StopAgentByUser stops the project's agent with user-stop
	// semantics (no chaining into the next ready task) — a cancel
	// issued from chat is a user intent, same as the TUI/GUI stop.
	StopAgentByUser func(projectID string) error
}

// commandContextDeps builds the production dependency set from the
// server's managers. Loaders resolve per call so config edits (new
// projects, changed bindings) take effect without a daemon restart.
func (s *Server) commandContextDeps() commandContextDeps {
	return commandContextDeps{
		LoadIntegrations:  config.LoadIntegrations,
		LoadProjectsIndex: config.LoadProjectsIndex,
		LoadProject:       config.LoadProject,
		ListTasks:         s.taskManager.ListTasks,
		GetTask:           s.taskManager.GetTask,
		SetTaskStatus:     s.taskManager.BulkUpdateStatus,
		SaveTask:          config.SaveTask,
		AgentTaskNumber: func(projectID string) (int, bool) {
			ag, ok := s.agentManager.GetAgent(projectID)
			if !ok {
				return 0, false
			}
			return ag.TaskNumber, true
		},
		StopAgentByUser: s.agentManager.StopAgentByUser,
	}
}

// slackCommandContextFor is the `CommandContextFor` factory wired into
// the Slack slash-command + interactivity handlers. Scopes every
// callback to the calling workspace so a daemon serving multiple
// workspaces never crosses project boundaries.
func (s *Server) slackCommandContextFor(teamID, userID string) echo.CommandContext {
	return newCommandContext(commandScope{TeamID: teamID, UserID: userID}, s.commandContextDeps())
}

// discordCommandContextFor is the `CommandContextFor` factory wired
// into the Discord interactions handler. Scopes every callback to the
// calling guild.
func (s *Server) discordCommandContextFor(guildID, userID string) echo.CommandContext {
	return newCommandContext(commandScope{GuildID: guildID, UserID: userID}, s.commandContextDeps())
}

// telegramCommandContextFor is the factory wired into the Telegram
// bridge (v10.0 Torch, task 0137). Telegram chats are paired to the
// daemon owner — pairing is the authorization boundary, enforced by
// the bridge before any command reaches the router — so unlike the
// guild/team-scoped Slack/Discord factories this scope sees every
// registered project. chatID is accepted for symmetry with the bridge
// callback shape but deliberately unused: per-chat state (the active
// project) lives in the bridge, not in project visibility.
func (s *Server) telegramCommandContextFor(chatID, userID int64) echo.CommandContext {
	_ = chatID
	return newCommandContext(commandScope{Telegram: true, UserID: strconv.FormatInt(userID, 10)}, s.commandContextDeps())
}

// mappedProject pairs the router-facing ProjectInfo with the on-disk
// path the task lifecycle callbacks need.
type mappedProject struct {
	info echo.ProjectInfo
	path string
}

// newCommandContext assembles an echo.CommandContext whose callbacks
// are backed by deps, scoped to the given guild / team.
func newCommandContext(scope commandScope, deps commandContextDeps) echo.CommandContext {
	return echo.CommandContext{
		GuildID: scope.GuildID,
		TeamID:  scope.TeamID,
		UserID:  scope.UserID,

		FindProjects: func(ctx context.Context) ([]echo.ProjectInfo, error) {
			mapped, err := resolveMappedProjects(scope, deps)
			if err != nil {
				return nil, err
			}
			infos := make([]echo.ProjectInfo, 0, len(mapped))
			for _, m := range mapped {
				info := m.info
				// Live agent state for status glyphs (v10.0 Torch,
				// additive on ProjectInfo — renderers that don't care
				// simply ignore the fields).
				if n, running := deps.AgentTaskNumber(info.ID); running {
					info.AgentRunning = true
					info.AgentTaskNumber = n
				}
				infos = append(infos, info)
			}
			return infos, nil
		},

		LookupTask: func(ctx context.Context, taskRef string) (*models.Task, echo.ProjectInfo, error) {
			number, id, ok := echo.ParseTaskRef(taskRef)
			if !ok {
				return nil, echo.ProjectInfo{}, echo.ErrTaskNotFound
			}
			mapped, err := resolveMappedProjects(scope, deps)
			if err != nil {
				return nil, echo.ProjectInfo{}, err
			}
			for _, m := range mapped {
				// ListTasks without IncludeDeleted honours soft-delete:
				// a trashed task is invisible to chat commands.
				tasks, listErr := deps.ListTasks(m.path, task.ListOptions{})
				if listErr != nil {
					return nil, echo.ProjectInfo{}, fmt.Errorf("list tasks in %s: %w", m.info.Name, listErr)
				}
				for _, t := range tasks {
					if (id != "" && t.TaskID == id) || (id == "" && t.TaskNumber == number) {
						return t, m.info, nil
					}
				}
			}
			return nil, echo.ProjectInfo{}, echo.ErrTaskNotFound
		},

		ListTopActiveTasks: func(ctx context.Context, projectID string, limit int) ([]*models.Task, error) {
			m, err := mappedProjectByID(scope, deps, projectID)
			if err != nil {
				return nil, err
			}
			tasks, err := deps.ListTasks(m.path, task.ListOptions{})
			if err != nil {
				return nil, err
			}
			// "Active" = the task the running agent is working on right
			// now (first), then the ready queue in canonical order.
			var out []*models.Task
			current, running := deps.AgentTaskNumber(projectID)
			if running && current > 0 {
				for _, t := range tasks {
					if t.TaskNumber == current && t.Status != models.TaskStatusDone {
						out = append(out, t)
						break
					}
				}
			}
			for _, t := range tasks {
				if t.Status != models.TaskStatusReady {
					continue
				}
				if running && t.TaskNumber == current {
					continue // already listed as the in-flight task
				}
				out = append(out, t)
			}
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			return out, nil
		},

		Retry: func(ctx context.Context, projectID string, taskNumber int) error {
			m, err := mappedProjectByID(scope, deps, projectID)
			if err != nil {
				return err
			}
			t, err := deps.GetTask(m.path, taskNumber)
			if err != nil {
				return err
			}
			if t.IsDeleted() {
				return fmt.Errorf("task #%04d is in the trash — restore it first", taskNumber)
			}
			if current, running := deps.AgentTaskNumber(projectID); running && current == taskNumber {
				return fmt.Errorf("task #%04d is currently being worked — cancel it first", taskNumber)
			}
			if t.Status == models.TaskStatusReady {
				return nil // already queued — idempotent success
			}
			// Route through the interactive bulk set-status path so the
			// done → ready transition clears success / failure_reason /
			// completed_at exactly like a retry from the TUI/GUI.
			_, err = deps.SetTaskStatus(m.path, []int{taskNumber}, string(models.TaskStatusReady))
			return err
		},

		Cancel: func(ctx context.Context, projectID string, taskNumber int, reason string) error {
			m, err := mappedProjectByID(scope, deps, projectID)
			if err != nil {
				return err
			}
			t, err := deps.GetTask(m.path, taskNumber)
			if err != nil {
				return err
			}
			if t.IsDeleted() {
				return fmt.Errorf("task #%04d is in the trash", taskNumber)
			}
			if t.Status == models.TaskStatusDone {
				return fmt.Errorf("task #%04d is already done", taskNumber)
			}
			if reason == "" {
				reason = defaultCancelReason(scope)
			}
			// Stop the agent first (user-stop semantics: no chaining),
			// then mark the task. Ordering matters — marking done first
			// would let the watcher's StopAgentForTask path stop the
			// agent without the user-stop flag and chain into the next
			// ready task, which is not what a cancel means.
			if current, running := deps.AgentTaskNumber(projectID); running && current == taskNumber {
				if stopErr := deps.StopAgentByUser(projectID); stopErr != nil {
					return fmt.Errorf("stop agent: %w", stopErr)
				}
			}
			t.MarkDone(false, reason)
			return deps.SaveTask(m.path, t)
		},
	}
}

// defaultCancelReason fills the failure_reason when the transport
// couldn't collect one (Discord slash command, Slack button without the
// reason modal).
func defaultCancelReason(scope commandScope) string {
	switch {
	case scope.TeamID != "":
		return "cancelled via Slack"
	case scope.GuildID != "":
		return "cancelled via Discord"
	case scope.Telegram:
		return "cancelled via Telegram"
	default:
		return "cancelled via chat command"
	}
}

// resolveMappedProjects returns the registered, active projects visible
// to the calling guild / team.
//
// Discord (scope.GuildID set) — a project is visible when either:
//   - its `project.yaml` carries `integrations.discord_guild_id`
//     matching the calling guild (per-project binding), or
//   - a Discord endpoint in integrations.yaml carries the matching
//     `guild_id`; endpoints cover every project except the ids in
//     their `project_mute_ids` list.
//
// Slack (scope.TeamID set) — Slack outbound endpoints carry no team
// scoping (one signing secret authenticates exactly one workspace), so:
//   - when the OAuth-connected workspace is recorded
//     (`inbound.slack_team_id`), the calling team must match it; on a
//     match every active project is visible, on a mismatch none are —
//     a request signed for a different workspace never sees projects.
//   - when no workspace is recorded (signing secret only), projects
//     opt in individually via the per-project `integrations.slack_channel`
//     binding.
//
// Telegram (scope.Telegram set) — every active project is visible.
// Telegram chats reach the router only after redeeming a one-time
// pairing code minted on the daemon's own machine, which binds the
// chat to the daemon owner; there is no guild/team to narrow by.
//
// Archived projects are never visible. A project whose YAML fails to
// load is skipped with a WARN — one broken project.yaml shouldn't take
// chat commands down for the rest of the fleet.
func resolveMappedProjects(scope commandScope, deps commandContextDeps) ([]mappedProject, error) {
	if scope.GuildID == "" && scope.TeamID == "" && !scope.Telegram {
		return nil, nil
	}
	cfg, err := deps.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}
	index, err := deps.LoadProjectsIndex()
	if err != nil {
		return nil, fmt.Errorf("load projects index: %w", err)
	}

	// Discord: collect the mute sets of every endpoint bound to the
	// calling guild. guildEndpoint stays false when no endpoint matches.
	guildEndpoint := false
	guildMuted := map[string]bool{}
	if scope.GuildID != "" {
		for _, ep := range cfg.Discord {
			if ep.GuildID != scope.GuildID {
				continue
			}
			guildEndpoint = true
			for _, id := range ep.ProjectMuteIDs {
				guildMuted[id] = true
			}
		}
	}

	// Slack: workspace gate. See doc comment above.
	slackTeamKnown := cfg.Inbound.SlackTeamID != ""
	slackTeamMatches := slackTeamKnown && cfg.Inbound.SlackTeamID == scope.TeamID

	var mapped []mappedProject
	for _, entry := range index.Projects {
		proj, loadErr := deps.LoadProject(entry.Path)
		if loadErr != nil || proj == nil {
			log.Printf("WARN: echo: command routing skipped project %s (%s): %v", entry.Name, entry.ProjectID, loadErr)
			continue
		}
		if proj.Status == "archived" {
			continue
		}

		visible := false
		switch {
		case scope.Telegram:
			visible = true
		case scope.GuildID != "":
			if proj.Integrations.DiscordGuildID == scope.GuildID {
				visible = true
			} else if guildEndpoint && !guildMuted[entry.ProjectID] {
				visible = true
			}
		case scope.TeamID != "":
			if slackTeamKnown {
				visible = slackTeamMatches
			} else {
				visible = proj.Integrations.SlackChannel != ""
			}
		}
		if !visible {
			continue
		}

		name := proj.Name
		if name == "" {
			name = entry.Name
		}
		mapped = append(mapped, mappedProject{
			info: echo.ProjectInfo{ID: entry.ProjectID, Name: name, Color: proj.Color},
			path: entry.Path,
		})
	}
	return mapped, nil
}

// mappedProjectByID re-resolves the scope's project mapping and picks
// the requested project out of it. Callers get a hard error when the
// project isn't visible to the calling guild / team — the router only
// hands out project ids from FindProjects, so a miss here means either
// a stale button value or a crafted request; both should refuse.
func mappedProjectByID(scope commandScope, deps commandContextDeps, projectID string) (mappedProject, error) {
	mapped, err := resolveMappedProjects(scope, deps)
	if err != nil {
		return mappedProject{}, err
	}
	for _, m := range mapped {
		if m.info.ID == projectID {
			return m, nil
		}
	}
	return mappedProject{}, fmt.Errorf("project %s is not mapped to this workspace", projectID)
}
