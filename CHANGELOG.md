# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

#### TUI — Interactive Split-View Interface
- `watchfire` (no args) — Launch interactive TUI with split-view layout
- Split-panel layout: task list (left) + agent terminal (right), draggable divider
- Left panel tabs: Tasks (grouped by status), Definition (read-only + $EDITOR), Settings (inline form)
- Right panel tabs: Chat (live agent terminal), Logs (session history viewer)
- Full task management: add, edit, status transitions (draft/ready/done), soft delete
- Agent terminal with raw PTY streaming, keyboard input forwarding, and scrollback
- Agent control: start/stop, chat/task/start-all/wildfire modes with auto-start chat
- Wildfire phase display (Execute/Refine/Generate) in terminal panel
- Agent issue banners: auth required and rate limit detection with recovery actions
- Help overlay (Ctrl+h) with full keybinding reference
- Task add/edit overlays with multiline textarea fields
- Context-sensitive status bar with key hints and connection indicator
- Keyboard navigation: vim-style (j/k), arrows, tab switching (1/2/3), panel focus (Tab)
- Mouse support: click to focus/select, scroll, drag divider to resize panels
- Auto-reconnect to daemon with "Disconnected" indicator and 3-second retry
- Minimum terminal size detection (80x24)
- ANSI 256 color styling with light/dark terminal adaptation (lipgloss.AdaptiveColor)

#### CLI — Project Configuration Commands
- `watchfire definition` (alias: `def`) — Edit project definition in external editor ($EDITOR, $VISUAL, vim, vi, nano)
- `watchfire settings` — Configure project settings interactively (name, color, branch, automation toggles)

#### System Tray — Colored Project Icons
- Agent menu items now display a dynamically generated colored circle icon based on the project's hex color setting
- Icons are cached to avoid regeneration for the same color

#### CLI — Self-Healing Project Index
- Project-scoped CLI commands now auto-register the project in `~/.watchfire/projects.yaml` if missing
- Automatically updates the project path in the global index if the project directory was moved
- Reactivates archived projects when a CLI command is run from the project directory

#### CLI — Daemon Commands
- `watchfire daemon start` — Start the daemon (detects if already running)
- `watchfire daemon status` — Show daemon host, port, PID, uptime, and active agents (project, mode, task)
- `watchfire daemon stop` — Stop the daemon via SIGTERM with shutdown polling

#### CLI — Agent Commands
- `watchfire agent start [task-number|all]` — Start agent session in chat, task, or start-all mode
- `watchfire agent generate definition` (alias: `gen def`) — Generate/update project definition
- `watchfire agent generate tasks` (alias: `gen tasks`) — Generate tasks from project definition
- `watchfire agent wildfire` — Autonomous three-phase loop (execute → refine → generate)
- Terminal attach mode with raw PTY streaming, resize handling (SIGWINCH), and Ctrl+C forwarding
- Automatic re-subscription for chaining modes (start-all, wildfire) when tasks complete

#### CLI — gRPC Client
- Daemon connection helper (`internal/cli/grpc.go`) with auto-discovery via `~/.watchfire/daemon.yaml`

#### Daemon — Agent Infrastructure
- Agent manager (`internal/daemon/agent/manager.go`) with lifecycle tracking and mode support (chat, task, start-all, wildfire, generate-definition, generate-tasks)
- Agent process management (`internal/daemon/agent/process.go`) with PTY spawning via `github.com/creack/pty`
- Terminal emulation via `github.com/hinshun/vt10x` with screen buffer and scrollback
- Git worktree management (`internal/daemon/agent/worktree.go`) — create, merge, and remove worktrees per task
- macOS sandbox integration (`internal/daemon/agent/sandbox.go`) via `sandbox-exec`
- Agent prompts system (`internal/daemon/agent/prompts/`) with embedded templates:
  - Base Watchfire context prompt
  - Task mode prompts (system + user)
  - Wildfire refine/generate phase prompts
  - Generate definition/tasks mode prompts
- Agent state persistence (`~/.watchfire/agents.yaml`) — tracks running agents across CLI/daemon boundary
- Signal file detection for phase completion (refine_done.yaml, generate_done.yaml, definition_done.yaml, tasks_done.yaml)
- `AgentService` gRPC implementation with 10 RPCs (StartAgent, StopAgent, GetAgentStatus, SubscribeScreen, SubscribeRawOutput, GetScrollback, SendInput, Resize, SubscribeAgentIssues, ResumeAgent)
- Agent message types in proto (AgentStatus, ScreenBuffer, RawOutputChunk, ScrollbackLines, etc.)
- Task completion flow: agent writes `status: done` → watcher detects → daemon stops agent → auto-merge/cleanup
- Wired agent manager into daemon server and system tray

#### Daemon — Agent Issue Detection
- Real-time detection of auth errors and rate limits in PTY output (`internal/daemon/agent/issue.go`)
- Auth error detection: API 401 errors, OAuth token expiration, `/login` prompts
- Rate limit detection: 429 errors, "hit your limit" messages, with reset time parsing
- Issue subscriptions via `SubscribeAgentIssues` streaming RPC
- `ResumeAgent` RPC to clear rate limit cooldown
- `GetAgentStatus` now includes current blocking issue if any
- Agent continues running during issues (user can run `/login` or wait for rate limit reset)
- Pattern matching with ANSI escape code stripping for reliable detection

#### Daemon — Session Logs
- Session log system (`internal/config/log.go`, `internal/models/log.go`) — writes YAML-header log files per agent session
- Log storage at `~/.watchfire/logs/<project_id>/<logID>.log`
- `LogService` gRPC service with `ListLogs` and `GetLog` RPCs
- Proto messages: `LogEntry`, `LogList`, `LogContent`, `ListLogsRequest`, `GetLogRequest`

#### Daemon — Resilience Improvements
- Watcher re-watch on chain: chained agents (wildfire/start-all) re-watch the project to pick up directories created during earlier phases
- Polling fallback: task-mode agents poll task YAML every 5s as safety net for missed watcher events (kqueue overflow, late directory creation)
- Task number sync (`SyncNextTaskNumber`): scans task files and updates `next_task_number` when agents create task files directly
- ANSI screen content: `ScreenBuffer` now includes full ANSI SGR rendering for richer terminal display
- Initial screen snapshot sent on `SubscribeScreen` connect so TUI sees current state immediately
- PTY size passthrough: `StartAgent` accepts rows/cols from client, forwarded to agent PTY
- Stale project index cleanup: removes duplicate entries for the same path in `projects.yaml`

### Changed
- CLI help commands reordered alphabetically
- Migrated golangci-lint configuration to v2 format
- `StartAgent` now stops any existing agent before starting a new one (instead of returning existing)
- `StopAgent` from user (CLI/TUI) explicitly prevents wildfire/start-all chaining via `StopAgentByUser`
- CLI Ctrl+C in wildfire/start-all modes now stops the agent and breaks the re-subscribe loop (instead of forwarding Ctrl+C to agent)
- `MergeWorktree` returns `(bool, error)` — skips merge when no file differences exist (detects via `git diff --stat`)
- `onTaskDoneFn` returns bool to control whether chaining continues after task completion
- Architecture doc updated with worktree resilience, task lifecycle, TUI layout, and watcher improvements

### Fixed
- `formatTaskNumber` in `config/paths.go` — int-to-string conversion produced wrong results
- Resolved all 74 golangci-lint issues across the codebase (var-naming, noctx, errcheck, gofmt, doc comments, octal literals, etc.)
- Stale branch handling: worktree creation now deletes existing branch and recreates from current HEAD (instead of reusing old commit)
- Merge conflict recovery: `git merge --abort` on failure restores clean working directory
- Chain stop on merge failure: prevents cascading failures in wildfire/start-all modes

## [0.1.0] Ember - 2026-02-04

### Added

#### Development Environment
- Go module initialization (`github.com/watchfire-io/watchfire`)
- Makefile with commands: `dev-daemon`, `dev-tui`, `build`, `test`, `lint`, `proto`, `clean`
- golangci-lint configuration (`.golangci.yml`)
- Air hot reload configuration for daemon (`.air.toml`)
- EditorConfig for consistent formatting (`.editorconfig`)
- Version tracking (`version.json`) with version 0.1.0 codename "Ember"

#### Daemon (`watchfired`)
- gRPC server with dynamic port allocation
- Daemon discovery via `~/.watchfire/daemon.yaml`
- Project manager with CRUD operations
- Task manager with CRUD and soft delete/restore
- File watcher with debouncing for real-time updates
- Graceful shutdown on SIGINT/SIGTERM
- System tray icon and menu via `github.com/getlantern/systray`
  - Shows daemon status header and listening port
  - Pre-allocated agent slots (hidden until agents active)
  - "No active agents" placeholder
  - "Open GUI" stub action
  - "Quit" to shut down daemon
  - Tooltip: "Watchfire — {n} projects, {m} active"
  - `-foreground` flag bypasses tray (for hot reload / dev)

#### CLI (`watchfire`)
- `watchfire version` - Display version information
- `watchfire init` - Initialize new project (git init, .watchfire/ structure, .gitignore)
- `watchfire task list` - List tasks grouped by status (Draft, Ready, Done)
- `watchfire task list-deleted` - List soft-deleted tasks
- `watchfire task add` - Create new task (interactive prompts)
- `watchfire task <number>` - Edit existing task
- `watchfire task delete <number>` - Soft delete task
- `watchfire task restore <number>` - Restore soft-deleted task

#### Data Models
- Project configuration (`project.yaml`) with settings for auto-merge, auto-delete, auto-start
- Task files with status workflow (draft → ready → done)
- Global projects index (`~/.watchfire/projects.yaml`)
- Global settings (`~/.watchfire/settings.yaml`)

#### Proto Definitions
- `ProjectService` - Project CRUD operations
- `TaskService` - Task CRUD and bulk operations
- `DaemonService` - Daemon status and shutdown
