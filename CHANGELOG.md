# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
- `AgentService` gRPC implementation with 8 RPCs (StartAgent, StopAgent, GetAgentStatus, SubscribeScreen, SubscribeRawOutput, GetScrollback, SendInput, Resize)
- Agent message types in proto (AgentStatus, ScreenBuffer, RawOutputChunk, ScrollbackLines, etc.)
- Task completion flow: agent writes `status: done` → watcher detects → daemon stops agent → auto-merge/cleanup
- Wired agent manager into daemon server and system tray

### Changed
- CLI help commands reordered alphabetically
- Migrated golangci-lint configuration to v2 format

### Fixed
- `formatTaskNumber` in `config/paths.go` — int-to-string conversion produced wrong results
- Resolved all 74 golangci-lint issues across the codebase (var-naming, noctx, errcheck, gofmt, doc comments, octal literals, etc.)

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
