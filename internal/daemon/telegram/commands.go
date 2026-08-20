// Paired-chat command dispatch (v10.0 Torch, task 0137). The read
// surface — /projects /use /status /tasks /help, the inline-button tap
// that mirrors /use, and /watch (live conversation relay — task 0141,
// watch.go) — routes through the echo command callbacks (the same
// production implementations Slack and Discord use); /status reuses
// echo.Route verbatim. The run-control verbs (/run /runall /retry
// /cancel /screen /say /mute /unmute — task 0142) live in
// runcontrol.go and are dispatched here.
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

// botCommands is the canonical verb set registered via setMyCommands
// for Telegram's autocomplete menu — every command, minus the hidden
// aliases (/runall, /unmute) that only exist for muscle memory.
func botCommands() []telegrambot.BotCommand {
	return []telegrambot.BotCommand{
		{Command: "projects", Description: "List registered projects"},
		{Command: "use", Description: "Select the active project for this chat"},
		{Command: "status", Description: "Status of the active project, or 'all' for the fleet"},
		{Command: "tasks", Description: "Top active tasks of the active project"},
		{Command: "agent", Description: "Show or switch the project's agent backend"},
		{Command: "run", Description: "Start a task, or 'all' for every ready task"},
		{Command: "new", Description: "Start a fresh chat session (clears context)"},
		{Command: "wildfire", Description: "Start the autonomous loop (off to stop)"},
		{Command: "stop", Description: "Stop the running agent (chains end too)"},
		{Command: "generate", Description: "Generate the project definition from the codebase"},
		{Command: "plan", Description: "Generate tasks from the project definition"},
		{Command: "retry", Description: "Re-queue a failed task"},
		{Command: "cancel", Description: "Cancel a running or queued task"},
		{Command: "watch", Description: "Toggle the live conversation relay (on|off)"},
		{Command: "screen", Description: "Plain-text snapshot of the live session"},
		{Command: "say", Description: "Type into the working session explicitly"},
		{Command: "mute", Description: "Pause/resume event pushes (on|off)"},
		{Command: "pair", Description: "Pair this chat with a one-time code"},
		{Command: "help", Description: "All commands"},
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
		// "/status all" is the fleet view; bare /status stays the
		// active project's detail.
		if strings.EqualFold(strings.TrimSpace(rest), "all") {
			b.cmdStatusAll(ctx, chatID, userID)
			return
		}
		b.cmdStatus(ctx, chatID, userID)
	case "/tasks":
		b.cmdTasks(ctx, chatID, userID)
	case "/run":
		// "/run all" folds the old /runall in; the bare alias still works.
		if strings.EqualFold(strings.TrimSpace(rest), "all") {
			b.cmdRunAll(ctx, chatID)
			return
		}
		b.cmdRun(ctx, chatID, rest)
	case "/runall":
		b.cmdRunAll(ctx, chatID)
	case "/wildfire":
		b.cmdWildfire(ctx, chatID, rest)
	case "/new":
		b.cmdNew(ctx, chatID)
	case "/stop":
		b.cmdStop(ctx, chatID)
	case "/agent":
		b.cmdAgent(ctx, chatID, rest)
	case "/generate":
		// Closures, not method values: b.runner may be nil, and a method
		// value on a nil interface panics at selection — cmdSimpleMode's
		// requireRunner guard must run first.
		b.cmdSimpleMode(ctx, chatID,
			func(ctx context.Context, pid string) (RunStart, error) { return b.runner.StartGenerate(ctx, pid) },
			"📝 Generating the project definition from the codebase — the session streams here while watch is on.")
	case "/plan":
		b.cmdSimpleMode(ctx, chatID,
			func(ctx context.Context, pid string) (RunStart, error) { return b.runner.StartPlan(ctx, pid) },
			"🗺 Planning — generating tasks from the project definition. The session streams here while watch is on.")
	case "/retry":
		b.cmdRouteVerb(ctx, chatID, userID, "retry", rest)
	case "/cancel":
		b.cmdRouteVerb(ctx, chatID, userID, "cancel", rest)
	case "/screen":
		b.cmdScreen(ctx, chatID)
	case "/say":
		// /say gets the VERBATIM remainder of the original message —
		// the normalized rest would collapse the whitespace the user
		// meant to type into the session.
		b.cmdSay(ctx, chatID, sayVerbatim(msg.Text))
	case "/mute":
		// "/mute off" folds the old /unmute in; bare /mute mutes, and
		// the /unmute alias still works.
		switch strings.ToLower(strings.TrimSpace(rest)) {
		case "", "on":
			b.cmdMute(ctx, chatID, true)
		case "off":
			b.cmdMute(ctx, chatID, false)
		default:
			b.reply(ctx, chatID, "Usage: /mute on|off")
		}
	case "/unmute":
		b.cmdMute(ctx, chatID, false)
	case "/watch":
		b.cmdWatch(ctx, chatID, rest)
	case "/help":
		b.reply(ctx, chatID, helpHTML())
	default:
		b.reply(ctx, chatID, "Unknown command "+EscapeHTML(cmd)+" — send /help for the list.")
	}
}

func helpHTML() string {
	return strings.Join([]string{
		"<b>Watchfire commands</b>",
		"<i>Just type to talk to a chat agent — I'll start one if nothing is running.</i>",
		"",
		"<b>Project</b>",
		"/projects — list registered projects",
		"/use &lt;name|number&gt; — select the active project for this chat",
		"/status [all] — status of the active project, or the whole fleet",
		"/tasks — top active tasks of the active project",
		"/agent [name] — show or switch the project's agent backend",
		"",
		"<b>Run</b>",
		"/run &lt;n&gt;|all — start a task, or every ready task in sequence",
		"/new — fresh chat session (clears the conversation context)",
		"/wildfire — start the autonomous loop (milestones via watch; /wildfire off stops)",
		"/stop — stop whatever agent is running (a run-all/wildfire chain ends too)",
		"/generate — write the project definition from the codebase",
		"/plan — generate tasks from the project definition",
		"/retry &lt;n&gt; · /cancel &lt;n&gt; — re-queue a failed task / cancel one",
		"",
		"<b>Session</b>",
		"/watch on|off — relay the live agent conversation here",
		"/screen — plain-text snapshot of the live session",
		"/say &lt;text&gt; — type into the working session explicitly",
		"/mute on|off — pause/resume event pushes to this chat",
		"",
		"/pair &lt;code&gt; — pair this chat · /help — this list",
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
	projectID, ok := b.chatProject(ctx, chatID)
	if !ok {
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
// cmdStatusAll renders the fleet view: one line per project with its
// live session state (mode, wildfire phase, current task) resolved
// through the SessionSource so it matches what watch mode would relay.
func (b *Bridge) cmdStatusAll(ctx context.Context, chatID, userID int64) {
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
	current := b.paired[chatID].DefaultProjectID
	b.mu.Unlock()

	working := 0
	lines := make([]string, 0, len(projects)+2)
	for _, p := range projects {
		state := "idle"
		glyph := "⚪"
		if b.sessions != nil {
			if sess, live := b.sessions.ActiveSession(p.ID); live {
				working++
				glyph = "🟢"
				state = sessionStateLine(sess)
			}
		} else if p.AgentRunning {
			working++
			glyph = "🟢"
			state = "agent running"
		}
		marker := ""
		if p.ID == current {
			marker = " ✓"
		}
		lines = append(lines, fmt.Sprintf("%s <b>%s</b>%s — %s", glyph, EscapeHTML(p.Name), marker, state))
	}
	header := fmt.Sprintf("<b>Fleet</b> — %d project%s, %d working", len(projects), plural(len(projects)), working)
	b.reply(ctx, chatID, header+"\n"+strings.Join(lines, "\n"))
}

// sessionStateLine compresses one live session into a phone-width
// description: "wildfire (generate)", "task #0012 — Fix the flux…",
// "chat session", …
func sessionStateLine(sess *WatchedSession) string {
	switch {
	case sess.Mode == "wildfire":
		state := "wildfire"
		if sess.Phase != "" {
			state += " (" + EscapeHTML(sess.Phase) + ")"
		}
		if sess.TaskNumber > 0 {
			state += fmt.Sprintf(" · task #%04d", sess.TaskNumber)
		}
		return state
	case sess.TaskNumber > 0:
		state := fmt.Sprintf("task #%04d", sess.TaskNumber)
		if sess.TaskTitle != "" {
			state += " — " + EscapeHTML(firstLineTrunc(sess.TaskTitle, 40))
		}
		if sess.Mode == "start-all" {
			state = "run-all · " + state
		}
		return state
	case sess.Mode == "chat":
		return "chat session"
	default:
		return EscapeHTML(sess.Mode)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func projectGlyph(p echo.ProjectInfo) string {
	if p.AgentRunning {
		return "🟢"
	}
	return "⚪"
}
