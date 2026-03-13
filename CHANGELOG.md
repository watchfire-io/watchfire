# Changelog

## [0.5.0] Ember

### Added

- Integrated terminal in the GUI — footer bar that expands into a resizable bottom panel with tabbed shell sessions via node-pty, Cmd+` toggle, Nerd Font support
- Version display in system tray menu below "Watchfire Daemon" header for easy version identification

### Fixed

- Status indicator dots in sidebar/dashboard now only pulse for projects with an autonomous agent (task, wildfire, start-all) — chat mode no longer triggers pulsing
- Dashboard project card X button overlapping chevron arrow on hover
- GUI crash ("Object has been destroyed") when PTY emits data after BrowserWindow is closed — `onData`/`onExit` callbacks now check `isDestroyed()` before sending IPC messages

## [0.4.0] Ember

### Fixed

- Daemon crash (exit code 2) when macOS notification fires outside `.app` bundle — `hasAppBundle()` pre-check and `@try/@catch` prevent `NSInternalInconsistencyException`
- Agent subprocess inheriting `CLAUDECODE` env var — stripped from child process environment to prevent Claude Code nesting issues
- Project color not updating in sidebar/dashboard after changing in settings — optimistic local store update now re-renders immediately
- Tasks not updating in GUI when chat agent creates them on disk — removed flawed shallow comparison that suppressed store updates from protobuf-es objects
- CLI wildfire/start-all crashing with "stream error: no agent running" during task transitions — stream errors are now handled gracefully in chaining mode
- System tray concurrent update crashes — serialized Cocoa API calls through a single goroutine with debouncing
- Agent manager deadlock when `onChangeFn` calls `ListAgents()` during state persist — moved callback to a goroutine

## [0.3.0] Ember

### Added

- Daemon health check (`Ping` RPC) for lightweight connection verification

### Fixed

- Daemon startup race condition — `daemon.yaml` is now written only after the gRPC server is accepting connections, eliminating "connection refused" errors on startup
- GUI no longer shows "Failed to fetch" when starting tasks immediately after daemon launch
- TUI no longer shows "connection refused" on first connect attempt
- GUI settings page (and all views) no longer vanish during brief daemon disconnects — disconnect message now shows as an overlay
- CLI and GUI daemon startup now verify port readiness before proceeding

## [0.2.0] Ember

### Added

- Agent memory file (`.watchfire/memory.md`) — agents can persist project-specific knowledge (conventions, preferences, patterns) across sessions

### Changed

- Removed configurable "default branch" setting — tasks now merge into whatever branch is currently checked out in the project root

### Fixed

- macOS notifications now display the Watchfire icon instead of a generic system icon
- GUI terminal no longer duplicates output in an infinite loop when an agent stops

## [0.1.3] Ember

### Fixed

- Homebrew Cask download URL now includes `-universal` suffix to match the actual DMG release asset name, fixing `brew install --cask watchfire`
- GUI now polls tasks and agent status continuously so the interface updates when task files change
- GUI project settings color changes now apply immediately without needing a restart

## [0.1.2] Ember

### Fixed

- GUI auto-updater no longer fails with `ENOENT: app-update.yml` — the `--prepackaged` electron-builder flag skips generating this file; it is now created explicitly in the build workflow

## [0.1.1] Ember

### Fixed

- GUI now detects Homebrew-installed binaries in `/opt/homebrew/bin/` on Apple Silicon Macs
- CLI installer checks both `/opt/homebrew/bin` and `/usr/local/bin` before prompting to install
- Daemon discovery finds `watchfired` in Homebrew prefix when Electron's PATH is limited

## [0.1.0] Ember

Watchfire orchestrates coding agent sessions (starting with Claude Code) based on project specs and tasks. Define what you want built, break it into tasks (or have agents do it), and let agents work through them autonomously — with full visibility into what's happening. Or just turn on wildfire mdoe and let you agents do it all for you.

### Daemon (`watchfired`)

The always-on backend that manages everything:

- **Agent orchestration** — Spawns coding agents in sandboxed PTYs with terminal emulation, one task per project, multiple projects in parallel
- **Git worktree isolation** — Each task runs in its own worktree (`watchfire/<task_number>`), auto-merged back on completion with conflict detection
- **macOS sandbox** — Agents run inside `sandbox-exec` with restricted filesystem/network access
- **File watching** — Real-time detection of task completion and phase signals via fsnotify, with polling fallback for reliability
- **Session logs** — Every agent session recorded to `~/.watchfire/logs/` with YAML metadata
- **System tray** — Menu bar icon showing daemon status, active agents with colored project dots, and quick stop/quit actions
- **Secrets folder** — `.watchfire/secrets/instructions.md` for providing agents with external service credentials and setup instructions, injected into the system prompt
- **Issue detection** — Monitors agent output for auth errors (401, expired tokens) and rate limits (429), with real-time notifications to clients
- **gRPC + gRPC-Web** — Single port serves both native gRPC (CLI/TUI) and gRPC-Web (Electron GUI)
- **Auto-discovery** — Writes connection info to `~/.watchfire/daemon.yaml` so clients find it automatically

### CLI (`watchfire`)

Project-scoped command-line interface:

- `watchfire init` — Initialize a project (git setup, `.watchfire/` structure, `.gitignore`, interactive config)
- `watchfire task add|list|edit|delete|restore` — Full task CRUD with soft delete/restore
- `watchfire definition` — Edit project definition in `$EDITOR`
- `watchfire settings` — Configure project settings interactively
- `watchfire agent start [task|all]` — Start agent in chat, single-task, or run-all-ready mode
- `watchfire agent wildfire` — Autonomous three-phase loop: execute ready tasks → refine drafts → generate new tasks → repeat
- `watchfire agent generate definition|tasks` — One-shot generation commands
- `watchfire daemon start|status|stop` — Daemon lifecycle management
- `watchfire update` — Self-update from GitHub Releases
- **Terminal attach** — Raw PTY streaming with resize handling and Ctrl+C forwarding
- **Self-healing project index** — Auto-registers projects, updates moved paths, reactivates archived projects

### TUI (`watchfire` with no args)

Interactive split-view terminal interface:

- **Split layout** — Task list (left) + agent terminal (right) with draggable divider
- **Left panel tabs** — Tasks (grouped by status), Definition (read-only + `$EDITOR`), Settings (inline form)
- **Right panel tabs** — Chat (live agent terminal), Logs (session history viewer)
- **Agent modes** — Chat, task, start-all, and wildfire with phase display (Execute/Refine/Generate)
- **Issue banners** — Auth required and rate limit detection with recovery guidance
- **Keyboard navigation** — Vim-style (`j/k`), arrows, tab switching (`1/2/3`), panel focus (`Tab`)
- **Mouse support** — Click to focus/select, scroll, drag divider to resize
- **Task management** — Add, edit, status transitions (draft/ready/done), soft delete — all from the keyboard
- **Auto-reconnect** — Reconnects to daemon on disconnect with status indicator
- **Help overlay** — `Ctrl+h` for full keybinding reference

### GUI (Electron)

Multi-project desktop application:

- **Dashboard** — Project cards with task counts, status dots, active task display
- **Project view** — Tasks, Definition, Secrets, Trash, Settings tabs with collapsible right panel (Chat, Branches, Logs)
- **Add Project wizard** — Three-step flow: project info → git config → definition
- **Branch management** — View, merge, delete, and bulk-manage worktree branches
- **Remove project** — Unregister projects from sidebar context menu, dashboard card, or settings tab (stops agents, no files deleted)
- **Agent terminal** — Live streaming via gRPC-Web with input support
- **Global settings** — Defaults, appearance (system/light/dark theme), agent path config, update preferences
- **Daemon lifecycle** — Auto-restarts daemon if it dies, handles binary updates gracefully

### Agent Modes

| Mode | Description |
|------|-------------|
| **Chat** | Free-form conversation with the agent at project root |
| **Task** | Work on a specific task in an isolated worktree |
| **Start All** | Run all ready tasks in sequence, one at a time |
| **Wildfire** | Fully autonomous loop: execute → refine → generate → repeat until done |
| **Generate Definition** | One-shot: agent analyzes codebase and writes project definition |
| **Generate Tasks** | One-shot: agent reads definition and creates task files |

### Task Lifecycle

```
draft → ready → done (success ✓ or failure ✗)
```

- Tasks are YAML files in `.watchfire/tasks/`
- Agents detect completion by writing `status: done` to the task file
- Daemon auto-merges the worktree branch, cleans up, and chains to the next task
- Merge conflicts abort the chain to prevent cascading failures

### Build & Distribution

- **macOS DMG** — Universal binary (arm64 + amd64) with GUI, CLI, and daemon bundled
- **Code signing & notarization** — Developer ID certificate with hardened runtime
- **Homebrew** — `brew tap watchfire/tap && brew install watchfire`
- **Auto-update** — GUI via `electron-updater`, CLI via `watchfire update`, daemon checks on startup
- **CI/CD** — GitHub Actions: lint, test, build matrix (arm64/amd64), sign, notarize, draft release
