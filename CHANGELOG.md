# Changelog

## [5.0.0] Flare

Flare closes the inbound loop Beacon left half-open and hardens the run-all path. The two "Known issues" filed against Beacon — the missing GitHub PR-merge handler and the missing Slack HTTP transport — both ship; the inbound surface gains OAuth, multi-host parity (GitHub Enterprise / GitLab / Bitbucket), per-IP rate limiting, Slack interactive components, and Discord guild auto-registration; the run-all silent-halt bug, the chat-tab repaint loop, and the buried `failure_reason` are all fixed; and the global settings UI is reorganized into searchable category sub-pages.

### Added

- **GitHub PR-merge handler — closes the v4.0 Beacon auto-PR loop (#0075).** New `internal/daemon/echo/handler_github.go` registered at `POST /echo/github?project=<id>` parses `X-GitHub-Event` / `X-Hub-Signature-256` / `X-GitHub-Delivery`, resolves the per-project HMAC secret from the keyring, runs `verify.VerifyGitHub`, deduplicates against the LRU+TTL idempotency cache, narrows on `event == "pull_request" && action == "closed" && pull_request.merged == true`, then matches the Watchfire task by `pull_request.head.ref == watchfire/<n>` and calls `task.MarkDoneIfNotAlready` + emits a Pulse `RUN_COMPLETE` notification titled `<project> — PR #<number> merged`. Closes the v4.0 Beacon "Known issue" #1.
- **Slack slash-command HTTP transport — closes the v4.0 Beacon Slack-parity gap (#0076).** New `internal/daemon/echo/handler_slack_commands.go` translates the URL-encoded slash-command form body (`command`, `text`, `team_id`, `channel_id`, `user_id`, `trigger_id`) into a call against the shared transport-agnostic `commands.Route(...)` router, then renders `CommandResponse` as Slack response JSON (`{response_type: "in_channel" | "ephemeral", text, blocks}`). `/watchfire status / retry / cancel` now works in Slack at parity with the Discord interactions endpoint that shipped in Beacon. Closes the v4.0 Beacon "Known issue" #2.
- **OAuth bot tokens for Slack and Discord (#0077).** Replaces the v4.0 paste-a-signing-secret model with a proper OAuth install flow. Slack: `xoxb-...` bot token from the workspace OAuth callback, used for `chat.postMessage` so slash responses can include rich attachments and DM the originator on private failures. Discord: `Authorization: Bot <token>` for inbound auth and command registration. New "Connect Slack" / "Connect Discord" buttons in the Integrations settings UI launch the flow in the user's default browser; success surfaces a `Connected as <bot username>` pill. The legacy signing-secret + public-key path stays additive for users mid-cutover.
- **GitHub Enterprise / GitLab / Bitbucket inbound parity (#0078).** Per-project `github_host` field on `models.InboundConfig` lets the existing GitHub HMAC-SHA256 verifier target arbitrary GitHub Enterprise hostnames (the v7.0 outbound auto-PR path picks up the same field). New `internal/daemon/echo/handler_gitlab.go` verifies `X-Gitlab-Token` (per-project shared secret), narrows on `Merge Request Hook` events with `action: merge`. New `internal/daemon/echo/handler_bitbucket.go` verifies `X-Hub-Signature` (HMAC-SHA256), narrows on `pullrequest:fulfilled` events. Settings UI surfaces a "Git host" picker on inbound config.
- **Per-IP rate limiting on the inbound HTTP server (#0079).** Per-IP token bucket via `golang.org/x/time/rate`, default 30 req/min/IP across every `/echo/*` route, configurable through `models.InboundConfig.RateLimitPerMin` (`0` disables). Idempotent deliveries already in the LRU cache do NOT count against the bucket. On 429, the daemon logs a single WARN per IP per minute to avoid log flooding under a sustained flood.
- **Slack interactive components — buttons + cancel-reason modal (#0080).** The v7.0 Slack outbound TASK_FAILED Block Kit template gains three action buttons: `Retry`, `Cancel`, `View in Watchfire`. New inbound endpoint `POST /echo/slack/interactivity` handles the `block_actions` and `view_submission` payloads with the same v0 HMAC verification + 5-minute drift window as the slash-commands endpoint. Button presses route through `commands.Route` so a `Retry` click is the exact equivalent of `/watchfire retry`. `Cancel` opens a Slack modal that asks "Why are you cancelling?"; the supplied reason lands in `task.failure_reason`.
- **Discord slash-command auto-registration on guild join (#0081).** The daemon now enumerates the guilds the bot is in at startup and POSTs the three slash-command schemas to each via the existing `internal/cli/integrations_discord.go::registerForGuild` helper; it also subscribes to `GUILD_CREATE` Gateway events so a freshly-added guild gets commands within 30 seconds (no CLI step). The Settings UI lists every guild with a ✓ / ✗ registration pill. The manual `watchfire integrations register-discord <guild>` CLI stays as a fallback. Discord's commands API is upsert-style, so re-running is safe.
- **Settings UI: macOS-style category sub-pages with search (#0082).** Both GUI (`gui/src/renderer/src/views/Settings/GlobalSettings.tsx`) and TUI (`internal/tui/settings.go`) replace the single long scrolling page with a two-pane layout — left sidebar of eight categories (Appearance, Defaults, Agent Paths, Notifications, Integrations, Inbound, Updates, About), right pane shows only the selected category. New search input filters categories AND surfaces individual matching controls with category breadcrumbs; clicking a result navigates to the category and pulses the matching field for ~1.5s. GUI: `Cmd/Ctrl+F` focuses search, `Esc` clears, `Up/Down/Enter` navigate. TUI: `/` opens a search overlay with the same field-jumping behaviour. Deep-link routes (`#integrations` etc.) still work.

### Fixed

- **Run-all silently halted on auto-merge failure (#0083).** When `internal/daemon/agent/taskdone.go::HandleTaskDone`'s silent merge failed (dirty `main`, merge conflict, post-merge hook failure), the chain stopped — but **silently**: the task YAML still showed `status: done` + `success: true`, no notification fired, and the user was left wondering why their queue stalled. `onTaskDoneFn` now returns a structured `TaskDoneResult{Outcome, Reason}` (with `TaskDoneOK` / `TaskDoneMergeFailed` / `TaskDoneCancelled`) instead of a bare bool; `monitorProcess` branches on `result.Outcome == TaskDoneMergeFailed` and emits a TASK_FAILED-shaped notification before the chain decision; `runSilentMerge` populates the task's new `merge_failure_reason` field (`yaml: merge_failure_reason,omitempty`, exposed via proto + GUI/TUI). The chain-stop semantics are unchanged — the user still has to clean up `main` manually — but the silence is gone.
- **GUI chat-tab repainted multiple times on project switch (#0084).** Verified the symptom under the new `RightPanel/ChatTab.tsx` architecture (the v5.0 spec had referenced the now-deleted `ChatPanel.tsx`), then locked in single-mount + single-start guards: the auto-start `useEffect` deps tightened to `[!!agentStatus, isRunning, projectId]` so a stale `agentStatus` reference from the previous project no longer fires `handleStart` on a transient render edge; the `autoStarted.current = false` reset on `projectId` change runs before the auto-start check; regression test in `gui/src/renderer/src/views/ProjectView/RightPanel/ChatTab.test.tsx` simulates rapid project switching and asserts `handleStart` fires exactly once per navigation.
- **Failed-task UI hid the reason behind two clicks (#0085).** `TaskStatusBadge` now carries a `title=` tooltip for agent-reported failures (it already had one for merge failures only), populated by a new exported pure helper `computeBadgeTooltip` that prefers `Merge failed: …` over `Failed: …` when both reasons are set and truncates to 500 runes. `TaskItem` passes `failureReason={task.failureReason}` into the badge alongside `mergeFailureReason`. `TaskModal`'s tab decision is now lazy in `useState(() => …)` AND kept in sync via the existing effect, so `done` tasks land on the Inspect tab on first paint without a flicker through the form-tab state. The TUI task list (`internal/tui/tasklist.go`) renders an inline preview of both reasons (merge-failure precedence) under the `[✗]` glyph.

### Tests

- **Inbound framework coverage gap closed (#0070).** Filled out `internal/daemon/echo/`'s test surface — every signature verifier (GitHub HMAC-SHA256, Slack v0, Discord Ed25519) covers golden-path + every rejection mode (missing header, malformed signature, drift overshoot, replay window); `idempotency.go`'s LRU+TTL behaves correctly under concurrent access, eviction, and TTL refresh; `commands.Route` round-trips `status` / `retry <task>` / `cancel <task>` against a mocked task manager.

### Migration

- All Flare features are additive — projects upgrade with no behaviour change.
- Inbound: existing signing-secret + public-key configs continue to work; OAuth is opt-in via the new "Connect Slack" / "Connect Discord" buttons. The new `RateLimitPerMin` field defaults to 30; set to 0 to disable.
- Multi-host inbound: leave `github_host` empty for github.com; set per-project for GitHub Enterprise. GitLab and Bitbucket handlers are inactive until their per-project secret is configured.
- Discord auto-registration runs on next daemon start — existing guilds get re-upserted (idempotent). The CLI `watchfire integrations register-discord <guild>` stays available as a fallback.
- Run-all halt fix: `onTaskDoneFn`'s signature changed from `func(...) bool` to `func(...) TaskDoneResult`. Internal callback only — no external API impact, but third-party forks pinning to the old signature will need to update.

## [4.0.0] Beacon

Beacon is the consolidated dashboard / notifications / insights / integrations release — glanceable dashboard, proactive OS notifications, retrospective insights, outbound + inbound integrations.

### Added

- **Dashboard aggregate status bar** — single muted status line `N working · N needs attention · N idle · N done today` between the dashboard header and the project grid; counts derived from existing zustand stores so it updates live with no new gRPC.
- **Dashboard filter chips** — pill chips (`All`, `Working`, `Needs attention`, `Idle`, `Has ready tasks`) with live counts; selection persists in `localStorage[wf-dashboard-filter]`. Predicates shared via `gui/src/renderer/src/lib/dashboard-filters.ts`.
- **Elapsed-time badge on running ProjectCards** — ticking `Ns / Nm / Nh Mm` next to the agent badge, sourced from a new `AgentStatus.started_at` proto field stamped in `RunningAgent.StartedAt`. Flips to `var(--wf-warning)` past 30 minutes.
- **Last-activity timestamp on dashboard cards** — `Active now / 5m ago / 4h ago / 2mo ago` segment derived from the most recent task `updated_at`. Hand-rolled relative-time formatter in `gui/src/renderer/src/lib/relative-time.ts`.
- **Live PTY last-line preview on dashboard cards** — latest non-blank terminal line in monospace muted text, throttled to 4 Hz. Singleton subscription manager in `gui/src/renderer/src/stores/agent-preview-store.ts` ref-counts the underlying `AgentService.SubscribeScreen` stream.
- **Needs-attention treatment for failed tasks** — red-tinted card border + header `AlertTriangle` chip + `N failed` segment in the counts row + red progress segment when any task has `status === 'done' && success === false`.
- **Current-task surfacing on running ProjectCards** — replaces the misleading `Next:` line with `Working: <current task title>` (with `Flame` icon) when the agent is actively running. No proto change — uses the existing `AgentStatus.task_title`.
- **Shell-count chip on running ProjectCards** — terminal icon + count from `useTerminalStore` filtered by alive sessions for the project; pulses when any session emitted output in the last 2s. Click expands the bottom panel.
- **Dashboard grid/list layout toggle** — `LayoutGrid` / `Rows3` toggle in the header; list mode renders one ~46px row per project. Selection persists in `localStorage[wf-dashboard-layout]`. Per-project rendering in `gui/src/renderer/src/views/Dashboard/ProjectRow.tsx`.
- **Notification bus** — new `internal/daemon/notify` package with a typed `Bus`, channel fan-out (slow-consumer drop), stable `MakeID` (`sha256(kind|project_id|task_number|emitted_at_unix)[:8]`), and JSONL append to `~/.watchfire/logs/<project_id>/notifications.log` for headless fallback.
- **TASK_FAILED OS notification** — fires from `internal/daemon/server/task_failed.go::emitTaskFailed` on `done && !success`. Title `<project> — task #NNNN failed`, body is the task title + optional failure reason.
- **RUN_COMPLETE OS notification** — fires at the falling edge of every autonomous run (single-task, start-all, wildfire) bounded by a new `RunningAgent.RunStartedAt`. Body `N tasks done · M failed` over the run window.
- **Bundled notification sounds** — `assets/sounds/task-{done,failed}.wav` (mono 22050 Hz, ~25 KB each). Pure `shouldPlaySound(kind, prefs)` decision in `gui/src/renderer/src/stores/notifications-sound.ts`. OS toast goes silent precisely when the renderer plays its own audio.
- **Dynamic system tray menu** — `internal/daemon/tray/tray.go` rebuilds on every project / task / agent / settings change; sections for `Needs attention` / `Working` / `Idle` plus `Notifications (N today) ▸` submenu reading the JSONL fallback. Click-through routes via the new `DaemonService.SubscribeFocusEvents` stream.
- **Notification preferences UI** — TUI (`internal/tui/globalsettings.go`) and GUI (`gui/src/renderer/src/views/Settings/NotificationsSection.tsx`) expose master / per-event / sounds / volume / quiet-hours / per-project mute. Schema under `defaults.notifications` in `~/.watchfire/settings.yaml`. Gating helper `models.ShouldNotify`.
- **Inline diff viewer** — new `internal/daemon/diff` package resolves diffs pre-merge (`<merge-base>...HEAD` on `watchfire/<n>`) and post-merge (locates the merge commit via `git log --grep`). Structured `FileDiffSet`; cap at 10000 lines; cache at `~/.watchfire/diff-cache/<project_id>/<task_number>.json`. GUI Inspect tab + TUI overlay (bound to `d`).
- **Per-task metrics capture** — `<n>.metrics.yaml` siblings carrying duration, exit reason, agent, tokens, cost. New `internal/daemon/metrics` package with parsers for Claude Code, Codex, opencode, Gemini, Copilot (stub). Capture from a non-blocking goroutine on `handleTaskChanged`. New `watchfire metrics backfill` CLI.
- **Per-project Insights view** — `internal/daemon/insights/project.go` aggregates one project's tasks per window. New GUI Insights tab + TUI overlay (bound to `i`) with KPI strip, stacked-bar tasks-per-day, agent donut, duration histogram. `localStorage[wf-insights-window]` persists the 7d / 30d / 90d / All selector.
- **Cross-project Insights rollup** — `internal/daemon/insights/global.go` aggregates the whole fleet per window; cached at `~/.watchfire/insights-cache/_global.json`. Dashboard rollup card under the Beacon status bar; TUI fleet overlay bound to `Ctrl+f`.
- **Report export (CSV + Markdown)** — shared `InsightsService.ExportReport` RPC with `oneof` scope (`project_id` / `global` / `single_task`). Markdown templates in `internal/daemon/insights/templates/`; CSV uses `# section: <name>` headers. Single `<ExportPill>` component on the dashboard + ProjectView headers; TUI binds `Ctrl+e`.
- **Weekly digest notification** — `digestRunner` schedules with a re-armable `time.Timer` from `models.DigestSchedule.NextFire` (DST-stable, with 24-hour catch-up on daemon start). Markdown rendered to `~/.watchfire/digests/<YYYY-MM-DD>.md` regardless of toast suppression. New `WEEKLY_DIGEST` notification kind + `FOCUS_TARGET_DIGEST`.
- **Outbound delivery framework + webhook adapter** — new `internal/daemon/relay` package with an `Adapter` interface and a `Dispatcher` subscribing to `notify.Bus`. Per-adapter retry (`[500ms, 2s, 8s]`) + circuit breaker (3 failures / 5-minute window). Generic `WebhookAdapter` POSTs the canonical payload with `X-Watchfire-Signature: sha256=<hex>` HMAC. Secrets via OS keyring (`internal/config/keyring.go`) with file-store fallback.
- **Slack adapter (Block Kit messages)** — `internal/daemon/relay/slack.go` renders three `text/template` Block Kit envelopes (TASK_FAILED / RUN_COMPLETE / WEEKLY_DIGEST) with header / section / context / actions blocks. Project-color → `:large_<color>_square:` shortcode map in `slack_color.go`.
- **Discord adapter (rich embeds)** — `internal/daemon/relay/discord.go` renders three embed envelopes with project-color tinting. Shared `hexToInt` / `rfc3339` template helpers. Defensive 4000-rune description trim with single-WARN log on overflow. New `watchfire integrations` CLI parent with `list` and `test` subcommands.
- **GitHub auto-PR creation** — opt-in per project via `github.auto_pr.enabled: true`. End-of-task lifecycle in `internal/daemon/git/pr.go::OpenPR`: `gh auth status` → parse `<owner>/<repo>` → `git push --force-with-lease` → render PR body via `pr_body.md.tmpl` → `gh api -X POST /repos/:owner/:repo/pulls`. Sentinel errors distinguish silent fallback (one WARN per project lifetime) from per-attempt failures.
- **Integrations settings UI (GUI + TUI)** — new `IntegrationsService` gRPC service with `List` / `Save` / `Delete` / `Test` RPCs; `Save` carries a `oneof` payload, secrets are write-only on the wire. GUI `IntegrationsSection.tsx` with per-type detail panels; TUI overlay reachable via `Ctrl+I`.
- **Inbound HTTP server framework** — `internal/daemon/echo/server.go` binds `ListenAddr` (default `127.0.0.1:8765`), 5 s graceful shutdown drain, 1 MiB body cap + panic recovery middleware, unauthenticated `/echo/health`. `RegisterProvider(method, path, handler)` for plug-in handlers. Bind failure logs ERROR but doesn't crash the daemon.
- **Signature verification** — `internal/daemon/echo/verify.go` ships `VerifyGitHub` (HMAC-SHA256 against `sha256=<hex>`), `VerifySlack` (HMAC-SHA256 over `v0:<timestamp>:<body>` with 5-minute drift), `VerifyDiscord` (Ed25519 over `timestamp || body`, same drift) — all constant-time.
- **Idempotency cache** — `internal/daemon/echo/idempotency.go` is an LRU+TTL cache (1000 entries / 24h, `container/list`-backed, `sync.Mutex`-protected); `Seen(key)` refreshes TTL on hit.
- **Per-task lifecycle helpers + command router** — `internal/daemon/echo/commands.go::Route(ctx, cmd, subcmd, rest, CommandContext) CommandResponse` powers slash-command transports. Three commands (`status` / `retry <task>` / `cancel <task>`); `CommandResponse{text, blocks, ephemeral, in_channel}` is transport-agnostic.
- **Discord interactions endpoint** — `internal/daemon/echo/handler_discord.go` exposes `POST /echo/discord/interactions` with end-to-end Ed25519 verification + replay window + idempotency. PING → PONG; APPLICATION_COMMAND → dispatch to `commands.Route`, render via `discord_render.go::RenderInteraction`. Slash-command registration via `watchfire integrations register-discord <guild_id>` (idempotent).
- **Inbound settings UI (GUI + TUI)** — `gui/src/renderer/src/views/Settings/InboundSection.tsx` shows a Listening pill polled at 5 s, editable `ListenAddr` + `PublicURL` with restart button, Copy-as-`<provider>`-URL buttons, four write-only secret inputs, per-provider last-delivery timestamps. TUI mirrors via a new "Inbound" tab inside the Integrations overlay.

### Changed

- **Dashboard auto-sorts projects by activity** — replaces raw `position` order with bucketing into needs-attention → working → has-ready-tasks → idle (input-array index as final tiebreaker for stability). Predicate helpers in `gui/src/renderer/src/lib/dashboard-filters.ts`. A muted `Sorted by activity` label appears when the activity order differs from the underlying position order.

### Fixed

- **GUI: switching projects silently killed every running shell in the bottom panel** — PTY sessions now live in a global pool keyed by `projectId` and survive navigation; Cmd+\` toggles a non-destructive `panelCollapsed` flag. `destroyProjectSessions(projectId)` is called only from `removeProject`. `BottomPanel.tsx` always-mounts every `TerminalTab` with a `visible` flag so xterm.js scrollback survives React reconciliation.
- **In-app terminal couldn't find pnpm / volta / fnm-managed binaries (#32)** — new shared helper `gui/src/main/login-shell.ts` runs `$SHELL -l -c env`, parses PATH + dev-tool env vars, with a fallback PATH merge against the standard user-install locations. Caches per Electron process. New `defaults.terminal_shell` global setting picks the shell binary (X_OK validated). Fixes #32

### Migration

- All Beacon features are additive — existing projects upgrade with no behaviour change.
- Notifications: master toggle defaults on, `weekly_digest` defaults off, quiet hours default off.
- Outbound integrations: nothing fans out until you configure an integration under Settings → Integrations.
- GitHub auto-PR: opt-in per project. Requires `gh` on PATH and `gh auth status` returning 0; missing prerequisites fall back to silent merge with one WARN per project lifetime.
- Inbound integrations: empty `InboundConfig` = no listener. Concrete handlers return 503 until the per-provider secret is configured.

### Known issues

- The dedicated `handler_github.go` for `pull_request.closed` events did not ship with Beacon — auto-PR loop closes manually for now (filed as v5.0 follow-up).
- The Slack HTTP transport on top of the shared `commands.Route` did not ship with Beacon — `/watchfire status / retry / cancel` works in Discord but not in Slack (filed as v5.0 follow-up).

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
