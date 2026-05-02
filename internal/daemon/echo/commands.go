package echo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// CommandResponse is the transport-agnostic shape every slash command
// returns. Slack renders it as Block Kit JSON; Discord renders it as
// a `type: 4` interaction response with embeds. The shape keeps the
// router free of provider-specific concerns:
//
//   - Text       — fallback / one-line summary, used when blocks are absent
//                  or by clients that don't render rich content.
//   - Blocks     — structured content in a Slack-Block-Kit-compatible
//                  shape. The Discord renderer translates them to embeds
//                  (see `discord_render.go`).
//   - Ephemeral  — true means "show only to the invoking user". Slack
//                  maps to `response_type: "ephemeral"`; Discord maps to
//                  the EPHEMERAL flag (64).
//   - InChannel  — a hint independent of Ephemeral: "this should be
//                  publicly visible to others in the channel". When both
//                  are false the renderer's transport default applies.
//                  When InChannel is true the Slack renderer emits
//                  `response_type: "in_channel"`.
type CommandResponse struct {
	Text      string
	Blocks    []Block
	Ephemeral bool
	InChannel bool
}

// Block is a minimal Slack-Block-Kit-compatible block. Only the
// shapes Watchfire emits are supported; Slack's full Block Kit is
// far richer, but the slash-command surface only needs these four:
//
//   - "header"   — a single short title line (Slack `header` block).
//   - "section"  — body text, optionally rendered as Markdown.
//   - "context"  — a small footer-like row (Slack `context` block).
//   - "divider"  — a separator (Slack `divider` block).
//
// The Discord renderer translates header → embed title, section →
// embed description, context → embed footer, and ignores dividers
// (Discord embeds have no equivalent — successive embeds already
// visually separate themselves).
type Block struct {
	Type     string
	Text     string // section / header / context body
	Markdown bool   // section: render as mrkdwn
}

// CommandContext carries the information the router needs to map the
// invoking guild / team to one or more Watchfire projects, plus the
// task lifecycle helpers it ultimately calls. The four function
// fields are all that the router uses — concrete implementations are
// injected by the daemon at startup, mocks are injected by tests.
//
// Keeping these as injected callbacks avoids dragging the entire
// task / project / agent manager graph into the echo package, which
// would otherwise force every test to spin up the full daemon.
type CommandContext struct {
	// GuildID / TeamID identify the calling Discord guild or Slack
	// workspace. Exactly one is set per request; the other is empty.
	GuildID string
	TeamID  string

	// UserID identifies the slash-command invoker. Logged for
	// auditability; not currently used for authorization.
	UserID string

	// Now returns the wall clock; tests inject a deterministic value.
	Now func() time.Time

	// FindProjects returns the subset of registered projects mapped
	// to the calling guild / team. The daemon's implementation
	// iterates `IntegrationsConfig.Discord` (or `.Slack`) for matching
	// `GuildID` / `TeamID` and returns the project IDs they cover.
	// If the empty slice comes back, the router renders a "no
	// projects mapped" reply.
	FindProjects func(ctx context.Context) ([]ProjectInfo, error)

	// LookupTask finds a task across the mapped projects by either
	// its task_id (8-char alphanumeric) or its task_number (the
	// "5" in "/watchfire retry 5"). Returns ErrTaskNotFound on miss.
	LookupTask func(ctx context.Context, taskRef string) (*models.Task, ProjectInfo, error)

	// ListTopActiveTasks returns up to `limit` of the highest-priority
	// in-flight tasks for the given project (status `ready` /
	// in-progress). Used by the `status` command.
	ListTopActiveTasks func(ctx context.Context, projectID string, limit int) ([]*models.Task, error)

	// Retry flips a `done` task back to `ready`. Implemented by the
	// daemon's task lifecycle helpers (see `task.Retry`); the router
	// just dispatches.
	Retry func(ctx context.Context, projectID string, taskNumber int) error

	// Cancel terminates an in-flight task (sends StopAgent to the
	// agent manager) or marks a `ready` task as cancelled.
	Cancel func(ctx context.Context, projectID string, taskNumber int) error
}

// ProjectInfo is the minimum project metadata the router needs for
// rendering. The daemon's FindProjects implementation populates it
// from the projects index; tests populate it inline.
type ProjectInfo struct {
	ID    string
	Name  string
	Color string
}

// ErrTaskNotFound is returned by `LookupTask` when no task matches
// the user-provided reference. Surfaced as a friendly ephemeral
// reply rather than an HTTP error.
var ErrTaskNotFound = fmt.Errorf("task not found")

// Route dispatches a slash command to the right handler. cmd is the
// raw command including the leading slash (`/watchfire`); subcmd is
// the first word of the argument string (`status`, `retry`,
// `cancel`); rest is everything after that. The split is done here
// (rather than at the transport layer) so Slack's "command + text"
// model and Discord's "name + options[]" model both feed into the
// same router after a tiny shim.
func Route(ctx context.Context, cmd, subcmd, rest string, cc CommandContext) *CommandResponse {
	if cc.Now == nil {
		cc.Now = time.Now
	}
	switch strings.ToLower(strings.TrimSpace(subcmd)) {
	case "status", "":
		return routeStatus(ctx, rest, cc)
	case "retry":
		return routeRetry(ctx, rest, cc)
	case "cancel":
		return routeCancel(ctx, rest, cc)
	default:
		return helpResponse(subcmd)
	}
}

func helpResponse(unknown string) *CommandResponse {
	header := "Watchfire commands"
	if unknown != "" {
		header = fmt.Sprintf("Unknown command: %q", unknown)
	}
	return &CommandResponse{
		Ephemeral: true,
		Blocks: []Block{
			{Type: "header", Text: header},
			{Type: "section", Markdown: true, Text: strings.Join([]string{
				"`/watchfire status [project]` — show in-flight tasks",
				"`/watchfire retry <task>` — re-queue a failed task",
				"`/watchfire cancel <task>` — cancel a running task",
			}, "\n")},
		},
	}
}

func routeStatus(ctx context.Context, rest string, cc CommandContext) *CommandResponse {
	projects, err := cc.FindProjects(ctx)
	if err != nil {
		return errorResponse(fmt.Sprintf("Failed to load projects: %v", err))
	}
	if len(projects) == 0 {
		return &CommandResponse{
			Ephemeral: true,
			Text:      "No Watchfire projects are mapped to this channel — open Settings → Integrations to wire one up.",
			Blocks: []Block{
				{Type: "section", Text: "No Watchfire projects are mapped to this channel."},
				{Type: "context", Text: "Open Settings → Integrations to wire one up."},
			},
		}
	}

	filter := strings.TrimSpace(rest)
	blocks := []Block{{Type: "header", Text: "Watchfire — current status"}}
	rendered := 0
	for _, p := range projects {
		if filter != "" && !strings.EqualFold(filter, p.Name) && !strings.EqualFold(filter, p.ID) {
			continue
		}
		tasks, listErr := cc.ListTopActiveTasks(ctx, p.ID, 3)
		if listErr != nil {
			blocks = append(blocks,
				Block{Type: "section", Markdown: true, Text: fmt.Sprintf("*%s* — failed to load tasks: %v", p.Name, listErr)},
				Block{Type: "divider"},
			)
			rendered++
			continue
		}
		blocks = append(blocks, Block{
			Type:     "section",
			Markdown: true,
			Text:     fmt.Sprintf("*%s* — %d active task(s)", p.Name, len(tasks)),
		})
		for _, t := range tasks {
			elapsed := "—"
			if t.StartedAt != nil {
				elapsed = formatDuration(cc.Now().Sub(*t.StartedAt))
			}
			blocks = append(blocks, Block{
				Type:     "section",
				Markdown: true,
				Text:     fmt.Sprintf("• `#%04d` %s _(elapsed %s)_", t.TaskNumber, t.Title, elapsed),
			})
		}
		blocks = append(blocks, Block{Type: "divider"})
		rendered++
	}
	if rendered == 0 {
		return &CommandResponse{
			Ephemeral: true,
			Text:      fmt.Sprintf("No project matched filter %q.", filter),
			Blocks: []Block{
				{Type: "section", Text: fmt.Sprintf("No project matched filter %q.", filter)},
			},
		}
	}
	blocks = append(blocks, Block{Type: "context", Text: fmt.Sprintf("As of %s", cc.Now().UTC().Format(time.RFC3339))})
	return &CommandResponse{
		Ephemeral: true,
		Blocks:    blocks,
	}
}

func routeRetry(ctx context.Context, rest string, cc CommandContext) *CommandResponse {
	taskRef := strings.TrimSpace(rest)
	if taskRef == "" {
		return errorResponse("Usage: `/watchfire retry <task-id-or-number>`")
	}
	task, project, err := cc.LookupTask(ctx, taskRef)
	if err != nil {
		if isNotFound(err) {
			return errorResponse(fmt.Sprintf("Task %q not found in mapped projects.", taskRef))
		}
		return errorResponse(fmt.Sprintf("Failed to find task: %v", err))
	}
	if err := cc.Retry(ctx, project.ID, task.TaskNumber); err != nil {
		return errorResponse(fmt.Sprintf("Retry failed: %v", err))
	}
	return &CommandResponse{
		InChannel: true,
		Text:      fmt.Sprintf("✅ Retrying task #%04d — %s (%s)", task.TaskNumber, task.Title, project.Name),
		Blocks: []Block{
			{Type: "section", Markdown: true, Text: fmt.Sprintf("✅ *Retrying task #%04d* — %s\n_%s_", task.TaskNumber, task.Title, project.Name)},
		},
	}
}

func routeCancel(ctx context.Context, rest string, cc CommandContext) *CommandResponse {
	taskRef := strings.TrimSpace(rest)
	if taskRef == "" {
		return errorResponse("Usage: `/watchfire cancel <task-id-or-number>`")
	}
	task, project, err := cc.LookupTask(ctx, taskRef)
	if err != nil {
		if isNotFound(err) {
			return errorResponse(fmt.Sprintf("Task %q not found in mapped projects.", taskRef))
		}
		return errorResponse(fmt.Sprintf("Failed to find task: %v", err))
	}
	if err := cc.Cancel(ctx, project.ID, task.TaskNumber); err != nil {
		return errorResponse(fmt.Sprintf("Cancel failed: %v", err))
	}
	return &CommandResponse{
		InChannel: true,
		Text:      fmt.Sprintf("⏹️ Cancelled task #%04d — %s (%s)", task.TaskNumber, task.Title, project.Name),
		Blocks: []Block{
			{Type: "section", Markdown: true, Text: fmt.Sprintf("⏹️ *Cancelled task #%04d* — %s\n_%s_", task.TaskNumber, task.Title, project.Name)},
		},
	}
}

func errorResponse(msg string) *CommandResponse {
	return &CommandResponse{
		Ephemeral: true,
		Text:      msg,
		Blocks: []Block{
			{Type: "section", Text: msg},
		},
	}
}

func isNotFound(err error) bool {
	return err == ErrTaskNotFound || (err != nil && strings.Contains(err.Error(), ErrTaskNotFound.Error()))
}

// ParseTaskRef tries the user input as a task_number first, then
// falls back to a task_id string. Returns ok=false on the empty
// string. Provider handlers can call this to short-circuit a numeric
// argument without round-tripping through the resolver.
func ParseTaskRef(s string) (number int, id string, ok bool) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "#"))
	if s == "" {
		return 0, "", false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, "", true
	}
	return 0, s, true
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
