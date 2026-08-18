# Watchfire - Development Guide

## What is Watchfire?

Watchfire orchestrates coding agents sessions based on specs (project definitions and tasks). Read `ARCHITECTURE.md` for the full design.

## Source of Truth

`ARCHITECTURE.md` is the single source of truth for:
- Component responsibilities
- Data structures
- Directory layout
- Build phases
- Tech stack

If this file and `ARCHITECTURE.md` conflict, `ARCHITECTURE.md` wins.

For all decisions, use architecture document as reference. If you need to do something different, please update atchitecture after checking with user. 

## Task-completion lifecycle (v7.0 Relay)

When an agent finishes a task, the daemon picks one of two merge paths via the
on-task-done hook (`internal/daemon/agent/taskdone.go:HandleTaskDone`):

- **Silent merge (default).** `git merge --no-ff watchfire/<n>` lands the work
  on the project's default branch and the worktree is removed. This matches
  the v6.x behaviour and is the path every project gets unless they opt in
  to auto-PR.
- **GitHub auto-PR.** If `~/.watchfire/integrations.yaml` enables
  `github.auto_pr` for the project (either globally or via `project_scopes`),
  the daemon pushes `watchfire/<n>` to GitHub and opens a PR via `gh api`
  with body rendered from the task metadata + v6.0 diff stats. The local
  merge is suppressed; the user reviews and merges in GitHub. The worktree
  is still cleaned up after PR creation.

Auto-PR requires the `gh` CLI on PATH and `gh auth status` returning 0. If
either fails, the daemon logs a single WARN per project lifetime and falls
back to silent merge — no task ever strands inside an unmerged worktree.
Push failures and `gh api` errors fall back the same way but log per failure.

To enable, set `github.enabled: true` in `~/.watchfire/integrations.yaml` and
optionally restrict to specific projects via `github.project_scopes: [<id>...]`.

## Telegram Bridge (v10.0 Torch)

The daemon can be supervised from Telegram: a long-polling bridge
(`internal/daemon/telegram/` + `internal/daemon/telegrambot/`) with pairing as
the allowlist, live watch-mode relay, and an outbound relay adapter. See
`ARCHITECTURE.md` → "Telegram Bridge (`internal/daemon/telegram/`) — v10.0
Torch" for the design. Two invariants for anyone touching bridge code: it
**never calls `Resize`**, and the explicit `/say` path is the **only** PTY
write — both enforced by `internal/daemon/telegram/watch_guard_test.go`.

## Cycle ground rules

Tasks that run inside a release cycle are **local-only**: never push to a
remote, never create PRs or tags, and never touch release bookkeeping —
`version.json`, `CHANGELOG.md` release headers, or the project definition's
"Shipped" sections. Release bookkeeping and the push happen manually, only
after the cycle's validation gate passes and the user explicitly signs off.

## Repository Structure

Flat Go monorepo — one `go.mod` at the root:

```
watchfire/
├── ARCHITECTURE.md         # Single source of truth
├── CLAUDE.md               # This file - development guide
├── assets/                 # Images, logos, brand references (shared across components)
├── proto/                  # gRPC protobuf definitions
│   └── watchfire.proto
├── cmd/
│   ├── watchfired/         # Daemon entry point
│   └── watchfire/          # CLI/TUI entry point
├── internal/
│   ├── daemon/             # Daemon packages (agent, server, watcher, task, project, telegram, …)
│   ├── tui/                # Bubbletea TUI
│   ├── cli/                # CLI commands
│   ├── mcpserver/          # MCP server (watchfire mcp serve)
│   ├── config/             # YAML config, path helpers
│   └── models/             # Data structures
├── gui/                    # Electron GUI
└── go.mod
```

See `ARCHITECTURE.md` → "Repository Structure" for the full tree.
