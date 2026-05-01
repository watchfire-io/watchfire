# Changelog

## [4.0.0] Beacon

### Added

- **Last-activity timestamp on dashboard cards** — every `ProjectCard` in the dashboard now carries a third item in its meta row (after branch and folder) showing when the project was last touched: an `Activity` lucide icon at 11px to match the sibling icons, then the literal `Active now` in `var(--wf-success)` green when an autonomous agent is currently running on the project, or `Active <1m ago` / `Active 5m ago` / `Active 4h ago` / `Active 3d ago` / `Active 2mo ago` / `Active 1y ago` in muted text when it isn't. The source-of-truth timestamp is the most recent `updated_at` across non-deleted tasks for the project (the agent's start time only matters as a tiebreaker, but since "running" already maps to the literal `Active now` string we never need to actually compare to it). Projects with zero non-deleted tasks render no segment at all so empty cards stay clean. The relative-time formatter is hand-rolled in a new `gui/src/renderer/src/lib/relative-time.ts` (no new dependency, no `Intl.RelativeTimeFormat` round-trip): a small `timestampToMs(ts)` helper that walks the protobuf `{seconds: bigint, nanos: number}` shape into a JS millisecond value, paired with a `relativeTime(ms, now?)` formatter that floors to the largest unit that fits (`<1m` → `m` → `h` → `d` → `mo` → `y`) and clamps deltas to ≥ 0 so a momentary clock skew never produces "Active in 3s" output. Re-derivation rides the dashboard's existing fetch tick — `useTasksStore.fetchTasks` is the same call that already powers the task counts and the next-up label, so the activity segment refreshes for free on every dashboard poll cycle without adding a per-card 1 Hz interval (that pattern is reserved for task 0040's elapsed badge). Sub-minute precision is intentionally absent — if the user-visible string is wrong by 30 seconds the next dashboard tick (every 3–10 s) will catch it. The `<1m ago` bucket covers the still-cooling-down case after an agent stops, which is the only window where the imprecise "now vs ago" boundary would otherwise show

- **Live PTY last-line preview on dashboard cards** — when an agent is running on a project, its `ProjectCard` in the dashboard now shows the latest non-blank line of the agent's terminal at the very bottom of the card in monospace muted text, single-line truncated with `text-overflow: ellipsis`. The card pulses with what the agent is actually doing right now (a tool call, a file path being edited, the latest assistant message) without forcing the user into the full project view. Wiring goes through a new singleton subscription manager in `gui/src/renderer/src/stores/agent-preview-store.ts`: the first card to mount for a given `projectId` opens the daemon's `AgentService.SubscribeScreen` stream, subsequent consumers (e.g. multiple cards for the same project, or a future terminal panel reusing the same screen-buffer feed) ref-count onto the same `AbortController` so there is no duplicate gRPC stream per card. The store stores plain text in `previews[projectId]` — `lastNonBlank()` walks the screen buffer's `lines` array bottom-to-top until it finds a row whose `.trim()` is non-empty, then trims and caps at 500 chars before render. UI updates are throttled to <= 4 Hz per project: the first emission lands immediately, any further emissions inside the 250 ms window are coalesced into a single trailing-edge update via a single `setTimeout`, so a screen that scrolls at 60 Hz only re-renders cards 4×/sec. The dashboard consumer side lives in `gui/src/renderer/src/hooks/useAgentPreview.ts` — `useAgentPreview(projectId, enabled)` calls `acquireAgentPreview` only while `enabled === !!agentStatus.isRunning`, so navigating away from the dashboard (or the agent stopping) unmounts the consumer cleanly, decrements the ref count, and aborts the underlying stream when the last consumer leaves. ANSI/SGR codes never reach the renderer because the daemon already serialises the screen buffer's `lines` field as plain cell characters in `internal/daemon/agent/process.go:snapshotScreen`. The preview is hidden entirely when the agent is not running, and an empty preview string also renders nothing so the card layout never reserves an empty row for it

- **Dashboard grid/list layout toggle** — the dashboard now exposes a two-icon toggle (lucide `LayoutGrid` / `Rows3`) in the header that flips the project overview between the existing card grid and a new dense one-row-per-project list. With 10+ projects the card grid wastes vertical space; the list mode renders each project as a single ~46px row — status dot (pulsing if an autonomous agent is running) → project name → branch → either the running `AgentBadge` plus the agent's current task title, or the live counts (`N todo · N in dev · N done · N failed`) — followed by a remove button on hover and a chevron. Failed-state projects pick up a red left-border accent in row mode so a busted task is still spottable at a glance. Per-project sorting via `@dnd-kit` is preserved in both layouts (`rectSortingStrategy` for grid, `verticalListSortingStrategy` for list). The selection persists in `localStorage` under `wf-dashboard-layout` (`'grid' | 'list'`, default `'grid'`) and is read back synchronously at mount so the user never sees a flash of the wrong layout. The per-project rendering for the new layout lives in its own `gui/src/renderer/src/views/Dashboard/ProjectRow.tsx` rather than parameterising `ProjectCard` with a `dense` prop — the two layouts have meaningfully different shapes and the prop sprawl was not worth it. Filter chips and auto-sort (tasks 0037/0038) and the richer running/idle metadata (tasks 0039–0042) hang off the same store reads as the card, so when those land they apply identically across both layouts

### Fixed

- **GUI: switching projects silently killed every running shell in the bottom panel** — opening project A, spawning a couple of long-lived shells (e.g. `tail -f`, `npm run dev`), then clicking project B in the sidebar destroyed every PTY session — returning to A showed an empty terminal panel with no scrollback and no live processes. Root cause was a documented but unwanted behaviour, not a regression: `gui/src/renderer/src/views/ProjectView/ProjectView.tsx:104-109` ran `useTerminalStore.getState().destroyAllSessions()` from a cleanup effect on `projectId` change, and the Cmd+` toggle (`ProjectView.tsx:88-102`) treated "panel currently shows tabs" as a signal to call the same `destroyAllSessions()` — so collapsing the panel also nuked the shells. The original architecture line in `ARCHITECTURE.md:907` codified this: *"Sessions are cleaned up on project switch or app quit."* For Beacon this rule is flipped: PTY sessions live in a global pool keyed by `projectId` and survive every navigation event short of the user explicitly closing the tab (X button) or quitting Electron. Concretely: (a) the `destroyAllSessions`-on-project-switch effect is gone; (b) Cmd+` now toggles a non-destructive `panelCollapsed` flag via the new `expandPanel()` / `collapsePanel()` actions in `gui/src/renderer/src/stores/terminal-store.ts`, falling through to `createSession` only when the current project has zero sessions; (c) `terminal-store.ts` gains a `destroyProjectSessions(projectId)` action that the projects store calls from `removeProject` so deleting a project still cleans up its shells but never bleeds into other projects; (d) `BottomPanel.tsx` is now a single always-mounted container with three CSS-driven states — empty footer (no sessions for this project), collapsed-with-chip (`var(--wf-success)` dot + `N shells` label, pulsing if any session has produced output in the last 2s), and expanded panel (drag handle + tab bar filtered by `session.projectId === currentProjectId` + content area) — and crucially renders **all** session `TerminalTab`s globally with a `visible={session.id === activeSessionId && session.projectId === projectId}` flag so xterm.js Terminal instances and their scrollback survive React reconciliation when the user switches projects; (e) the chevron-down button in the expanded tab bar (previously bound to `destroyAllSessions`) now calls `collapsePanel()` instead, while the per-tab X still destroys that single session as before; (f) `panelOpen` was previously dead state — read from localStorage at init but never tied to actual visibility — and is now the source of truth for collapse, persisted as `wf-terminal-panel-open` and re-applied across remounts. Electron's `before-quit` handler in `gui/src/main/index.ts` already calls `destroyAllPtys()` from `pty-manager.ts`, so the global cleanup path on app quit is unchanged. Architecturally this flips the bottom panel from "scoped to the active project view" to "global session pool with per-project filtering," which is the only model where long-lived dev servers and tail loops can coexist with project hopping. `ARCHITECTURE.md:907` is updated to match the new contract

### Changed

## [3.0.0] Blaze

### Added

- **GitHub Copilot CLI backend** — `copilot` (https://github.com/github/copilot-cli) ships as a fifth first-class backend alongside Claude Code, OpenAI Codex, opencode, and Gemini CLI, registered in the backend registry and selectable per project or per task. The session runs with `--allow-all` (yolo mode) and receives the composed Watchfire system prompt as `AGENTS.md` in a per-session `COPILOT_HOME` directory referenced via `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`, while the user's real `~/.copilot/{config.json,mcp-config.json,session-store.db}` are symlinked in so existing GitHub login, MCP config, and session history are reused. Transcript discovery walks the per-session `session-state/**/events.jsonl` tree and renders events into the same User/Assistant/Tool format as the other backends. No wiring changes in the manager, sandbox, proto, or UX surfaces — they already iterate the backend registry generically

### Fixed

- **`watchfire update` failed across filesystems on Linux (#25)** — a Fedora user reported `failed to update CLI: install new binary: rename /tmp/watchfire-update-2058306240 /home/madoke/.local/bin/watchfire: invalid cross-device link` when upgrading from 0.9.0 to 1.0.0. Root cause: `updater.DownloadAsset` staged the downloaded binary under `os.TempDir()` (on Fedora/Ubuntu `/tmp` is a separate `tmpfs` filesystem) and `updater.ReplaceBinary` finished the install with `os.Rename(newPath, destPath)`, which on Linux boils down to the `renameat2(2)` syscall and returns `EXDEV` across filesystems. Fix: `DownloadAsset` now takes a `preferDir` argument and stages the download inside the install directory itself (e.g. `~/.local/bin/.watchfire-update-XXXXXX`) so the final rename is always same-filesystem and therefore atomic; the CLI `update` command resolves the install dirs for both the CLI (`os.Executable()` + `EvalSymlinks`) and the daemon (`findDaemonBinary` + `EvalSymlinks`) up front and passes each one as the preferred staging dir. As belt-and-suspenders, `ReplaceBinary` keeps its atomic-rename semantics even if a future caller ever stages the binary elsewhere: it detects the cross-dir case, copies+fsyncs the staged binary into the install dir, removes the original, and finishes with a same-dir rename — the final swap is still a single atomic operation so a concurrent `watchfire` invocation never observes a half-written binary. Exec perms (`0o755`) are applied before the rename so the final binary lands already executable. Regression test in `internal/updater/updater_test.go` covers the same-dir fast path, the cross-dir EXDEV fallback (no leftover staging file, original temp cleaned up), exec-perm preservation, and the `preferDir` fallback to `os.TempDir()` when the install dir is unusable. The daemon binary update path goes through the same `DownloadAsset`/`ReplaceBinary` pair so it inherits the fix automatically. Fixes #25
- **Task list rotated in projects with many tasks (#28)** — a Fedora user with 47 tasks reported the task list starting at `0017`, running to `0047`, then wrapping back to `0001`. Root cause: the task manager's `ListTasks` sorted primarily by the legacy `position` field and only used `task_number` as a tiebreaker, and the TUI re-sorted within each status group with the same key. When a project had tasks split across status groups (16 done + 31 ready in the reporter's case), the section headers — `Ready` renders before `Done` in the TUI, and `In Development` before `Done` in the GUI — placed `0017…0047` above `0001…0016`, producing the "rotation" despite position and task_number matching. Sorting is now canonical across every surface: the task manager returns tasks strictly descending by `task_number` (newest first), the CLI `task list`, TUI task list, and GUI TasksTab all rely on that order without re-sorting, and the legacy `position` field is ignored at read time. Regression test in `internal/daemon/task/manager_test.go` seeds 25 mixed-status tasks with shuffled `position` values and asserts the returned order is strictly descending by `task_number`. Fixes #28
- **GUI prompted to update CLI on every launch (#30)** — the `cli-installer` version check compared the installed CLI's version string against `app.getVersion()` with strict `!==`, which tripped on trailing whitespace, pre-release suffixes, and ANSI hyperlinks that escaped the old CSI-only stripper; on Linux it also read the wrong binary because the search-dir order (`/usr/local/bin` → `~/.local/bin`) put the system path ahead of the user path that `installCLI()` actually writes to, so a stale 2.0.0 binary in `/usr/local/bin` kept re-triggering the prompt even after the user clicked Install. Version parsing now lives in a pure `gui/src/main/version.ts` module with a broader ANSI stripper (CSI + OSC + other ESC), a proper semver-aware comparator (trims whitespace, drops leading `v`, ignores build metadata), and the Linux/macOS search order now matches the install target with a PATH fallback (`command -v`) for rpm/deb/Linuxbrew installs. Regression tests in `gui/src/main/version.test.mjs` cover the representative Linux and macOS outputs (run with `node --test`)
- **Newly-installed agents invisible in GUI/TUI pickers (#29)** — a Fedora user installed Codex while Watchfire was running and neither the TUI nor the GUI surfaced it in the agent picker until `project.yaml` was hand-edited. The architectural root cause was that agent discovery had been implicitly coupled to binary availability — a backend whose `ResolveExecutable` returned an error could get hidden entirely, so a freshly-installed CLI stayed invisible until the resolver happened to find it and the UI re-enumerated. The registry is now the sole source of truth for pickers: `SettingsService.ListAgents` always returns every registered backend (Claude Code, Codex, Gemini, opencode, Copilot) and exposes a new `AgentInfo.available` boolean as a display-time hint only. GUI (`SettingsTab`, `AgentPathsSection`, `DefaultsSection`, `StepAgent`) and TUI (`settings.go`, `taskform.go`, `globalsettings.go`) pickers now append a `(not installed)` suffix to unavailable agents instead of omitting them, so users can select a backend they're mid-installing and get a clear error at spawn time rather than a silent absence. Linux fallback paths for Claude, Codex, Gemini, opencode, and Copilot were simultaneously broadened to cover `/usr/bin/<name>` (distro packages like Fedora `dnf`) and `~/.npm-global/bin/<name>` where they were missing. Regression test in `internal/daemon/server/settings_service_test.go` registers a fake backend whose binary always fails to resolve and asserts it still appears in `ListAgents` with `Available=false`. Fixes #29

### Migration

- Existing projects and tasks are unaffected — Copilot is purely additive. To opt a project into Copilot, switch `project.default_agent` (or a specific task's `agent` field) to `copilot`. A custom Copilot binary path can be set via the global settings UI or by hand in `~/.watchfire/settings.yaml`

## [2.0.1] Spark

### Fixed

- **Silently discarded work when Codex (or any agent) forgot to commit** — if an agent edited files in the worktree and set `status: done` without running `git commit`, Watchfire saw no diff on the branch, skipped the merge, and auto-deleted the branch and worktree — losing everything the agent did. `MergeWorktree` now runs `git add -A && git commit --no-verify` inside the worktree as a safety net before the diff check, so uncommitted edits are always captured even when the agent skips the commit step
- **Codex commit instruction not emphatic enough** — the base Watchfire system prompt already tells agents to commit before marking a task done, but Codex didn't consistently follow it. Codex sessions' per-session `AGENTS.md` now includes an additional, explicit `CRITICAL: Commit before marking a task done` addendum at the end, making the rule the last thing Codex reads before starting work

## [2.0.0] Spark

### Added

- **Pluggable agent backend interface** — new `AgentBackend` interface in `internal/daemon/agent/backend/` lets any CLI coding agent be plugged into Watchfire (executable resolution, command construction, sandbox extras, system-prompt delivery, transcript discovery and formatting)
- **OpenAI Codex backend** — Codex ships as a first-class backend alongside Claude Code, registered in the backend registry and selectable per project
- **opencode backend** — `opencode` (https://opencode.ai) ships as a first-class backend alongside Claude Code and OpenAI Codex, registered in the backend registry and selectable per project. No wiring changes in the manager, sandbox, or UX surfaces — they already iterate the backend registry generically
- **Gemini CLI backend** — Google's `gemini` (https://github.com/google-gemini/gemini-cli) ships as a first-class backend alongside Claude Code, OpenAI Codex, and opencode, registered in the backend registry and selectable per project
- **Agent picker in `watchfire init`** — init wizard prompts for the coding agent to use, seeding `project.default_agent` in `project.yaml`
- **Agent selector in project settings (TUI)** — settings tab exposes a backend picker so the agent can be switched after init
- **Agent selector in project settings (GUI)** — Electron settings tab exposes a backend picker populated from the daemon registry via the new `SettingsService.ListAgents` RPC, bringing the GUI to parity with the TUI settings tab
- **Global settings UI for agent paths** — new settings overlay for registering custom binary paths per backend and choosing the global default agent, including an "Ask per project" option that forces `watchfire init` to prompt every time
- **Per-task agent override** — each task can now pin itself to a specific backend via a new optional `agent` field in its YAML (`.watchfire/tasks/<n>.yaml`), letting users mix and match agents within a single project (e.g. Claude Code for architecture work, Codex for trivial edits, or rerunning a failed task under a different agent without touching project settings). The field is editable in the TUI task form (new cycling selector below the existing fields) and the GUI task modal (picker populated from the daemon's `SettingsService.ListAgents` RPC); both surfaces include a leading "Project default" entry that maps to the empty string, so the effective agent remains visible at a glance. An empty value defers to the project default, keeping existing tasks behaving exactly as before
- **Agent badge on task lists** — TUI (`internal/tui/tasklist.go`) and GUI (`gui/src/renderer/src/views/ProjectView/TasksTab/TaskItem.tsx`) render a compact agent badge next to a task's title whenever `task.agent` is set and differs from the project default; tasks that defer to the project default render no badge, keeping the list visually quiet for the common case
- **Per-session Codex home** — Codex receives the composed Watchfire system prompt via a per-session `CODEX_HOME` directory containing a generated `AGENTS.md`, keeping the user's real `~/.codex` as the source of auth/config that the session can write back to
- **Per-session opencode home** — Watchfire gives every opencode session its own `OPENCODE_CONFIG_DIR` + `OPENCODE_DATA_DIR` under `~/.watchfire/opencode-home/<session>/`, symlinking the user's real `~/.config/opencode` entries (auth, providers, agents, commands) for login reuse while keeping the Watchfire system prompt (`AGENTS.md`) and yolo permission config (`opencode.json` with `"permission": "allow"`) isolated per session
- **Per-session Gemini home** — Watchfire gives every Gemini session its own `GEMINI_SYSTEM_MD` pointing at `~/.watchfire/gemini-home/<session>/system.md`, keeping the Watchfire system prompt isolated per session while the user's real `~/.gemini/` continues to supply auth, settings, and hierarchical `GEMINI.md` context (Gemini has no per-session config-dir env var, so auth is read from the shared global dir)
- **Codex transcripts in the log viewer** — after a session completes, Watchfire discovers Codex's JSONL rollout under the session's `CODEX_HOME/sessions/` tree and renders it in the same User/Assistant format as the other backends
- **opencode transcripts in the log viewer** — after a session completes, opencode's per-message JSON files are collated into a single JSONL and rendered in the same User/Assistant format as the other backends
- **Gemini transcripts in the log viewer** — after a session completes, Watchfire discovers the session's chat log under `~/.gemini/tmp/<project_hash>/chats/session-*.jsonl` (or the legacy `logs.json` array) and renders it in the same User/Assistant format as the other backends

### Changed

- **Agent resolution chain** — the daemon now resolves the backend for each session through a four-step chain in `agent/manager.go:resolveBackend`: `task.agent` → `project.default_agent` → `settings.defaults.default_agent` → `claude-code`. Empty string at any level defers to the next, and chat / wildfire-refine / wildfire-generate sessions (which aren't scoped to a single task) skip the task step and start from the project default
- **Backend-owned transcript discovery** — JSONL transcript location and formatting moved out of the agent manager and into each backend's `LocateTranscript` / `FormatTranscript` implementation
- **Backend-contributed sandbox paths** — writable paths, cache patterns, and stripped env vars are now contributed by each backend via `SandboxExtras()` instead of being hardcoded in the sandbox layer

### Fixed

- **Agent auth failure when launched from GUI** — macOS GUI apps inherit a minimal environment (`PATH=/usr/bin:/bin:/usr/sbin:/sbin`) missing user-installed tool paths like `~/.local/bin`. This caused Claude Code to misroute API calls through "extra usage" billing instead of the user's subscription, producing spurious "You're out of extra usage" errors on task, Run All, and Wildfire modes while Chat mode worked fine. The Electron daemon spawner now resolves the user's full login-shell PATH before launching `watchfired`, and the macOS sandbox PATH enrichment adds `~/.local/bin` alongside `/opt/homebrew/bin` and `/usr/local/bin`
- **GUI: blank window on macOS** — production renderer is now served over a custom `app://` protocol instead of `file://`, restoring execution of the `crossorigin` ES-module entry bundle that Chromium was silently blocking. Added global `error` / `unhandledrejection` handlers in the renderer entry so any future module-init failure surfaces in the window instead of rendering blank, guarded module-top `localStorage` access in Zustand stores, and auto-opened DevTools in dev so residual issues are immediately visible

### Migration

- Existing projects without `default_agent` continue to use Claude Code — no action required
- Existing tasks without an `agent` field continue to use the project default — no action required
- Custom `codex`, `opencode`, and `gemini` binary paths can be configured via the new global settings UI or by hand in `~/.watchfire/settings.yaml`

## [1.0.0] Ember

### Added

- **JSONL transcript logs** — session logs now capture Claude Code's structured JSONL transcripts (`~/.claude/projects/`) instead of raw PTY scrollback, producing clean readable User/Assistant conversation logs
- **Transcript auto-discovery** — daemon locates Claude Code's transcript files by matching session names and copies them to `~/.watchfire/logs/` alongside the existing `.log` file

### Changed

- **Log viewer** — TUI and GUI now display formatted conversation transcripts (User/Assistant messages, tool call summaries) instead of garbled terminal output; falls back to PTY scrollback when no transcript is available

### Fixed

- **Agent restart loop** — wildfire/start-all now stops after 3 consecutive restarts of the same task and transitions to chat mode, preventing infinite loops on rate limits, crashes, or auth expiry
- **Sandbox blocks ~/Desktop projects** (#17) — macOS Seatbelt sandbox no longer denies read access to protected directories (Desktop, Documents, Downloads, etc.) when the project is located inside one of them
- **TUI task list scroll with 100+ tasks** (#18) — fixed height accounting for section header blank lines and scroll indicators that caused the last few tasks to be invisible
- **Install script "tmp_dir: unbound variable"** (#20) — moved temp directory variable to global scope so the cleanup trap can access it after function returns
- **Desktop always thinks CLI tools are outdated** (#21) — version check now strips ANSI escape codes before parsing and logs the actual error when the CLI binary can't be executed
- **Can't edit already created tasks in GUI** (#23) — task editor no longer resets form contents when background polling refreshes the task list
- **Duplicate terminal headers in GUI** — Chat panel no longer accumulates repeated Claude Code banners when switching projects or during wildfire phase transitions; terminal is properly cleared before each new subscription, and raw output subscriptions use their own abort map instead of colliding with screen subscriptions

## [0.9.0] Ember

### Added

- **Linux GUI** — AppImage and `.deb` packages for x64 Linux, built in GitHub Actions on `ubuntu-latest`. Bundled CLI + daemon binaries installed to `~/.local/bin` on first launch with `pkexec` fallback for admin privileges.
- **Windows GUI** — NSIS installer (`Watchfire-Setup-x.y.z.exe`) for x64 Windows, built in GitHub Actions on `windows-latest`. Bundled CLI + daemon binaries installed to `%LOCALAPPDATA%\Watchfire` on first launch with PowerShell elevation fallback.
- **Cross-platform auto-update for GUI** — `electron-updater` now checks `latest-linux.yml` (Linux) and `latest.yml` (Windows) in addition to `latest-mac.yml` (macOS). All three update manifests are generated and uploaded as release artifacts.
- **Linux GUI CI verification** — `gui-build-linux` job in CI workflow verifies Electron builds on `ubuntu-latest` on every PR.

### Changed

- **CLI installer is cross-platform** — `cli-installer.ts` detects OS and uses platform-appropriate install directories (`/usr/local/bin` on macOS, `~/.local/bin` on Linux, `%LOCALAPPDATA%\Watchfire` on Windows) with platform-specific privilege elevation (`osascript`, `pkexec`, PowerShell)
- **Window chrome adapts to platform** — macOS uses `hiddenInset` title bar with traffic lights; Linux and Windows use native window frames
- **electron-builder.yml** — added `linux` (AppImage + deb) and `win` (NSIS) targets with platform-specific `extraResources` for correct binary bundling (`.exe` on Windows)
- **Release workflow** — added `build-gui-linux` and `build-gui-windows` jobs; release job collects AppImage, deb, NSIS exe, and all update YAMLs as assets

## [0.8.0] Ember

### Fixed

- `watchfire update` now works on Windows — `stopDaemonForUpdate` uses `Kill()` instead of `SIGTERM`
- `findDaemonBinary()` handles Windows `.exe` extension correctly (was producing `watchfire.exed`)
- Build directory fallback uses platform-appropriate binary name

### Changed

## [0.7.0] Ember

### Added

- **Linux and Windows binaries in GitHub Releases** — release workflow now builds amd64 + arm64 for darwin, linux, and windows (6 platform targets total)
- **Cross-platform CI** — CI workflow verifies builds on macOS, Linux, and Windows
- **Install scripts** — `scripts/install.sh` (macOS/Linux) and `scripts/install.ps1` (Windows) for one-line installation from GitHub Releases
- **README.md** — updated with install instructions for all three platforms
- **No-CGO tray fallback** — daemon runs headless when built without CGO (enables Linux/Windows cross-compilation)

### Changed

## [0.6.0] Ember

### Added

- `watchfire chat` CLI command — dedicated command to start an interactive chat session with full project context
- **Cross-platform sandbox abstraction** — shared `SandboxPolicy` with platform-specific backends: macOS Seatbelt, Linux Landlock (kernel 5.13+) / bubblewrap (fallback)
- **Landlock sandbox** (Linux) — zero-dependency kernel-based sandboxing using `go-landlock`, daemon re-invokes itself as helper to apply restrictions before exec
- **Bubblewrap sandbox** (Linux) — namespace-based isolation with read-only root, writable project dir, hidden credential dirs
- `--sandbox <backend>` and `--no-sandbox` CLI flags on `run`, `chat`, `plan`, `generate`, `wildfire` commands
- Sandbox backend configurable per-project (`project.yaml`) and globally (`settings.yaml`)
- System tray icon abstraction for Linux — `setTrayIcon()` helper dispatches between macOS template icons and Linux standard icons
- **Windows build support** — CLI and daemon compile and run on Windows (unsandboxed, no POSIX signal dependencies)
- **Windows notifications** — toast notifications via `beeep` library
- Platform-aware updater asset names — supports `watchfire-<os>-<arch>[.exe]` format

### Fixed

- Agent chaining not stopping on auth (401) or rate-limit (429) errors — start-all/wildfire mode now checks for active issues before spawning the next agent
- Linux notification double-close bug — `notify_linux.go` now properly handles file close errors

### Changed

- Default sandbox changed from `"sandbox-exec"` to `"auto"` — platform auto-detects best backend
- Sandbox setting priority: CLI flag > project setting > global default

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
- **Session logs** — Agent sessions logged to `~/.watchfire/logs/` with JSONL transcripts from Claude Code (clean conversation format) and PTY scrollback fallback
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
