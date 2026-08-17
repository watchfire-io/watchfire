// Paired-chat command dispatch (v10.0 Torch, task 0137). Read-only
// surface: /projects /use /status /tasks /help, plus the inline-button
// tap that mirrors /use. Everything routes through the echo command
// callbacks (the same production implementations Slack and Discord
// use) — /status in particular reuses echo.Route verbatim. Run-control
// verbs (/run /say …) arrive in 0142; until then /help advertises them
// as "(soon)" and the dispatcher answers them with the same hint.
package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
)

// tasksLimit caps the /tasks listing.
const tasksLimit = 10

// callbackUsePrefix namespaces inline-button callback data so future
// keyboards (0142 run controls) can share the callback pipe.
const callbackUsePrefix = "use:"

// soonCommands are the 0142 run-control verbs — advertised in /help,
// answered with a hint instead of "unknown" when tried early.
var soonCommands = map[string]bool{
	"/run": true, "/runall": true, "/retry": true, "/cancel": true,
	"/screen": true, "/say": true, "/watch": true, "/mute": true, "/unmute": true,
}

// botCommands is the set registered via setMyCommands for Telegram's
// autocomplete menu. Only live commands — advertising the 0142 verbs
// in autocomplete before they work would be a lie the client caches.
func botCommands() []telegrambot.BotCommand {
	return []telegrambot.BotCommand{
		{Command: "projects", Description: "List registered projects"},
		{Command: "use", Description: "Select the active project for this chat"},
		{Command: "status", Description: "Status of the active project"},
		{Command: "tasks", Description: "Top active tasks of the active project"},
		{Command: "help", Description: "Show available commands"},
		{Command: "pair", Description: "Pair this chat with a one-time code"},
	}
}

// dispatchCommand routes one slash command from a paired chat. rest is
// the whitespace-normalized remainder of the message after the command.
func (b *Bridge) dispatchCommand(ctx context.Context, msg *telegrambot.Message, cmd, rest string) {
	chatID, userID := msg.Chat.ID, msg.From.ID
	switch cmd {
	case "/projects":
		b.cmdProjects(ctx, chatID, userID)
	case "/use":
		b.cmdUse(ctx, chatID, userID, rest)
	case "/status":
		b.cmdStatus(ctx, chatID, userID)
	case "/tasks":
		b.cmdTasks(ctx, chatID, userID)
	case "/help":
		b.reply(ctx, chatID, helpHTML())
	default:
		if soonCommands[cmd] {
			b.reply(ctx, chatID, EscapeHTML(cmd)+" isn't available yet — coming soon. Send /help for what works today.")
			return
		}
		b.reply(ctx, chatID, "Unknown command "+EscapeHTML(cmd)+" — send /help for the list.")
	}
}

func helpHTML() string {
	return strings.Join([]string{
		"<b>Watchfire commands</b>",
		"/projects — list registered projects",
		"/use &lt;name|number&gt; — select the active project for this chat",
		"/status — status of the active project",
		"/tasks — top active tasks of the active project",
		"/pair &lt;code&gt; — pair this chat with a one-time code",
		"/help — this list",
		"",
		"<i>/run &lt;n&gt;, /runall — start tasks (soon)</i>",
		"<i>/retry &lt;n&gt;, /cancel &lt;n&gt; — task lifecycle (soon)</i>",
		"<i>/screen — live session snapshot (soon)</i>",
		"<i>/say &lt;text&gt; — send text to the running agent (soon)</i>",
		"<i>/watch on|off, /mute, /unmute — live relay controls (soon)</i>",
	}, "\n")
}

// commandContext resolves the chat's scoped CommandContext, replying
// with a shrug when the bridge was built without a factory (never the
// case in production wiring).
func (b *Bridge) commandContext(ctx context.Context, chatID, userID int64) (echo.CommandContext, bool) {
	if b.cmdCtxFor == nil {
		b.reply(ctx, chatID, "Commands are not wired up on this daemon.")
		return echo.CommandContext{}, false
	}
	return b.cmdCtxFor(chatID, userID), true
}

// cmdProjects renders the numbered project list with one inline button
// per project, and remembers the ordering so "/use 2" can refer to it.
func (b *Bridge) cmdProjects(ctx context.Context, chatID, userID int64) {
	cc, ok := b.commandContext(ctx, chatID, userID)
	if !ok {
		return
	}
	projects, err := cc.FindProjects(ctx)
	if err != nil {
		b.reply(ctx, chatID, "Failed to load projects: "+EscapeHTML(err.Error()))
		return
	}
	if len(projects) == 0 {
		b.reply(ctx, chatID, "No projects are registered on this daemon yet.")
		return
	}

	b.mu.Lock()
	b.lastProjects[chatID] = append([]echo.ProjectInfo(nil), projects...)
	current := b.paired[chatID].DefaultProjectID
	b.mu.Unlock()

	lines := []string{"<b>Projects</b>"}
	keyboard := make([][]telegrambot.InlineKeyboardButton, 0, len(projects))
	for i, p := range projects {
		marker := ""
		if p.ID == current {
			marker = " ✓"
		}
		lines = append(lines, fmt.Sprintf("%d. %s %s%s", i+1, projectGlyph(p), EscapeHTML(p.Name), marker))
		keyboard = append(keyboard, []telegrambot.InlineKeyboardButton{
			{Text: p.Name, CallbackData: callbackUsePrefix + p.ID},
		})
	}
	lines = append(lines, "", "<i>Tap a project or send /use &lt;name|number&gt;.</i>")
	if _, err := b.client.SendMessageWithKeyboard(ctx, b.token, chatID, strings.Join(lines, "\n"), keyboard); err != nil {
		b.logger.Printf("WARN: telegram bridge: sendMessage to %d failed: %v", chatID, err)
	}
}

// cmdUse resolves <name|number> against the registered projects and
// persists the chat's selection.
func (b *Bridge) cmdUse(ctx context.Context, chatID, userID int64, arg string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		b.reply(ctx, chatID, "Usage: /use &lt;name|number&gt; — send /projects for the list.")
		return
	}
	cc, ok := b.commandContext(ctx, chatID, userID)
	if !ok {
		return
	}
	projects, err := cc.FindProjects(ctx)
	if err != nil {
		b.reply(ctx, chatID, "Failed to load projects: "+EscapeHTML(err.Error()))
		return
	}
	b.mu.Lock()
	last := b.lastProjects[chatID]
	b.mu.Unlock()

	target, errMsg := pickProject(projects, last, arg)
	if errMsg != "" {
		b.reply(ctx, chatID, errMsg)
		return
	}
	if err := b.setDefaultProject(chatID, target.ID); err != nil {
		b.logger.Printf("ERROR: telegram bridge: persist default project for chat %d: %v", chatID, err)
		b.reply(ctx, chatID, "Failed to save the selection — please try again.")
		return
	}
	b.reply(ctx, chatID, "✓ Active project set to <b>"+EscapeHTML(target.Name)+"</b>.")
}

// handleCallback services an inline-keyboard tap. Only the "use:"
// namespace exists today. Unpaired or malformed callbacks are answered
// blank (the client stops its spinner) and otherwise ignored — same
// no-information-leak posture as unpaired messages.
func (b *Bridge) handleCallback(ctx context.Context, cq *telegrambot.CallbackQuery) {
	answer := func(text string) {
		if err := b.client.AnswerCallbackQuery(ctx, b.token, cq.ID, text); err != nil {
			b.logger.Printf("WARN: telegram bridge: answerCallbackQuery failed: %v", err)
		}
	}
	if cq.Message == nil { // message too old for Telegram to reference
		answer("")
		return
	}
	chatID := cq.Message.Chat.ID
	if !b.IsPaired(chatID) || !strings.HasPrefix(cq.Data, callbackUsePrefix) {
		answer("")
		return
	}
	projectID := strings.TrimPrefix(cq.Data, callbackUsePrefix)
	cc, ok := b.commandContext(ctx, chatID, cq.From.ID)
	if !ok {
		answer("")
		return
	}
	projects, err := cc.FindProjects(ctx)
	if err != nil {
		answer("Failed to load projects")
		return
	}
	var target *echo.ProjectInfo
	for i := range projects {
		if projects[i].ID == projectID {
			target = &projects[i]
			break
		}
	}
	if target == nil {
		answer("That project no longer exists — send /projects for a fresh list")
		return
	}
	if err := b.setDefaultProject(chatID, target.ID); err != nil {
		b.logger.Printf("ERROR: telegram bridge: persist default project for chat %d: %v", chatID, err)
		answer("Failed to save the selection")
		return
	}
	answer("Active project: " + target.Name)
	b.reply(ctx, chatID, "✓ Active project set to <b>"+EscapeHTML(target.Name)+"</b>.")
}

// cmdStatus routes /status through echo.Route for the chat's active
// project — the same status handler Slack and Discord use.
func (b *Bridge) cmdStatus(ctx context.Context, chatID, userID int64) {
	cc, projectID, ok := b.activeProject(ctx, chatID, userID)
	if !ok {
		return
	}
	resp := echo.Route(ctx, "/watchfire", "status", projectID, cc)
	for _, chunk := range RenderHTML(resp) {
		b.reply(ctx, chatID, chunk)
	}
}

// cmdTasks lists the top active tasks of the chat's active project.
func (b *Bridge) cmdTasks(ctx context.Context, chatID, userID int64) {
	cc, projectID, ok := b.activeProject(ctx, chatID, userID)
	if !ok {
		return
	}
	projects, err := cc.FindProjects(ctx)
	if err != nil {
		b.reply(ctx, chatID, "Failed to load projects: "+EscapeHTML(err.Error()))
		return
	}
	var info *echo.ProjectInfo
	for i := range projects {
		if projects[i].ID == projectID {
			info = &projects[i]
			break
		}
	}
	if info == nil {
		b.reply(ctx, chatID, "Your selected project is gone — send /projects and pick a new one with /use.")
		return
	}
	tasks, err := cc.ListTopActiveTasks(ctx, projectID, tasksLimit)
	if err != nil {
		b.reply(ctx, chatID, "Failed to list tasks: "+EscapeHTML(err.Error()))
		return
	}
	if len(tasks) == 0 {
		b.reply(ctx, chatID, "No active tasks in <b>"+EscapeHTML(info.Name)+"</b>.")
		return
	}
	lines := []string{"<b>" + EscapeHTML(info.Name) + " — active tasks</b>"}
	for _, t := range tasks {
		glyph := "🟡" // queued (ready)
		if info.AgentRunning && info.AgentTaskNumber == t.TaskNumber {
			glyph = "🟢" // the agent is on it right now
		}
		lines = append(lines, fmt.Sprintf("%s #%04d %s", glyph, t.TaskNumber, EscapeHTML(t.Title)))
	}
	for _, chunk := range chunkLines(strings.Join(lines, "\n")) {
		b.reply(ctx, chatID, chunk)
	}
}

// activeProject resolves the chat's CommandContext and selected
// project, prompting for /use when none is selected yet.
func (b *Bridge) activeProject(ctx context.Context, chatID, userID int64) (echo.CommandContext, string, bool) {
	cc, ok := b.commandContext(ctx, chatID, userID)
	if !ok {
		return echo.CommandContext{}, "", false
	}
	b.mu.Lock()
	projectID := b.paired[chatID].DefaultProjectID
	b.mu.Unlock()
	if projectID == "" {
		b.reply(ctx, chatID, "No project selected yet — send /projects, then /use &lt;name|number&gt;.")
		return echo.CommandContext{}, "", false
	}
	return cc, projectID, true
}

// setDefaultProject persists the chat's selection and mirrors it into
// the in-memory allowlist snapshot.
func (b *Bridge) setDefaultProject(chatID int64, projectID string) error {
	if err := b.setDefaultFn(chatID, projectID); err != nil {
		return err
	}
	b.mu.Lock()
	if chat, ok := b.paired[chatID]; ok {
		chat.DefaultProjectID = projectID
		b.paired[chatID] = chat
	}
	b.mu.Unlock()
	return nil
}

// pickProject resolves a /use argument against the registered
// projects. A number indexes the chat's last /projects listing (or,
// when none was printed yet, the live list — same ordering). A name
// matches fuzzily: exact (case-insensitive, also against the id),
// then unique prefix, then unique substring. Returns a user-facing
// HTML error message when nothing (or too much) matches.
func pickProject(projects, last []echo.ProjectInfo, arg string) (echo.ProjectInfo, string) {
	if n, err := strconv.Atoi(arg); err == nil {
		list := last
		if len(list) == 0 {
			list = projects
		}
		if n < 1 || n > len(list) {
			return echo.ProjectInfo{}, fmt.Sprintf("No project number %d — send /projects for the current list.", n)
		}
		picked := list[n-1]
		// The remembered list can be stale (project deleted since).
		for _, p := range projects {
			if p.ID == picked.ID {
				return p, ""
			}
		}
		return echo.ProjectInfo{}, "That list is stale — send /projects again."
	}

	var exact, prefix, substr []echo.ProjectInfo
	for _, p := range projects {
		name := strings.ToLower(p.Name)
		needle := strings.ToLower(arg)
		switch {
		case name == needle || strings.EqualFold(p.ID, arg):
			exact = append(exact, p)
		case strings.HasPrefix(name, needle):
			prefix = append(prefix, p)
		case strings.Contains(name, needle):
			substr = append(substr, p)
		}
	}
	for _, candidates := range [][]echo.ProjectInfo{exact, prefix, substr} {
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates[0], ""
		default:
			names := make([]string, 0, len(candidates))
			for _, p := range candidates {
				names = append(names, EscapeHTML(p.Name))
			}
			return echo.ProjectInfo{}, "Ambiguous — matches " + strings.Join(names, ", ") + ". Be more specific."
		}
	}
	return echo.ProjectInfo{}, "No project matches " + EscapeHTML(arg) + " — send /projects for the list."
}

// projectGlyph maps live agent state onto a list glyph.
func projectGlyph(p echo.ProjectInfo) string {
	if p.AgentRunning {
		return "🟢"
	}
	return "⚪"
}
