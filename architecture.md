# Watchfire Architecture

## Summary

Watchfire orchestrates coding agent sessions (starting with Claude Code) based on task files. It manages multiple projects in parallel, with one active task per project. A daemon (`watchfired`) manages all logic: spawning agents in sandboxed PTYs, terminal emulation, git worktree workflows, and file watching. Thin clients (CLI/TUI and GUI) connect via gRPC to display task status and live terminal output.

## Components

| Component | Binary | Role | Tech |
|-----------|--------|------|------|
| **Daemon** | `watchfired` | Orchestration, PTY management, terminal emulation, git workflows, gRPC server, system tray | Go |
| **CLI/TUI** | `watchfire` | CLI commands + TUI mode. Project-scoped thin client | Go + Bubbletea |
| **GUI** | `Watchfire.app` | Multi-project thin client (shows all projects) | Electron |

## Non-Negotiable Tech Choices

- **Language**: Go for daemon and CLI/TUI. No exceptions.
- **PTY**: `github.com/creack/pty`
- **Terminal emulation**: `github.com/hinshun/vt10x` — parses escape codes, maintains screen buffer
- **gRPC**: `google.golang.org/grpc` with protobuf
- **gRPC-Web**: `github.com/improbable-eng/grpc-web` — for Electron GUI support
- **TUI framework**: `github.com/charmbracelet/bubbletea`
- **System tray**: `github.com/getlantern/systray`
- **File watching**: `github.com/fsnotify/fsnotify`
- **Sandbox**: macOS `sandbox-exec`

---

## Daemon (`watchfired`)

### Overview

The daemon is the backend brain of Watchfire. It manages multiple projects simultaneously, watches for file changes, spawns coding agents in sandboxed PTYs with terminal emulation, handles git worktree workflows, and serves state to thin clients over gRPC.

### Lifecycle

| Aspect | Behavior |
|--------|----------|
| **Development** | Run `watchfired` in foreground to see logs |
| **Production** | Runs in background, started automatically by CLI/TUI/GUI if not running |
| **Persistence** | Stays running when thin clients close |
| **Shutdown** | Ctrl+C (foreground), CLI command, or system tray quit. All thin clients close when daemon quits |

### Network

| Aspect | Decision |
|--------|----------|
| **Protocol** | gRPC + gRPC-Web (multiplexed on same port) |
| **Port** | Dynamic allocation (start with port 0, OS assigns free port) |
| **Discovery** | Connection info written to `~/.watchfire/daemon.yaml` for client discovery |
| **Clients** | CLI/TUI use native gRPC, Electron GUI uses gRPC-Web |

### Multi-Project Management

| Aspect | Behavior |
|--------|----------|
| **Projects index** | `~/.watchfire/projects.yaml` lists all registered projects |
| **Registration** | Projects added via CLI (`watchfire init`) or GUI |
| **Concurrency** | One active task per project, multiple projects in parallel |
| **Client tracking** | Tracks which clients are watching which projects |
| **Task cancellation** | Task stops only when ALL clients for that project disconnect |

### File Watching

| Aspect | Behavior |
|--------|----------|
| **Mechanism** | fsnotify with debouncing |
| **Robustness** | Handles create-then-rename pattern (common with AI tools) |
| **Global watched** | `~/.watchfire/projects.yaml` |
| **Per-project watched** | `.watchfire/project.yaml`, `.watchfire/tasks/*.yaml` |
| **Re-watch on chain** | When agents chain (wildfire/start-all), project is re-watched to pick up directories created during earlier phases |
| **Polling fallback** | Task-mode agents poll task YAML every 5s as safety net for missed watcher events (kqueue overflow, late directory creation) |
| **Reaction** | File changes trigger real-time updates to connected clients |

### Git Worktree Management

| Aspect | Behavior |
|--------|----------|
| **Creation** | Creates worktree in `.watchfire/worktrees/<task_number>/` when task starts |
| **Branch naming** | `watchfire/<task_number>` (e.g., `watchfire/0001`) |
| **Location** | Agent runs inside worktree, not main working tree |
| **Completion** | On task completion, daemon merges worktree to target branch, deletes worktree |
| **Stale branches** | If a branch already exists when creating a worktree, deletes it and recreates from current HEAD |
| **Merge conflict** | On merge failure, runs `git merge --abort` to restore clean working directory |
| **Chain stop** | Merge failure stops wildfire/start-all chaining (prevents cascading failures) |
| **Pruning** | Periodically detects and cleans orphaned worktrees |

### Coding Agent Abstraction

| Aspect | Behavior |
|--------|----------|
| **Abstraction** | Generic "coding agent" interface |
| **Initial agent** | Claude Code |
| **Future agents** | Designed to support others (Cursor, Aider, etc.) |
| **Agent config** | Agent-specific settings abstracted behind interface |

### Agent Sandbox & Permissions

| Aspect | Behavior |
|--------|----------|
| **Sandbox** | Agent process runs inside macOS `sandbox-exec` |
| **Sandbox profile** | Custom profile restricting filesystem/network access |
| **Profile storage** | Embedded in binary, not user-visible |
| **Agent permissions** | Agent runs in "yolo mode" — full permissions within sandbox |
| **Claude Code flag** | `--dangerously-skip-permissions` |
| **Security model** | Agent has free reign inside sandbox; sandbox limits blast radius |

### PTY & Terminal Emulation

```
sandbox-exec (macOS sandbox)
       ↓ contains
Coding Agent (e.g., Claude Code with --dangerously-skip-permissions)
       ↓ runs inside
     PTY (github.com/creack/pty)
       ↓ raw output with escape codes
   vt10x (github.com/hinshun/vt10x)
       ↓ parsed into
   Screen Buffer (rows × cols grid of cells with attributes)
       ↓ streamed via
     gRPC to clients
```

| Aspect | Behavior |
|--------|----------|
| **PTY creation** | `github.com/creack/pty` spawns agent process |
| **Terminal emulation** | `github.com/hinshun/vt10x` parses escape codes, maintains virtual screen |
| **Screen buffer format** | 2D grid of cells (char, fg, bg, bold, italic, underline, inverse) + cursor position |
| **Resize flow** | Client sends resize request → daemon resizes PTY → vt10x updates → agent receives SIGWINCH |
| **Streaming** | Screen buffer sent to clients on change (debounced) |

### Agent Spawning

| Aspect | Behavior |
|--------|----------|
| **Sandbox** | Agent wrapped in `sandbox-exec -f <profile>` (macOS) |
| **Sandbox profile** | Embedded in binary, not user-visible |
| **PTY** | Agent runs in PTY via `github.com/creack/pty` |
| **Terminal emulation** | Output parsed by `github.com/hinshun/vt10x` |
| **Working directory** | Task mode: worktree (`.watchfire/worktrees/<task_number>/`). Chat mode: project root. |
| **Modes** | With prompt (task mode) or without (chat mode) |
| **Yolo mode** | `--dangerously-skip-permissions` for Claude Code |
| **System prompt** | `--append-system-prompt "..."` with embedded text |
| **Prompt source** | Embedded in binary, not user-visible |
| **Resize** | PTY resized on client request, agent receives SIGWINCH |

**Example spawn command (Claude Code)**:
```bash
sandbox-exec -f <profile> claude --dangerously-skip-permissions --append-system-prompt "..." [--prompt "..."]
```

### Task Lifecycle (Reactive Model)

```
1. Client calls StartTask(task_id)
2. Daemon creates git worktree for task
3. Daemon spawns coding agent in sandboxed PTY (inside worktree)
4. Daemon streams screen buffer to subscribed clients
5. Agent updates task file when done (status: completed/failed)
6. Daemon detects via fsnotify OR polling fallback (5s interval)
7. Daemon kills agent (if still running)
8. Daemon processes git rules (merge, delete worktree)
   - If merge conflicts → abort merge, stop chain
9. Daemon starts next task (if queued and merge succeeded)
```

| Scenario | Behavior |
|----------|----------|
| **Agent updates task file** | Daemon reacts, processes git, moves to next task |
| **Agent crashes (PTY exits)** | Daemon detects, stops task |
| **Watcher misses event** | Polling fallback detects task done within 5s, stops agent |

### Phase Completion Signals

The daemon detects phase completion via signal files created by the agent. The daemon deletes these files after processing.

| Phase | Signal File | Agent Action | Daemon Response |
|-------|-------------|--------------|-----------------|
| **Task / Execute** | Task YAML `status: done` | Set status to done | Stop agent, merge worktree, start next |
| **Refine** | `.watchfire/refine_done.yaml` | Create empty file | Stop agent, start next phase |
| **Generate** | `.watchfire/generate_done.yaml` | Create empty file | Stop agent, check for new tasks or end wildfire |
| **Generate Definition** | `.watchfire/definition_done.yaml` | Create empty file | Stop agent (single-shot command) |
| **Generate Tasks** | `.watchfire/tasks_done.yaml` | Create empty file | Stop agent (single-shot command) |

### System Tray

| Menu Item | Content |
|-----------|---------|
| **Status header** | "Watchfire Daemon" |
| **Port** | "Running on port: {port}" |
| **Running agents** | List with project name, e.g., "● myproject — Claude Code" |
| **Separator** | — |
| **Open GUI** | Launches Electron GUI |
| **Quit** | Shuts down daemon (closes all thin clients) |

**Tooltip**: "Watchfire — {n} projects, {m} active agents"

### Crash Recovery

| Scenario | Behavior |
|----------|----------|
| **Daemon crashes mid-task** | On restart, user must manually restart task. Agent reads worktree to understand state. |
| **Agent crashes** | Daemon detects PTY exit, stops task |

---

## CLI/TUI (`watchfire`)

### Overview

The CLI/TUI is the primary interface for developers. A single binary (`watchfire`) operates in two modes: CLI commands for scripting/automation, and TUI mode for interactive work. It's project-scoped — run from within a project directory.

### Modes

| Mode | Entry | Description |
|------|-------|-------------|
| **TUI** | `watchfire` (no args) | Interactive split-view: task list + agent terminal |
| **CLI** | `watchfire <command>` | Scriptable commands, returns to shell |

### Daemon Interaction

| Scenario | Behavior |
|----------|----------|
| **Daemon not running** | CLI/TUI starts daemon automatically before proceeding |
| **Daemon shuts down** | CLI/TUI closes |
| **Multiple instances** | Can run multiple `watchfire` instances in different projects simultaneously |
| **Project not in index** | CLI auto-registers the project in `~/.watchfire/projects.yaml` on any project-scoped command |

### Commands

#### Global

| Command | Alias | Description |
|---------|-------|-------------|
| `watchfire` | | Start TUI |
| `watchfire version` | `-v` | Show version (all components) |
| `watchfire help` | `-h` | Show help |
| `watchfire update` | | Update all components (daemon, CLI/TUI, GUI) |
| `watchfire init` | | Initialize project in current directory |

#### Task

| Command | Alias | Description |
|---------|-------|-------------|
| `watchfire task list` | `task ls` | List tasks (excludes soft-deleted) |
| `watchfire task list-deleted` | `task ls-deleted` | List soft-deleted tasks |
| `watchfire task add` | | Add new task (interactive prompts) |
| `watchfire task <taskid>` | | Edit task (interactive TUI) |
| `watchfire task delete <taskid>` | `task rm <taskid>` | Soft delete task (sets deleted_at) |
| `watchfire task restore <taskid>` | | Restore soft-deleted task |

#### Project

| Command | Alias | Description |
|---------|-------|-------------|
| `watchfire definition` | `def` | Edit project definition (interactive) |
| `watchfire settings` | | Configure project settings (interactive) |

#### Daemon

| Command | Alias | Description |
|---------|-------|-------------|
| `watchfire daemon start` | | Start the daemon (no-op if already running) |
| `watchfire daemon status` | | Show daemon host, port, PID, uptime, and active agents |
| `watchfire daemon stop` | | Stop the daemon via SIGTERM |

#### Agent

| Command | Alias | Description |
|---------|-------|-------------|
| `watchfire agent start [taskid]` | | No taskid = chat mode. With taskid = marks ready + starts. Ctrl+C stops. |
| `watchfire agent start all` | | Run all ready tasks in sequence, exit when none left. |
| `watchfire agent generate definition` | `agent gen def` | One-shot: generate project definition (JSON output mode) |
| `watchfire agent generate tasks` | `agent gen tasks` | One-shot: generate tasks (JSON output mode) |
| `watchfire agent wildfire` | | Autonomous three-phase loop until no new tasks or Ctrl+C |

### `watchfire init` Flow

```
1. Check for git → if missing, initialize git repo
2. Create .watchfire/ directory structure
3. Create initial project.yaml (generated UUID, name from folder)
4. Append .watchfire/ to .gitignore (create if missing)
5. Commit .gitignore change
6. Prompt: "Project definition (optional):" → user enters text
7. Prompt: "Project settings:"
   - Auto-merge after task completion? (y/n)
   - Auto-delete worktrees after merge? (y/n)
   - Auto-start agent when task set to ready? (y/n)
   - Default branch? (default: main)
8. Save project.yaml
9. Register project in ~/.watchfire/projects.yaml
```

### TUI Layout

```
┌──────────────────────────────────────────────────────────────────────┐
│ ● project-name  Tasks | Definition | Settings    Chat | Logs  ● Idle│
├────────────────────────────────────────┬─────────────────────────────┤
│  Task List                             │  Agent Terminal / Logs      │
│                                        │                             │
│  Draft (2)                             │  > Starting session...      │
│    #0001 Setup project structure       │                             │
│    #0004 Add authentication            │  Claude Code v2.1.31        │
│                                        │  ~/source/my-project        │
│  Ready (1)                             │                             │
│  ● #0002 Implement login flow    [▶]   │  > Working on task...       │
│                                        │                             │
│  Done (3)                              │                             │
│    #0003 Create database schema  ✓     │                             │
│    #0005 Add unit tests          ✓     │                             │
│    #0006 Fix bug in auth         ✗     │                             │
│                                        │                             │
├────────────────────────────────────────┴─────────────────────────────┤
│ Ctrl+q quit  Ctrl+h help  Tab switch panel           a add  s start │
└──────────────────────────────────────────────────────────────────────┘
```

**Header bar:**

| Element | Position | Description |
|---------|----------|-------------|
| **Project dot** | Far left | Colored dot using `project.color` |
| **Project name** | After dot | Project name from `project.yaml` |
| **Left tabs** | Center-left | `Tasks`, `Definition`, `Settings` — active tab highlighted |
| **Right tabs** | Center-right | `Chat`, `Logs` — active tab highlighted |
| **Agent badge** | Far right | `● Idle`, `● Working`, `● Wildfire`, `⚠ Issue` |

**Status bar:**

| Element | Position | Description |
|---------|----------|-------------|
| **Key hints** | Left | Context-sensitive shortcuts for current focus/state |
| **Connection status** | Right | `Connected` or `⚠ Disconnected` (with reconnect indicator) |

**Panel divider:** Draggable with mouse to resize left/right split.

**Left panel tabs:**

| Tab | Content |
|-----|---------|
| **Tasks** | Task list grouped by status (Draft, Ready, Done) |
| **Definitions** | Markdown editor for project definition |
| **Settings** | Git config, automation toggles |

**Right panel tabs:**

| Tab | Content |
|-----|---------|
| **Chat** | Agent terminal (live stream from daemon) |
| **Logs** | Past session logs per task |

### TUI Navigation

| Input | Action |
|-------|--------|
| `Tab` | Switch between left/right panels |
| `j/k` or `↓/↑` | Move up/down in lists |
| `h/l` or `←/→` | Switch tabs |
| `1/2/3` | Switch left panel tabs (Tasks/Definition/Settings) |
| `Enter` | Select/edit item |
| `Esc` | Close overlay / cancel / go back |
| `Ctrl+s` | Save in overlay forms |
| `Mouse click` | Select item, switch panels/tabs |
| `Mouse scroll` | Scroll lists and terminal |
| `Ctrl+q` | Quit |
| `Ctrl+h` | Help overlay |

**Task actions (when task list focused):**

| Key | Action |
|-----|--------|
| `a` | Add new task |
| `e` / `Enter` | Edit selected task |
| `s` | Start task (move to Ready + start agent) |
| `r` | Move task to Ready |
| `t` | Move task to Draft |
| `d` | Mark done |
| `x` | Delete (soft) |

**Note:** Single-letter shortcuts only work when task list panel is focused. When agent terminal is focused, all input goes to the agent.

### TUI Features

- Mouse enabled (click, scroll)
- Vim-like + arrow key navigation
- Resizable panels
- Scroll support in all panels
- Real-time updates from daemon
- Type into agent terminal when Chat panel focused

### TUI Architecture

**Pattern:** Bubbletea Elm architecture (Model → Update → View).

| Concept | Role |
|---------|------|
| **Model** | Application state: tasks, agent status, terminal buffer, focus, overlays |
| **Update** | Processes messages (key events, mouse events, gRPC responses, window resize) → returns new model + commands |
| **View** | Renders model to string using lipgloss styling → Bubbletea writes to terminal |

**Component hierarchy:**

```
App (root model)
├── Header              — project dot, name, tab selectors, agent badge
├── LeftPanel
│   ├── TaskList        — grouped task list with status sections
│   ├── DefinitionView  — read-only viewport of project definition
│   └── SettingsForm    — editable project settings fields
├── RightPanel
│   ├── Terminal        — raw PTY output viewport
│   └── LogViewer       — log list + log content viewport
├── StatusBar           — key hints, connection status
└── Overlays
    ├── HelpOverlay     — keybinding reference
    ├── TaskAddForm     — title, prompt, criteria, status fields
    ├── TaskEditForm    — edit existing task fields
    └── ConfirmDialog   — inline y/n confirmation in status bar
```

**Message routing:** Global keybindings (Ctrl+q, Tab, Ctrl+h) are handled at root level first. If an overlay is active, it captures all remaining input. Otherwise, input routes to the focused panel.

**Custom message types:**

| Message | Source | Purpose |
|---------|--------|---------|
| `DaemonConnectedMsg` | Init command | Daemon gRPC connection established |
| `DaemonDisconnectedMsg` | Connection monitor | Lost connection to daemon |
| `ProjectLoadedMsg` | `GetProject` RPC | Project data loaded |
| `TasksLoadedMsg` | `ListTasks` RPC | Task list refreshed |
| `AgentStatusMsg` | `GetAgentStatus` RPC | Agent status polled |
| `ScreenUpdateMsg` | `SubscribeScreen` stream | Pre-rendered ANSI screen from agent |
| `AgentIssueMsg` | `SubscribeAgentIssues` stream | Auth/rate limit issue detected or cleared |
| `TaskSavedMsg` | `CreateTask`/`UpdateTask` RPC | Task save confirmed |
| `ProjectSavedMsg` | `UpdateProject` RPC | Settings/definition save confirmed |
| `LogsLoadedMsg` | `ListLogs` RPC | Log list loaded |
| `LogContentMsg` | `GetLog` RPC | Single log content loaded |
| `ErrorMsg` | Any RPC | gRPC error to display |

**Command pattern:** All gRPC calls are wrapped as `tea.Cmd` functions. Streaming RPCs (`SubscribeScreen`, `SubscribeAgentIssues`) run in goroutines that call `Program.Send()` to push messages back into the update loop.

### TUI State Management

| State | Source RPC | Cache Strategy |
|-------|-----------|----------------|
| **Project config** | `GetProject` | Load once on startup, refresh on `ProjectSavedMsg` |
| **Task list** | `ListTasks` | Load on startup, refresh after any task mutation |
| **Agent status** | `GetAgentStatus` | Poll every 2s while agent running |
| **Terminal output** | `SubscribeScreen` | Pre-rendered ANSI screen snapshots (viewport displays latest) |
| **Agent issues** | `SubscribeAgentIssues` | Latest issue held in model, cleared when issue resolves |
| **Logs list** | `ListLogs` | Load on demand when Logs tab selected |
| **Log content** | `GetLog` | Load on demand when log selected |

**Note:** The TUI does NOT use the `Subscribe` event stream. That stream is designed for the GUI's multi-project view. The TUI is project-scoped and uses targeted RPCs: `SubscribeScreen` for pre-rendered ANSI content, `SubscribeAgentIssues` for issue notifications, and polling `GetAgentStatus` for agent state.

### TUI gRPC Usage

Full mapping of TUI actions to gRPC RPCs:

| TUI Action | RPC | Service |
|------------|-----|---------|
| Load project | `GetProject` | ProjectService |
| Save definition | `UpdateProject` | ProjectService |
| Save settings | `UpdateProject` | ProjectService |
| List tasks | `ListTasks` | TaskService |
| Add task | `CreateTask` | TaskService |
| Edit task | `UpdateTask` | TaskService |
| Change task status | `UpdateTask` | TaskService |
| Delete task | `DeleteTask` | TaskService |
| Restore task | `RestoreTask` | TaskService |
| Start agent | `StartAgent` | AgentService |
| Stop agent | `StopAgent` | AgentService |
| Poll agent status | `GetAgentStatus` | AgentService |
| Stream terminal | `SubscribeScreen` | AgentService |
| Send keystrokes | `SendInput` | AgentService |
| Resize terminal | `Resize` | AgentService |
| Stream issues | `SubscribeAgentIssues` | AgentService |
| Clear issue | `ResumeAgent` | AgentService |
| List logs | `ListLogs` | LogService |
| View log | `GetLog` | LogService |
| Check daemon | `GetStatus` | DaemonService |

**Note:** The TUI uses `SubscribeScreen` (pre-rendered ANSI from vt10x), not `SubscribeRawOutput` (raw PTY bytes). Since Bubbletea's Elm architecture requires deterministic rendering via the `View()` method, raw bytes cannot be cleanly integrated — they would need a separate terminal emulator. Instead, the daemon's vt10x terminal emulates the PTY output and sends pre-rendered ANSI SGR text, which the TUI displays in a lipgloss viewport. `SubscribeRawOutput` is used by the CLI's direct-attach mode, where raw bytes can be written to stdout.

### TUI Terminal Panel

The right panel's Chat tab renders live agent output.

**Rendering approach:** Pre-rendered ANSI screen snapshots arrive via `SubscribeScreen`. The daemon's vt10x terminal emulator parses raw PTY output and renders the visible screen as ANSI SGR text. The TUI stores the latest snapshot and displays it in a lipgloss viewport, fitting naturally into Bubbletea's Elm update/view cycle.

| Aspect | Behavior |
|--------|----------|
| **Scrollback** | Viewport supports scrollback via mouse scroll and `PgUp`/`PgDn` when terminal focused |
| **Input mode** | When Chat panel focused, all keystrokes sent to agent via `SendInput` RPC |
| **Global shortcuts** | `Ctrl+q`, `Tab`, `Ctrl+h` intercepted before input reaches agent |
| **Resize** | Right panel size change → `Resize` RPC (debounced 100ms) → daemon resizes PTY → agent receives SIGWINCH |
| **Empty state** | "No agent running. Press `s` on a task to start." |
| **Agent exited** | "Agent stopped." with last output visible in scrollback |
| **Wildfire display** | Shows current phase badge above terminal: `Execute #0001`, `Refine`, `Generate` |

### TUI Task Status Display

| Internal Status | Display Label | Terminal Visual |
|-----------------|---------------|-----------------|
| `draft` | Draft | `[ ]` default style |
| `ready` (no agent) | Ready | `[R]` highlighted |
| `ready` (agent active) | Active | `[●]` animated spinner |
| `done` (success: true) | Done | `[✓]` green |
| `done` (success: false) | Failed | `[✗]` red |

**Task list item format:**
```
[●] #0002 Implement login flow
[R] #0007 Add search feature
[ ] #0001 Setup project structure
[✓] #0003 Create database schema
[✗] #0006 Fix bug in auth
```

Tasks are grouped by status section (Draft, Ready, Done) with section headers showing count.

### TUI Agent Issue Display

Agent issues (auth errors, rate limits) appear as colored banners above the terminal viewport in the right panel.

| Issue Type | Banner Color | Banner Text | Recovery Action |
|------------|-------------|-------------|-----------------|
| `auth_required` | Yellow | `⚠ Authentication required — switch to Chat and run /login` | User switches to terminal, runs `/login` in agent |
| `rate_limited` | Yellow | `⚠ Rate limited — resets at {time}` | Wait for reset, or press `R` to resume (`ResumeAgent` RPC) |
| (cleared) | (none) | Banner removed | Automatic on issue clear |

The agent badge in the header also changes to `⚠ Issue` when an issue is active.

### TUI Definition Editor

The Definition tab displays the project definition text.

| Aspect | Behavior |
|--------|----------|
| **Display** | Read-only `bubbles/viewport` showing definition markdown as plain text |
| **Edit** | Press `e` or `Enter` → launches `$EDITOR` via `tea.ExecProcess` |
| **Suspend/resume** | TUI suspends (Bubbletea gives up terminal), external editor takes over, TUI resumes when editor exits |
| **Save** | On editor exit, TUI reads temp file content, calls `UpdateProject` RPC with new definition |
| **Pattern** | Same approach as existing CLI `definition.go` — external editor for multiline content |

### TUI Settings View

The Settings tab shows an inline form for project configuration.

| Field | Type | Maps To |
|-------|------|---------|
| **Name** | Text input | `project.name` |
| **Color** | Text input (hex) | `project.color` |
| **Default branch** | Text input | `project.default_branch` |
| **Auto-merge** | Toggle (on/off) | `project.auto_merge` |
| **Auto-delete branch** | Toggle (on/off) | `project.auto_delete_branch` |
| **Auto-start tasks** | Toggle (on/off) | `project.auto_start_tasks` |

**Navigation:** `j/k` moves between fields. `Enter` or `Space` edits text fields or toggles booleans. Changes saved immediately via `UpdateProject` RPC. Brief "Saved" indicator shown in status bar on success.

### TUI Logs Tab

The right panel's Logs tab shows past agent session logs.

| Aspect | Behavior |
|--------|----------|
| **List view** | `bubbles/list` showing log entries, newest first |
| **Entry format** | `Task #0001 — Session 2 — 2026-02-03 14:30 (completed)` or `Chat — Session 1 — ...` |
| **View log** | `Enter` on entry → switches to `bubbles/viewport` showing log content |
| **Back** | `Esc` returns to log list |
| **Loading** | Logs loaded on demand: `ListLogs` RPC when tab selected, `GetLog` RPC when entry opened |

### TUI Overlays & Modals

Overlays render on top of the main layout and capture all input until dismissed.

**Architecture:** Root model has an `activeOverlay` field. When set, the view function renders the overlay on top, and the update function routes all input to the overlay first.

| Overlay | Trigger | Fields / Content | Dismiss |
|---------|---------|------------------|---------|
| **Help** | `Ctrl+h` | Keybinding reference table | `Esc` or `Ctrl+h` |
| **Add Task** | `a` (task list focused) | Title (text), Prompt (textarea), Criteria (textarea), Status (draft/ready) | `Ctrl+s` saves, `Esc` cancels |
| **Edit Task** | `e` or `Enter` (task list focused) | Same fields as Add, pre-filled | `Ctrl+s` saves, `Esc` cancels |
| **Confirm Delete** | `x` (task list focused) | Inline in status bar: `Delete task #0001? (y/n)` | `y` confirms, `n` or `Esc` cancels |
| **Quit Confirm** | `Ctrl+q` (if agent running) | Inline in status bar: `Agent running. Quit? (y/n)` | `y` quits, `n` or `Esc` cancels |

**Multiline fields** (Prompt, Acceptance Criteria) use `bubbles/textarea`. Submit with `Ctrl+s`, cancel with `Esc`. `Tab` in textarea inserts a tab character (panel switching disabled while overlay active).

### TUI Header & Status Bar

**Header format:**
```
● project-name  Tasks | Definition | Settings    Chat | Logs  ● Working
```

| Element | Description |
|---------|-------------|
| **Project dot** | Circle in project's configured color |
| **Project name** | From `project.yaml` |
| **Left tabs** | Active tab is bold/highlighted, inactive is dimmed |
| **Right tabs** | Same styling as left tabs |
| **Agent badge** | Current agent state (see below) |

**Agent badge states:**

| State | Badge |
|-------|-------|
| No agent | `● Idle` (dim) |
| Agent running (task) | `● Task #0001` (green) |
| Agent running (chat) | `● Chat` (green) |
| Agent running (wildfire) | `● Wildfire` (orange) |
| Agent issue | `⚠ Issue` (yellow) |

**Status bar key hints by context:**

| Context | Key Hints |
|---------|-----------|
| Task list | `Ctrl+q quit  Ctrl+h help  Tab switch  a add  e edit  s start  r ready  d done  x delete` |
| Terminal | `Ctrl+q quit  Ctrl+h help  Tab switch  (input goes to agent)` |
| Definition | `Ctrl+q quit  Ctrl+h help  Tab switch  e edit  (opens $EDITOR)` |
| Settings | `Ctrl+q quit  Ctrl+h help  Tab switch  j/k navigate  Enter edit  Space toggle` |
| Overlay | `Ctrl+s save  Esc cancel` |

### TUI Focus Management

The TUI has two focus zones: left panel and right panel.

| Aspect | Behavior |
|--------|----------|
| **Switch focus** | `Tab` toggles between left and right panel |
| **Visual indicator** | Focused panel has highlighted/bright border, unfocused panel has dimmed border |
| **Mouse click** | Clicking inside a panel focuses it; clicking a tab switches to that tab |
| **Chat focus** | When Chat (right panel) is focused, all input routes to agent via `SendInput` except global shortcuts (`Ctrl+q`, `Tab`, `Ctrl+h`) |
| **Left panel focus** | Single-letter shortcuts (`a`, `e`, `s`, `r`, `t`, `d`, `x`) active only when left panel focused |
| **Tab switching** | `1/2/3` switches left panel tabs, `h/l` or `←/→` switches tabs in focused panel |

### TUI Resize Behavior

| Aspect | Behavior |
|--------|----------|
| **Window resize** | `tea.WindowSizeMsg` → recalculate panel dimensions → propagate to all children |
| **Default split** | 40% left / 60% right |
| **Draggable divider** | Mouse drag on divider adjusts split ratio |
| **Minimum terminal size** | 80 columns × 24 rows. Below this, display "Terminal too small" message |
| **Agent resize** | Right panel dimension change → `Resize` RPC (debounced 100ms) → daemon resizes PTY |

### TUI Styling

| Element | Color / Style |
|---------|---------------|
| **Focused border** | Bright white or project color |
| **Unfocused border** | Dim gray |
| **Active tab** | Bold, underlined |
| **Inactive tab** | Dim |
| **Task: draft** | Default foreground |
| **Task: ready** | Cyan/blue |
| **Task: active (agent)** | Green with spinner |
| **Task: done (success)** | Green |
| **Task: done (failed)** | Red |
| **Issue banner** | Yellow background, dark text |
| **Status bar** | Inverted (light text on dark background) |
| **Header** | Bold project name, styled tabs |

**Color approach:** ANSI 256 colors (not true color) for broad terminal compatibility. Use `lipgloss.AdaptiveColor` to provide both light and dark terminal variants for each color.

### TUI Startup Flow

Step-by-step sequence when `watchfire` is invoked with no arguments:

```
1. Cobra detects no subcommand → launches Bubbletea program
2. Init() returns startup commands:
   a. Check/start daemon (read daemon.yaml, verify PID, start if needed)
   b. Connect to daemon via gRPC
3. On DaemonConnectedMsg:
   a. Load project (GetProject RPC)
   b. Load tasks (ListTasks RPC)
   c. Check agent status (GetAgentStatus RPC)
4. On data loaded:
   a. If agent running → subscribe to SubscribeRawOutput + SubscribeAgentIssues
   b. If no agent → auto-start chat session (StartAgent RPC, mode=chat)
5. Render initial view
6. Enter main update/view loop
```

**Auto-reconnect:** If daemon connection drops, TUI shows "Disconnected" in status bar and attempts reconnection every 3 seconds. On reconnect, reloads project/tasks and resubscribes to streams.

### TUI Error States

| Error | Display | Recovery |
|-------|---------|----------|
| **Daemon not running** | "Starting daemon..." progress in center | Auto-start daemon, retry connection |
| **Connection lost** | "⚠ Disconnected" in status bar | Auto-reconnect every 3s |
| **Daemon shut down** | "Daemon shut down. Press any key to exit." | Exit TUI |
| **Agent crashed** | "Agent stopped unexpectedly." in terminal panel | User restarts via `s` |
| **Project not found** | "Not a Watchfire project. Run `watchfire init` first." → exit | User runs `watchfire init` |
| **gRPC error** | Brief error flash in status bar (3s) | Automatic retry for transient errors |
| **Git not found** | "Git is required. Install git and try again." → exit | User installs git |
| **Agent binary missing** | "Claude Code not found. Configure path in settings." → exit | User configures agent path |

### TUI v1 Scope

**Included:**
- Split-view layout (left panel + right panel)
- Task CRUD with full status transitions (draft ↔ ready → done)
- Agent terminal streaming via raw PTY bytes
- Agent start/stop (chat, task, start-all, wildfire modes)
- Wildfire phase display in terminal panel
- Project definition editing via external `$EDITOR`
- Settings form with inline editing
- Help overlay with keybinding reference
- Issue banners (auth required, rate limited) with recovery actions
- Mouse support (click, scroll, drag divider)
- Keyboard navigation (vim-style + arrows)
- Context-sensitive status bar with key hints
- Connection status with auto-reconnect
- Log list and log viewer

**Excluded (future):**
- Inline definition editor (always uses `$EDITOR` in v1)
- Task drag-to-reorder
- Trash tab (soft-deleted tasks managed via CLI commands)
- Branch management tab
- Log deletion from TUI
- Task search/filter
- Multi-project switching
- Theme customization
- Notifications
- Split terminal (multiple agents visible)

### Task Work Order

When multiple tasks are in `ready` status, agent picks next by:
1. Sort by `position` (ascending)
2. If equal, sort by `task_number` (ascending)
3. Pick first

### Wildfire Mode

Three-phase autonomous loop. Each phase is a separate agent process. The daemon manages transitions.

| Phase | Working Dir | Completion Signal | Description |
|-------|-------------|-------------------|-------------|
| **Execute** | Worktree | Task YAML `status: done` | Work on ready tasks (same as task mode). One agent per task. |
| **Refine** | Project root | `.watchfire/refine_done.yaml` | Analyze codebase, improve a draft task's prompt/criteria, set status: ready. |
| **Generate** | Project root | `.watchfire/generate_done.yaml` | Analyze project, create new tasks if meaningful work remains. |

**State machine:**
```
Execute (ready tasks) → Refine (draft tasks) → Generate (no tasks left)
         ↑                      ↑                         |
         └──────────────────────┘─────────────────────────┘
                                                (if new tasks created → loop)
                                                (if no new tasks → chat mode)
```

| Aspect | Behavior |
|--------|----------|
| **Entry** | `watchfire agent wildfire` |
| **Phase selection** | Daemon picks phase based on available tasks: ready → execute, draft → refine, none → generate |
| **Phase completion** | Agent creates signal file → daemon detects, deletes file, stops agent → next phase |
| **Stop conditions** | Generate phase creates no new tasks → transitions to chat mode, OR Ctrl+C |
| **Autonomy** | Fully autonomous, no human approval between cycles |

---

## GUI (Electron)

### Overview

The GUI is a multi-project client built with Electron. Unlike the CLI/TUI (project-scoped), the GUI shows all registered projects in one place. It connects to the daemon via gRPC-Web.

### Layout

```
┌─────────────────────────────────────────────────────────────────┐
│  Sidebar          │  Main Content                │  Right Panel │
│                   │                              │  (collapsible)│
│  - Logo/version   │  Dashboard OR Project View   │              │
│  - Dashboard      │                              │  - Chat      │
│  - Projects list  │                              │  - Branches  │
│  - Add Project    │                              │  - Logs      │
│  - Settings       │                              │              │
└─────────────────────────────────────────────────────────────────┘
```

### Views

#### Dashboard

Overview of all projects:
- Project cards showing: name, status dot, task counts (Draft/Ready/Done), active task
- "Add Project" button
- Click card → opens Project View

#### Add Project Wizard (3 steps)

**Step 1 — Project Info:**
- Project name
- Path (folder picker)
- Git status (detected)
- Branch (detected)

**Step 2 — Git Configuration:**
- Target branch
- Automation toggles:
  - Auto-merge on completion (local only)
  - Delete branch after merge (local only)
  - Auto-start tasks

**Step 3 — Project Definition:**
- Markdown editor for project context
- Skip option

#### Project View

Split layout with tabs:

**Left/Center Tabs:**

| Tab | Content |
|-----|---------|
| **Tasks** | Grouped by status (Draft, Ready, Done). Search, filters. Add task button. Status transition buttons. |
| **Definitions** | Markdown editor for project definition |
| **Trash** | Soft-deleted tasks. Restore, permanent delete, empty trash. |
| **Settings** | Git config, automation toggles, project color |

**Right Panel (collapsible, adjustable width):**

| Tab | Content |
|-----|---------|
| **Chat** | Agent terminal streamed from daemon. User can type input. |
| **Branches** | List of worktrees/branches with status, actions |
| **Logs** | Past session logs per task |

#### Global Settings

| Section | Content |
|---------|---------|
| **Defaults** | Default branch, automation toggles for new projects |
| **Appearance** | Theme (System/Light/Dark) |
| **Claude CLI** | Path detection, custom path, install instructions |
| **Updates** | Check frequency, auto-download toggle |

### Task Status Display

| Internal Status | Display Label | Visual |
|-----------------|---------------|--------|
| `draft` | Todo | Default style |
| `ready` | In Development | Highlighted |
| `ready` + agent active | In Development | Animated indicator (pulsing/spinner) |
| `done` (success: true) | Done | Green indicator |
| `done` (success: false) | Failed | Red indicator |

### v1 Scope

**Included:**
- Dashboard with project cards
- Add Project wizard
- Project View (Tasks, Definitions, Trash, Settings)
- Right panel (Chat, Branches, Logs)
- Global Settings (Defaults, Appearance, Claude CLI, Updates)
- Sidebar navigation

**Excluded (future):**
- Notifications
- Generate Tasks button
- Auto-fill Definitions
- Branch mode toggle (always "new branch per task")
- Create PR on completion
- Wildfire mode in GUI

---

## Directory Structures

### Global (`~/.watchfire/`)

```
~/.watchfire/
├── daemon.yaml         # Connection info (host, port, PID, started_at)
├── agents.yaml         # Running agents state (project, mode, task info)
├── projects.yaml       # Projects index (id, path, name, position)
├── settings.yaml       # Global settings (agent paths, defaults)
└── logs/               # Session logs
    └── <project_id>/
        └── <task_number>-<session>-<timestamp>.log
```

**Log filename examples:**
- `0001-1-2026-02-03T13-05-00.log` — task 1, session 1
- `0001-2-2026-02-03T14-30-00.log` — task 1, session 2
- `chat-1-2026-02-03T15-00-00.log` — chat mode (no task)

### Per-Project (`<project>/.watchfire/`)

```
<project>/
├── .gitignore              # Daemon appends .watchfire/ here on init
└── .watchfire/             # Gitignored
    ├── project.yaml        # Project definition
    ├── tasks/
    │   └── 0001.yaml       # Task files (4-digit padded task_number)
    └── worktrees/
        └── 0001/           # Git worktrees (named by task_number)
```

**On project init**, daemon:
1. Creates `.watchfire/` directory structure
2. Appends `.watchfire/` to project's `.gitignore` (creates if missing)
3. Commits the `.gitignore` change (prevents worktree circular dependencies)
4. Adds project to `~/.watchfire/projects.yaml`

---

## Data Structures

### Screen Buffer (daemon → client)

```json
{
  "timestamp": "2026-02-03T13:05:00.123Z",
  "rows": 24,
  "cols": 80,
  "cursor": {"row": 5, "col": 12, "visible": true},
  "cells": [
    [{"char": ">", "fg": 7, "bg": 0, "bold": true, "italic": false, "underline": false, "inverse": false, "blink": false, "strikethrough": false, "dim": false}, ...]
  ],
  "scrollback_available": 150
}
```

- `cells` is a 2D array: `cells[row][col]`
- `fg` and `bg` are ANSI color codes (0-15 for standard, 16-255 for extended)
- Attributes: `bold`, `italic`, `underline`, `inverse`, `blink`, `strikethrough`, `dim`
- Full buffer sent each time (no diff mechanism)
- `scrollback_available`: lines in history, retrievable via separate RPC

### Task File Format

Tasks are YAML files in `<project>/.watchfire/tasks/`.

**Filename:** `<task_number_4digit>.yaml` (e.g., `0001.yaml`, `0012.yaml`)

```yaml
version: 1
task_id: "k7xm2nq9"                   # 8-char alphanumeric, internal only
task_number: 1                        # Sequential within project, user-facing
title: "Create HTML structure"
prompt: |
  Detailed instruction for the agent...
acceptance_criteria: |
  - index.html exists with valid HTML5 structure
  - 9 cells are rendered in a 3x3 layout
status: "draft"                       # draft | ready | done
success: true                         # Only when status=done
failure_reason: "..."                 # Only when success=false
position: 1                           # Display/work ordering
agent_sessions: 2                     # How many times agent worked on this
created_at: "2026-02-03T13:05:00Z"
started_at: "2026-02-03T14:00:00Z"    # When agent first started
completed_at: "2026-02-03T15:30:00Z"  # When status changed to done
updated_at: "2026-02-03T22:16:32Z"
deleted_at: null                      # Soft delete timestamp
```

**Task Status Flow:**
```
draft → ready → done (success: true)
                  └→ done (success: false, failure_reason: "...")
```

| Status | Meaning |
|--------|---------|
| `draft` | Created, not ready for agent |
| `ready` | Agent can pick up (triggers auto-start if enabled) OR agent currently working |
| `done` | Completed. Check `success` flag for outcome |

**User reference:** `watchfire task 1` (uses task_number, not task_id)

### Project File Format

Project configuration in `<project>/.watchfire/project.yaml`:

```yaml
version: 1
project_id: "53760635-1694-4cd1-8c30-5e705350f577"
name: "my-project"
status: "active"                      # active | archived (future)
color: "#22c55e"                      # Project color for GUI (hex)
default_branch: "main"
default_agent: "claude-code"
sandbox: "sandbox-exec"               # Internal: sandbox-exec, future: docker, etc.
auto_merge: true
auto_delete_branch: true
auto_start_tasks: true
definition: |
  # Project Name

  Description of what this project is...

  ## Technical Stack
  - React, Node.js, etc.

  ## Goals
  - Feature 1
  - Feature 2
created_at: "2026-02-03T13:02:52Z"
updated_at: "2026-02-03T14:30:00Z"
next_task_number: 6
```

### Global Settings File Format

Global configuration in `~/.watchfire/settings.yaml`:

```yaml
version: 1

agents:
  claude-code:
    path: null                    # null = lookup in PATH, or absolute path
  # future agents here

defaults:
  auto_merge: true
  auto_delete_branch: true
  auto_start_tasks: true
  default_branch: "main"
  default_sandbox: "sandbox-exec"
  default_agent: "claude-code"

updates:
  check_on_startup: true
  check_frequency: "every_launch"  # every_launch | daily | weekly
  auto_download: false
  last_checked: "2026-02-03T13:00:00Z"

appearance:
  theme: "system"                  # system | light | dark
```

### Projects Index File Format

Registry of all projects in `~/.watchfire/projects.yaml`:

```yaml
version: 1
projects:
  - project_id: "53760635-1694-4cd1-8c30-5e705350f577"
    name: "my-project"
    path: "/Users/me/code/my-project"
    position: 1
  - project_id: "a1b2c3d4-5678-90ab-cdef-1234567890ab"
    name: "another-project"
    path: "/Users/me/code/another"
    position: 2
```

**Notes:**
- `path` updated if same `project_id` opened from new location
- `position` used for GUI ordering
- **Self-healing:** Every project-scoped CLI command calls `EnsureProjectRegistered()`, which reads the local `.watchfire/project.yaml` and re-registers the project in the global index if missing, updates the path if the project moved, and reactivates it if archived. This means deleting `projects.yaml` or moving a project directory is automatically repaired on first use.

### Daemon Connection File Format

Written by daemon on startup (`~/.watchfire/daemon.yaml`):

```yaml
version: 1
host: "localhost"
port: 52431
pid: 12345
started_at: "2026-02-03T13:02:52Z"
```

**Port allocation:** Daemon starts with port `0` → OS auto-allocates free port → daemon writes actual port here.

**Daemon discovery logic:**

| Condition | Meaning | Action |
|-----------|---------|--------|
| File doesn't exist | No daemon running | Client starts daemon |
| File exists, PID alive | Daemon running | Client connects |
| File exists, PID dead | Daemon crashed | Client deletes stale file, starts daemon |

PID check: `kill -0 <pid>` (returns 0 if process exists)

### Agent State File Format

Written by daemon agent manager (`~/.watchfire/agents.yaml`):

```yaml
version: 1
agents:
  - project_id: "abc12345"
    project_name: "my-project"
    project_path: "/home/user/my-project"
    mode: "task"              # chat | task | start-all | wildfire
    task_number: 1            # Only when mode is task or wildfire
    task_title: "Add auth"    # Only when mode is task or wildfire
    issue_type: "rate_limited"  # auth_required | rate_limited | "" (optional)
    issue_message: "You've hit your limit"  # Original error message (optional)
```

- Updated by daemon agent manager whenever agents start or stop
- Read by `watchfire daemon status` to display active agents
- Cleaned up on daemon shutdown
- `issue_type` and `issue_message` reflect current blocking issue if any

---

## Agent Detection & Error Handling

### Issue Detection Architecture

The daemon detects issues (auth errors, rate limits) in real-time by scanning PTY output in the read loop. Detection happens in `Process.detectIssues()` which is called after each PTY read. Lines are buffered and scanned for known patterns.

```
PTY Read → detectIssues() → Pattern Match → AgentIssue → Broadcast to Subscribers
```

### Issue Types

| Type | Constant | Patterns |
|------|----------|----------|
| **Auth Required** | `auth_required` | "API Error: 401", "OAuth token expired", "Please run /login" |
| **Rate Limited** | `rate_limited` | "You've hit your limit", "rate limit", "too many requests", "API Error: 429" |

### Auth Issues

| Aspect | Behavior |
|--------|----------|
| **Detection** | Daemon parses agent PTY output for auth-required patterns |
| **Claude Code** | Detects "401 authentication_error", "OAuth token expired", "/login" prompts |
| **Agent state** | Agent keeps running (user needs terminal access for `/login`) |
| **Notification** | Clients receive issue via `SubscribeAgentIssues` stream |
| **Recovery** | User runs `/login` in terminal, issue auto-clears on successful auth |

### Rate Limits

| Aspect | Behavior |
|--------|----------|
| **Detection** | Daemon parses agent output for rate limit patterns |
| **Reset time parsing** | Extracts reset time from messages like "resets 4am (Europe/Lisbon)" |
| **Agent state** | Agent keeps running (allows user to wait or take action) |
| **Notification** | Clients receive issue with `reset_at` and `cooldown_until` timestamps |
| **User override** | Call `ResumeAgent` RPC to clear cooldown and retry |

### gRPC RPCs for Issues

| RPC | Purpose |
|-----|---------|
| `SubscribeAgentIssues` | Stream of `AgentIssue` messages when issues detected/cleared |
| `ResumeAgent` | Clear current issue (e.g., after rate limit cooldown) |
| `GetAgentStatus` | Includes current `AgentIssue` if any |

### AgentIssue Message

```protobuf
message AgentIssue {
  string issue_type = 1;                    // "auth_required" | "rate_limited" | ""
  google.protobuf.Timestamp detected_at = 2;
  string message = 3;                       // Original error message from agent
  optional google.protobuf.Timestamp reset_at = 4;      // When limit resets
  optional google.protobuf.Timestamp cooldown_until = 5; // When to auto-resume
}
```

### Startup Checks

On `watchfire init`, TUI launch, or GUI launch:

| Check | Required | Action if missing |
|-------|----------|-------------------|
| **Git** | Yes | Block with error: "Git is required. Install git and try again." |
| **Coding agent** | Yes | Block until user configures agent path in settings |

### Update Check

On every client startup:

| Aspect | Behavior |
|--------|----------|
| **When** | Every client startup (configurable: every launch / daily / weekly) |
| **Source** | Check GitHub releases for new version |
| **Scope** | Updates entire stack (daemon, CLI/TUI, GUI) together |
| **If update available** | Prompt user to download and install |
| **If daemon running** | After update, prompt user to restart daemon |
| **Settings** | Check frequency, auto-download toggle |

### Daemon Lifecycle

| Aspect | Behavior |
|--------|----------|
| **Startup** | `watchfire daemon start`, or started automatically by any client command if not running |
| **Persistence** | **Stays running** when clients close |
| **Shutdown** | `watchfire daemon stop`, or system tray "Quit" |
| **Rationale** | Agents can continue working in background; system tray provides visibility |

---

## Git Behavior

| Rule | Description |
|------|-------------|
| **No push** | Watchfire and agents NEVER push to upstream |
| **Local only** | All changes and merges happen locally |
| **Root reflection** | After merge, changes appear in project root (main worktree) |
| **Worktrees** | Tasks run in `.watchfire/worktrees/<task_number>/`, branch `watchfire/<task_number>` |

---

## gRPC API

Defined in `proto/watchfire.proto`.

### Request Metadata

Every request includes origin for analytics:

```protobuf
message RequestMeta {
  string origin = 1;        // "cli" | "tui" | "gui" | "api"
  string client_id = 2;     // Unique client instance ID
  string version = 3;       // Client version
}
```

### Services

| Service | Purpose |
|---------|---------|
| `ProjectService` | Project CRUD |
| `TaskService` | Task CRUD + bulk operations |
| `AgentService` | Agent control, terminal streaming |
| `BranchService` | Worktree/branch management |
| `LogService` | Session logs |
| `DaemonService` | Daemon status, shutdown |
| `SettingsService` | Global settings |

### ProjectService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `ListProjects` | `Empty` | `ProjectList` | All registered projects |
| `GetProject` | `ProjectId` | `Project` | Single project details |
| `CreateProject` | `CreateProjectRequest` | `Project` | Init new project |
| `UpdateProject` | `UpdateProjectRequest` | `Project` | Update settings/definition |
| `DeleteProject` | `ProjectId` | `Empty` | Unregister project |

### TaskService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `ListTasks` | `ListTasksRequest` | `TaskList` | Filter by status, include deleted |
| `GetTask` | `TaskId` | `Task` | Single task |
| `CreateTask` | `CreateTaskRequest` | `Task` | Add new task |
| `UpdateTask` | `UpdateTaskRequest` | `Task` | Edit task, change status |
| `DeleteTask` | `TaskId` | `Task` | Soft delete |
| `RestoreTask` | `TaskId` | `Task` | Restore soft-deleted |
| `EmptyTrash` | `ProjectId` | `Empty` | Permanent delete all |
| `BulkUpdateStatus` | `BulkUpdateStatusRequest` | `TaskList` | Update status for list of tasks |
| `BulkDelete` | `BulkDeleteRequest` | `TaskList` | Soft delete multiple |
| `BulkRestore` | `BulkRestoreRequest` | `TaskList` | Restore multiple |
| `ReorderTasks` | `ReorderTasksRequest` | `TaskList` | Update positions |

```protobuf
message BulkUpdateStatusRequest {
  RequestMeta meta = 1;
  string project_id = 2;
  repeated int32 task_numbers = 3;  // List of task numbers
  string new_status = 4;            // "draft" | "ready" | "done"
}
```

### AgentService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `StartAgent` | `StartAgentRequest` | `AgentStatus` | Start agent (chat or task mode) |
| `StopAgent` | `ProjectId` | `AgentStatus` | Stop running agent |
| `GetAgentStatus` | `ProjectId` | `AgentStatus` | Is agent running? Which task? Includes current issue. |
| `SubscribeScreen` | `SubscribeScreenRequest` | `stream ScreenBuffer` | Live terminal stream (parsed via vt10x, for GUI) |
| `SubscribeRawOutput` | `SubscribeRawOutputRequest` | `stream RawOutputChunk` | Raw PTY bytes stream (for CLI) |
| `GetScrollback` | `ScrollbackRequest` | `ScrollbackLines` | Historical terminal lines |
| `SendInput` | `SendInputRequest` | `Empty` | Send keystrokes to agent |
| `Resize` | `ResizeRequest` | `Empty` | Resize terminal |
| `SubscribeAgentIssues` | `SubscribeAgentIssuesRequest` | `stream AgentIssue` | Real-time auth/rate limit issue notifications |
| `ResumeAgent` | `ProjectId` | `AgentStatus` | Clear current issue (e.g., after rate limit) |

### BranchService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `ListBranches` | `ProjectId` | `BranchList` | All watchfire branches |
| `GetBranch` | `BranchId` | `Branch` | Single branch |
| `MergeBranch` | `MergeBranchRequest` | `Branch` | Merge to target |
| `DeleteBranch` | `BranchId` | `Empty` | Delete single |
| `PruneBranches` | `ProjectId` | `BranchList` | Clean orphaned |
| `BulkMerge` | `BulkBranchRequest` | `BranchList` | Merge multiple |
| `BulkDelete` | `BulkBranchRequest` | `Empty` | Delete multiple |

### LogService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `ListLogs` | `ListLogsRequest` | `LogList` | Logs for project/task |
| `GetLog` | `LogId` | `Log` | Single log content |
| `DeleteLog` | `LogId` | `Empty` | Delete single |
| `BulkDelete` | `BulkLogRequest` | `Empty` | Delete multiple |
| `DeleteAllForTask` | `TaskId` | `Empty` | Delete all logs for task |
| `DeleteAllForProject` | `ProjectId` | `Empty` | Delete all logs for project |

```protobuf
message Log {
  string log_id = 1;
  string project_id = 2;
  int32 task_number = 3;        // 0 if chat mode
  int32 session_number = 4;     // Which session (1, 2, 3...)
  string agent = 5;             // "claude-code"
  string started_at = 6;
  string ended_at = 7;
  string content = 8;           // Simplified text from terminal
  string status = 9;            // "completed" | "failed" | "interrupted"
}
```

### DaemonService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `GetStatus` | `Empty` | `DaemonStatus` | Port, uptime, active agents |
| `Shutdown` | `Empty` | `Empty` | Graceful shutdown |

### SettingsService

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `GetSettings` | `Empty` | `Settings` | Global settings |
| `UpdateSettings` | `Settings` | `Settings` | Update global settings |

### Event Streaming (per-project)

| RPC | Request | Response | Notes |
|-----|---------|----------|-------|
| `Subscribe` | `SubscribeRequest` | `stream Event` | Real-time events |

```protobuf
message SubscribeRequest {
  RequestMeta meta = 1;
  string project_id = 2;
}

message Event {
  string event_type = 1;    // "task_created" | "task_updated" | "agent_started" | etc.
  string timestamp = 2;
  oneof payload {
    Task task = 3;
    AgentStatus agent = 4;
    Branch branch = 5;
  }
}
```

---

## Flows

### Daemon Flows

#### Shutdown Flow

```
1. User triggers shutdown (system tray "Quit" OR CLI command)
2. Daemon notifies all connected clients: "shutting down"
3. Clients display message and close gracefully
4. Daemon stops all running agents (sends SIGTERM)
5. Daemon cleans up (releases port, deletes daemon.yaml)
6. Daemon exits
```

#### System Tray

| Item | Action |
|------|--------|
| **Header** | "Watchfire Daemon" |
| **Port** | "Running on port: {port}" |
| **Separator** | — |
| **Active Agents** | Submenu per agent (see below) |
| **No active agents** | "No active agents" (greyed) |
| **Separator** | — |
| **Open GUI** | Launches GUI |
| **Quit** | Shutdown daemon |

**Active Agent Submenu:**
```
● project-name — Task #0001: Title
  ├─ Open in GUI
  └─ Stop Agent
```

**Agent display format:**

| Mode | Display |
|------|---------|
| Chat | "● project-name — Chat" |
| Task | "● project-name — Task #0001: Title" |
| Wildfire | "🔥 project-name — Wildfire (Task #0003)" |

**Tooltip:** "Watchfire — {n} projects, {m} active"

---

### CLI/TUI Flows

#### C1: Startup Flow

**Client-side:**
```
1. Check for updates (client checks GitHub)
   └─ If available → prompt, download, install

2. Check if daemon running
   └─ If not → start daemon in background
   └─ If stale → clean up, start daemon

3. Connect to daemon via gRPC

4. Call daemon's health/startup check
   └─ Daemon checks Git → returns error if missing
   └─ Daemon checks agent paths → returns error if none configured

5. If errors → display to user, exit (or prompt to configure)
```

**After successful connection:**
```
6. If no project (.watchfire/ missing):
   └─ CLI: exit with error
   └─ TUI: interactive init via daemon RPCs

7. TUI → subscribe, render
   CLI → execute RPC, display, exit
```

#### C2: `watchfire init` Flow

**Client-side:**
```
1. Run startup checks (C1 steps 1-5)
2. Check if already a project (.watchfire/ exists)
   └─ If yes → ERROR: "Already a Watchfire project." → exit
```

**Interactive prompts (client displays, daemon executes):**
```
3. Prompt: "Project name:" (default: folder name)
4. Prompt: "Project definition (optional):"
5. Prompt: "Project settings:"
   └─ Auto-merge after task completion? (y/n) [default: y]
   └─ Auto-delete worktrees after merge? (y/n) [default: y]
   └─ Auto-start agent when task set to ready? (y/n) [default: y]
   └─ Default branch? [default: main]

6. Client calls daemon CreateProject RPC with all inputs
```

**Daemon-side (CreateProject RPC):**
```
7. Check if git repo → if not, run `git init`
8. Create .watchfire/ directory structure
9. Add .watchfire/ to .gitignore (create if missing)
10. Commit .gitignore change
11. Register project in ~/.watchfire/projects.yaml
12. Return success to client
```

#### C3: Task Management Flows

**Add Task (`watchfire task add`):**
```
1. Prompt: Title, Prompt, Acceptance criteria, Status
2. Client calls daemon CreateTask RPC
3. Daemon creates task file, returns task
4. If status=ready AND auto_start → trigger agent
```

**Edit Task (`watchfire task <number>`):**
```
1. Call GetTask RPC
2. Display interactive editor
3. Call UpdateTask RPC
4. If status changed to ready AND auto_start → trigger agent
```

**Delete/Restore:**
```
DeleteTask → sets deleted_at timestamp
RestoreTask → clears deleted_at
```

**Status Transitions (TUI):**

| Key | Action |
|-----|--------|
| `r` | Move to Ready (status change only) |
| `t` | Move to Draft |
| `d` | Mark Done (success=true) |
| `s` | Start Agent (work on ready tasks) |

#### C4: `watchfire agent start` Flow

**Client-side:**
```
1. Call daemon StartAgent RPC (project_id, optional task_number)
2. If agent already running → attach to existing stream
3. Subscribe to SubscribeScreen
4. Terminal shows agent output
5. User can type (SendInput RPC)
6. Ctrl+C → detach (task/wildfire) or stop (chat only)
```

**Daemon-side (if not running):**
```
1. Check for ready tasks (or use specified task_number)
2. If task → create worktree, set status=ready
3. Spawn agent in sandbox + PTY
4. Stream to clients
5. On task done → check for more ready → continue or chat mode
```

**Agent behavior:**
```
1. Pick first ready task (by position, then task_number)
2. Work on it
3. When done → check for more ready tasks
   └─ If yes → pick next
   └─ If no → switch to chat mode
4. Agent keeps running until explicitly stopped
```

#### C5: `watchfire agent wildfire` Flow

**Wildfire three-phase loop (daemon-managed):**
```
1. Daemon checks for ready tasks
   └─ If found → Execute phase: spawn agent in worktree (same as task mode)
   └─ Agent completes → daemon loops back to step 1

2. Daemon checks for draft tasks
   └─ If found → Refine phase: spawn agent at project root
   └─ Agent analyzes codebase, improves task, sets status: ready
   └─ Agent completes → daemon loops back to step 1

3. If previous phase was Generate → wildfire complete
   └─ Transition to chat mode

4. Generate phase: spawn agent at project root
   └─ Agent analyzes: definition, codebase, existing tasks
   └─ Agent decides: create new tasks OR create nothing ("best version")
   └─ Agent completes → daemon loops back to step 1
   └─ If no new tasks found → step 3 triggers chat transition
```

Each phase is a separate agent process. The daemon manages all transitions.

**Ctrl+C in wildfire:** Detaches only (agent continues in background)

**Stop wildfire:** System tray "Stop Agent" or GUI

#### C6: TUI Interaction

**Startup:** See "TUI Startup Flow" subsection for the full step-by-step sequence (Cobra → Bubbletea → daemon connect → load data → subscribe/auto-start → render).

**Layout:**
```
┌─────────────────────────────────────────────────────────────┐
│ ● project-name  Tasks | Definition | Settings  Chat | Logs │
├────────────────────────────────┬────────────────────────────┤
│  LEFT PANEL                    │  RIGHT PANEL               │
│  (Task list / Definition /     │  (Agent terminal +         │
│   Settings)                    │   issue banners)           │
└────────────────────────────────┴────────────────────────────┘
```

**On TUI open:**
- If agent running → attach, show current state + stream via `SubscribeRawOutput`
- If no agent → auto-start chat session
- Subscribe to `SubscribeAgentIssues` for auth/rate limit banners

**Auto-reconnect:** If daemon connection drops, status bar shows "⚠ Disconnected" and TUI retries every 3s. On reconnect, reloads project/tasks and resubscribes to streams.

**Global shortcuts:**

| Key | Action |
|-----|--------|
| `Tab` | Switch panels |
| `Ctrl+q` | Quit TUI |
| `Ctrl+h` | Help overlay |

**Left panel — Tasks:**

| Key | Action |
|-----|--------|
| `j/k` / `↓/↑` | Move selection |
| `a` | Add task |
| `e` / `Enter` | Edit task |
| `r` | Move to Ready |
| `t` | Move to Draft |
| `d` | Mark Done |
| `x` | Delete |
| `s` | Start agent |
| `1/2/3` | Switch tabs |

**Right panel — Chat:**
- All input goes to agent (except global shortcuts)
- Issue banners (auth, rate limit) appear above terminal viewport when active
- Ctrl+C detaches (task/wildfire) or stops (chat)
- Mouse scroll for scrollback

**Mouse:** Click, scroll, drag divider to resize

#### C7: Update Flow

```
1. Check GitHub releases on startup
2. If update available → prompt user
3. Download all binaries (daemon, CLI/TUI, GUI)
4. If daemon running → prompt to restart
5. Replace binaries, restart daemon if requested
```

---

### GUI Flows

#### G1: Startup Flow

```
1. Launch Watchfire.app
2. Check for updates → show banner if available
3. Check/start daemon
4. Connect via gRPC-Web
5. Daemon health check (Git, agent)
6. Load projects list
7. Render Dashboard
```

#### G2: Add Project Wizard

**Entry:** Dashboard "Add Project" button or sidebar

**Step 1 — Project Info:**
```
1. Folder picker opens ("New Folder" option available)
2. User selects/creates folder
3. If existing project (.watchfire/ exists):
   └─ Import project, register, navigate to Project View
   └─ Skip wizard
4. Display: Project name, path, git status, branch
5. Click "Next"
```

**Step 2 — Git Configuration:**
```
1. Target branch (default: main)
2. Automation toggles:
   └─ Auto-merge on completion (default: ON)
   └─ Delete branch after merge (default: ON)
   └─ Auto-start tasks (default: ON)
3. Click "Next"
```

**Step 3 — Project Definition:**
```
1. Markdown editor (optional)
2. Click "Create Project" or "Skip"
```

**On create:**
```
1. Call daemon CreateProject RPC
2. Daemon: init git, create .watchfire/, commit .gitignore, register
3. Success modal
4. Navigate to Project View
```

#### G3: Dashboard Interactions

**Project card displays:**
- Status dot (green=idle, orange=running, red=error)
- Project name
- Task counts (Todo, In Dev, Done)
- Active task (if agent running)

**Interactions:**

| Action | Result |
|--------|--------|
| Click card | Open Project View |
| Click active task | Open Project View, focus on task |
| Drag card | Reorder projects |

**Empty state:**
```
"No projects yet"
"Watchfire helps you orchestrate autonomous coding agent
sessions across your projects based on specs."
[Add Your First Project]
```

#### G4: Project View — Task Management

**Task list features:**
- Grouped by status: Done, In Development, Todo
- Search box
- Filters: Failed, With Issue
- Drag to reorder

**Task interactions:**

| Action | Result |
|--------|--------|
| Click task | Select, show in right panel |
| Double-click | Open editor modal |
| "→ Dev" button | Move to Ready |
| "Done" button | Mark success=true |
| "Todo" button | Move to Draft |
| 🗑 button | Soft delete |
| Drag | Reorder position |

**Add Task modal:**
- Title (required)
- Prompt (markdown)
- Acceptance criteria (markdown)
- Status: Draft / Ready

**Group actions:**
- 🗑 on group header → delete all in group
- "▶ Start" on In Development → start agent

#### G5: Project View — Agent Interaction

**Right panel tabs:**

| Tab | Content |
|-----|---------|
| **Chat** | Live agent terminal, user input |
| **Branches** | Worktrees/branches for project |
| **Logs** | Past session logs |

**Right panel header:**
- Collapse/expand button
- Agent mode badge: "Chat" / "Task #0001: Title" / "🔥 Wildfire"

---

**Chat Tab**

**On project open:**
- If agent running → attach, show current state + stream
- If no agent → auto-start chat session

**Display:**
- Live terminal stream from daemon (gRPC-Web SubscribeScreen)
- User types → SendInput RPC
- Mode badge in header

**Agent controls:**

| Element | Action |
|---------|--------|
| **Stop** button | Stops agent (StopAgent RPC) |

**Starting task from GUI:**

| Entry point | Behavior |
|-------------|----------|
| "▶ Start" on task group | Agent picks ready tasks, works through queue |
| Right-click task → "Start" | Agent works on specific task |

**Note:** If agent in chat mode, starting a task transitions it to task mode. If already working on tasks, new task gets queued (set to ready).

---

**Branches Tab**

**Branch list displays:**
```
watchfire/0001 — Task #0001: Title
├─ Status: merged / unmerged / orphaned
├─ Created: 2026-02-03
└─ Actions: [Merge] [Delete]

watchfire/0003 — Task #0003: Another task
├─ Status: unmerged (agent working)
└─ Actions: — (disabled while agent working)
```

**Branch statuses:**

| Status | Meaning |
|--------|---------|
| merged | Branch merged to target, worktree cleaned |
| unmerged | Branch exists, not yet merged |
| orphaned | Worktree exists but no task reference |

**Branch actions:**

| Action | When available | Behavior |
|--------|----------------|----------|
| Merge | Unmerged, agent not working | MergeBranch RPC |
| Delete | Not currently in use | DeleteBranch RPC |
| Prune All | Orphans exist | PruneBranches RPC |

**Bulk actions:**
- Checkbox select multiple branches
- "Merge Selected" / "Delete Selected" buttons

---

**Logs Tab**

**Log list displays:**
```
Task #0001 — Session 2 — 2026-02-03 14:30
├─ Duration: 25m
├─ Status: completed
└─ [View]

Task #0001 — Session 1 — 2026-02-03 13:05
├─ Duration: 15m
├─ Status: completed
└─ [View]

Chat — Session 1 — 2026-02-03 12:00
├─ Duration: 10m
├─ Status: interrupted
└─ [View]
```

**Filter/sort:**
- Filter by task (dropdown)
- Sort by date (default: newest first)

**View log:**
- Click "View" → opens log viewer modal
- Simplified text rendering (no terminal colors)
- Scroll, search within log

**Log actions:**

| Action | Behavior |
|--------|----------|
| Delete single | DeleteLog RPC |
| Delete all for task | DeleteAllForTask RPC |
| Delete all | DeleteAllForProject RPC (confirmation required) |

#### G6: Settings

**Entry:** Sidebar "Settings" link

**Sections:**

| Section | Content |
|---------|---------|
| **Defaults** | Default values for new projects |
| **Appearance** | Theme selection |
| **Claude CLI** | Agent path configuration |
| **Updates** | Update preferences |

---

**Defaults Section:**
- Default branch (text input, default: "main")
- Auto-merge on completion (toggle, default: ON)
- Delete branch after merge (toggle, default: ON)
- Auto-start tasks (toggle, default: ON)

**Appearance Section:**
- Theme: System / Light / Dark (radio or dropdown)

**Claude CLI Section:**
- Status: "Detected at /usr/local/bin/claude" or "Not found"
- Custom path input (optional override)
- "Test" button → validates path
- Link to install instructions if not found

**Updates Section:**
- Check frequency: Every launch / Daily / Weekly (dropdown)
- Auto-download updates (toggle, default: OFF)
- "Check Now" button
- Last checked: timestamp

**Save behavior:** Auto-save on change (UpdateSettings RPC), show brief "Saved" toast

#### G7: Update Flow

**Update check triggers:**
- App startup (based on frequency setting)
- Manual "Check Now" in Settings

**If update available:**

```
1. Banner appears at top of window:
   "Update available: v1.2.0 → v1.3.0  [Download] [Dismiss]"

2. User clicks "Download":
   └─ Progress indicator
   └─ Downloads all components (daemon, CLI/TUI, GUI)

3. Download complete:
   └─ If daemon running with active agents:
      "Restart required. Active agents will be stopped. [Restart Now] [Later]"
   └─ If daemon idle or not running:
      "Ready to install. [Restart Now] [Later]"

4. User clicks "Restart Now":
   └─ Stop daemon (if running)
   └─ Replace binaries
   └─ Restart app
   └─ Start daemon
```

**"Later" behavior:**
- Banner persists: "Update downloaded. [Restart to Install]"
- Update applies on next manual restart

**Auto-download (if enabled):**
- Downloads silently in background
- Shows "Update ready" banner when complete

---

## Development

### Repository Structure

Monorepo containing all components:

```
watchfire/
├── cmd/
│   ├── watchfired/       # Daemon entry point
│   └── watchfire/        # CLI/TUI entry point
├── internal/             # Shared Go packages
├── proto/                # Protobuf definitions
├── gui/                  # Electron app
├── assets/               # Icons, images
├── scripts/              # Build scripts
├── version.json          # Version tracking
├── CHANGELOG.md          # Release changelog
├── Makefile              # Dev commands
└── ...
```

**Separate repo:** Website and documentation

### Dev Commands

```makefile
make dev-daemon    # Run daemon in foreground with hot reload
make dev-tui       # Build and run TUI
make dev-gui       # Run Electron in dev mode
make build         # Build all components
make test          # Run all tests
make lint          # Run all linters
```

Ad-hoc: `go run ./cmd/watchfire` or `go run ./cmd/watchfired`

### Linting & Formatting

| Component | Tools |
|-----------|-------|
| **Go** | `gofmt`, `golangci-lint` |
| **Electron** | `prettier`, `eslint` |
| **Proto** | `buf lint` |

---

## CI/CD (GitHub Actions)

### On Pull Request

- Build all components
- Run all tests
- Run all linters
- Block merge if any fail

### On Push/Merge to Main

1. Run build, test, lint
2. Read `version.json` for version number
3. Create or replace draft release for that version
4. Build artifacts:
   - `Watchfire.dmg` — Universal installer (GUI + CLI + daemon)
   - `watchfire-cli-darwin-arm64` — CLI only
   - `watchfire-cli-darwin-amd64` — CLI only
   - `watchfired-darwin-arm64` — Daemon only
   - `watchfired-darwin-amd64` — Daemon only
5. Attach artifacts to draft release
6. Generate release notes from `CHANGELOG.md`

### Version Tracking

**version.json:**
```json
{
  "version": "0.1.0",
  "codename": "Ember"
}
```

**CHANGELOG.md:**
```markdown
# Changelog

## [0.1.0] Ember — 2026-XX-XX

### Added
- Initial release
- Daemon with PTY management
- CLI/TUI client
- Electron GUI
- ...
```

### Publishing Release

Manual step: Edit draft release → Publish

---

## Packaging & Installation

### macOS DMG (Watchfire.dmg)

- Contains `Watchfire.app` (Electron GUI)
- On first launch, app installs CLI tools:
  - Copies `watchfire` to `/usr/local/bin/`
  - Copies `watchfired` to `/usr/local/bin/`
  - Prompts user for permission if needed

### Homebrew

```bash
brew tap watchfire/tap
brew install watchfire   # Installs CLI + daemon
```

Tap repo: `watchfire/homebrew-tap` (separate repo, auto-updated on release)

### Distribution

| Channel | What |
|---------|------|
| **GitHub Releases** | DMG, CLI binaries, daemon binaries |
| **Homebrew** | CLI + daemon |
| **Mac App Store** | Future consideration |

### Auto-Update

| Component | Mechanism |
|-----------|-----------|
| **GUI** | `electron-updater` (checks GitHub releases) |
| **CLI/Daemon** | Custom updater (checks GitHub releases, downloads, replaces) |

Update flow: GUI updates itself, then updates CLI/daemon binaries

---

## Constraints

When implementing, follow these rules:

1. **No premature abstraction**. Write simple, obvious code first.
2. **No alternative approaches**. Use the tech stack specified above. Do not suggest alternatives.
3. **Test manually**. Describe how to test before writing code.
4. **Ask if unclear**. If the architecture doesn't specify something, ask—don't guess.
