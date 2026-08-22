---
name: watchfire
description: Operate Watchfire — the local orchestrator that runs coding agents (Claude Code, Codex, Gemini, opencode, Copilot) on tasks in sandboxed git worktrees and merges the results. Use when asked to run/queue/inspect Watchfire tasks or agents, drive the factory loop (create → run → wait → review), check why an agent is stuck or failed, set up or troubleshoot the Telegram bridge, or configure the daemon, projects, sandbox, integrations or the MCP server itself.
---

# Watchfire

Watchfire runs coding agents for you. You plan and review; Watchfire manufactures
the code in sandboxed, worktree-isolated runs and merges the result.

**One sentence of architecture, because it explains every rule below:** a single
local **daemon** (`watchfired`) owns all state and is the only thing that starts
agents. Everything else — CLI, TUI, GUI, MCP server, Telegram bridge — is a thin
client over its gRPC API. So any surface can drive Watchfire, they all see the
same truth, and none of them can disagree about what is running.

## Prefer MCP

**If the Watchfire MCP tools are available, use them.** They are the supported
agent surface: typed arguments, structured JSON results, errors written to be
read by a model. Shell out to the `watchfire` CLI only for things MCP does not
expose (listed under [CLI-only](#things-only-the-cli-can-do)).

Check availability by looking for tools named `list_projects`, `run_task`,
`get_agent_status`. If they are absent, see [Installing the MCP server](#installing-the-mcp-server).

## Before you do anything

1. **The daemon must be running.** Every surface fails without it. `watchfire daemon status`;
   start with `watchfire daemon start`. MCP and the CLI auto-start it; the GUI starts its own.
2. **A project must be registered.** `list_projects` (MCP) or `watchfire task list`.
   Register a new one with `watchfire init` inside the repo.
3. **Know which project you are talking about.** Every MCP tool takes an optional
   `project` (id *or* name). It is only optional when the MCP server was started
   inside that project's directory. **When in doubt, pass it explicitly** — a
   wrong default silently acts on the wrong repo.

## The factory loop

This is the core workflow. Do not improvise around it.

```
create_task  →  run_task  →  wait_for_task  →  get_task  →  get_task_diff
   file it       start it     block for it     read the     review what
                                               outcome      actually shipped
```

- `create_task` **files** work; it does **not** start it. `status: "ready"` only
  queues it for `run_all`/wildfire to pick up.
- `run_task` starts one agent now, in an isolated `watchfire/<n>` worktree.
- `wait_for_task` blocks. On `timed_out: true` **call it again** — that is a
  normal result, not an error. Runs take minutes.
- `get_task`: `status: "done"` only means the agent **stopped**. Check the
  `success` flag and `failure_reason` before believing it worked.
- `get_task_diff` is the review step. Read it before telling the user it is done.

**One agent per project, ever.** `run_task`, `run_all` and `start_wildfire`
*refuse* while an agent is running — they never queue and never replace. Wait
with `wait_for_task`, or abort with `stop_agent`.

### Modes

| Mode | Started by | What it does |
|---|---|---|
| **task** | `run_task` | One task, then stops. |
| **run-all** | `run_all` | Every `ready` task in sequence, chaining after each merge. |
| **chat** | typing in the GUI/TUI, or plain text in Telegram | An interactive session. No task, no merge. |
| **generate** / **plan** | `watchfire generate` / `plan` | Write the project definition / derive tasks from it. |
| **wildfire** | `start_wildfire` | **Autonomous.** Execute → Refine → Generate, looping until no work is left. |

**Wildfire creates and executes tasks nobody reviewed, and merges them.** Only
start it when the user has explicitly asked for autonomous operation. Never
start it to "save time" on a normal request.

## Diagnosing a run

In order — cheapest first:

1. `get_agent_status` — is anything running, in what mode, on what task, and is
   there a blocking `issue`?
2. `get_agent_screen` — the live terminal, ANSI stripped. This is how you tell a
   working agent from a hung one. Requires a running agent.
3. `list_logs` / `get_log` — for sessions that already ended.
4. `get_task_diff` — what the work actually changed.

**Issues** surface on `get_agent_status` and mean the agent is blocked, not slow:

- `auth_required` — the agent's login expired ("Please run /login"). Fix from
  Telegram with `/login`, or in the GUI/TUI terminal. The agent keeps running.
- `rate_limited` — provider limit; carries a reset time and auto-resumes.
- `trust_dialog` — Claude's folder-trust prompt; auto-acknowledged.
- `sandbox_denied` — the project path is inside a sandbox-denied root
  (`~/Desktop`, `~/Documents`, `~/Downloads`, `.ssh`, `.aws`, …). The agent was
  never started. **Move the project**; there is no override.

## The Telegram bridge

Watchfire's phone surface. A paired chat can talk to a project's chat agent,
watch runs stream, switch modes, and re-authenticate Claude — all from a phone.

**Pairing is the entire security boundary.** Anyone can message a Telegram bot,
so a chat sees project data only after redeeming a one-time code. Never treat a
Telegram username as authorization; the allowlist is by `chat_id`.

### Setting it up

Run `telegram_status` first — it reports exactly which step is missing in
`next_step`. The full sequence:

1. **Create a bot**: the user talks to [@BotFather](https://t.me/BotFather) on
   Telegram, `/newbot`, and copies the token.
2. **Store the token**: `telegram_configure` with `bot_token` and `enabled: true`.
   **Prefer the GUI/TUI** (Settings → Integrations → Telegram) or
   `watchfire integrations add telegram` — a token passed through a tool call is
   retained in the conversation transcript. Use `telegram_configure` without
   `bot_token` afterwards to just flip `enabled`.
3. **Restart the daemon** if `bridge_running` is still false — the long-poll loop
   starts with the daemon.
4. **Pair a chat**: `telegram_pair` returns a code and a deep link. Give the user
   the link (or `/pair <code>`). It is single-use, expires in 10 minutes, and
   `telegram_pair` does **not** wait — poll `telegram_status` until
   `pairing_state` is `"paired"`.
5. **Revoke** with `telegram_unpair` (chat id from `telegram_status`).

The token lives in the OS keyring, never in YAML. It is write-only across every
API: you can see *that* one is set (`token_set`), never what it is.

### What the user can do from the chat

Plain text is the headline feature: **typing in a paired chat talks to that
project's chat agent**, auto-starting one if nothing is running. Watch mode
(on by default) streams the replies back.

| Verb | Effect |
|---|---|
| `/projects`, `/use <n>` | List projects; select the one this chat acts on. |
| `/status`, `/tasks` | Daemon and task overview. |
| `/run <n>`, `/run all` | Start a task / run-all. **Replaces** a running agent. |
| `/wildfire`, `/stop` | Start the autonomous loop; stop whatever is running. |
| `/generate`, `/plan` | Definition / task generation. |
| `/new` | Fresh chat session, clearing context. |
| `/agent` | Switch the project's coding-agent backend. |
| `/login` | **Re-authenticate Claude from the phone** — the bridge drives the CLI's own OAuth dialog, sends you the link, and pastes the code you send back. |
| `/watch`, `/screen`, `/say` | Stream control, screen snapshot, explicit PTY write. |
| `/retry <n>`, `/cancel <n>`, `/mute` | Task retry / cancel, pause outbound events. |

Two invariants the bridge code enforces by test, worth knowing before you touch
it: it **never resizes** the PTY, and `injectSay` is the **only** thing that
writes to a PTY.

## Configuring the rest

**Projects** — `.watchfire/project.yaml` in each repo. Per-project: `sandbox`,
`default_agent`, `auto_merge`, `auto_delete_branch`, and `auto_start_tasks` —
which is a **persisted preference with no runtime consumer**: nothing reads it to
start an agent, so never tell a user that setting it will auto-run their tasks.
Tasks are `.watchfire/tasks/NNNN.yaml` (4-digit, zero-padded).
Edit through `watchfire configure`, the GUI/TUI, or the MCP task tools — not by
hand, so validation runs.

**Global** — `~/.watchfire/` includes `settings.yaml` (defaults, notifications,
quiet hours), `projects.yaml` (registry), `integrations.yaml` (outbound +
Telegram), `daemon.yaml`, `agents.yaml`, `daemon.log`, `digests/`, and
`logs/<project_id>/` (session transcripts, plus a per-project daemon log).

**Agent backends** — six: `claude-code`, `codex`, `gemini`, `opencode`, `copilot`,
`cursor` (Cursor Agent CLI; its binary is `cursor-agent`). Set per project via
`default_agent`, or switch with `/agent` from Telegram. Note this is a different
list from the MCP *install* clients below, which has five and excludes cursor.

**Sandbox** — macOS seatbelt / Linux Landlock or bubblewrap. The project dir is
writable; credential paths (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.netrc`, `~/.npmrc`)
are denied everywhere, and on **macOS only** the privacy folders (`~/Desktop`,
`~/Documents`, `~/Downloads`, `~/Music`, `~/Movies`, `~/Pictures`) too. The agent
does get keychain write access, so it can persist a refreshed OAuth token.

The `sandbox` value is messier than it looks: the daemon understands
`auto` (default) | `seatbelt` | `landlock` | `bwrap` | `none`, while the GUI/TUI
also write the legacy `sandbox-exec` (treated as `auto`) and `off`. **Only `none`
actually disables sandboxing** — `off` is not recognised and still runs sandboxed.
Disabling is a real reduction in isolation: suggest it only to diagnose a
sandbox-specific failure, and say so plainly.

**Outbound integrations** — webhooks (HMAC-signed), Slack, Discord, GitHub
auto-PR, Telegram. `watchfire integrations add|list|test <kind>`. GitHub auto-PR
pushes the branch and opens a PR instead of merging locally; it falls back to a
silent local merge if `gh` is missing or unauthenticated.

## Installing the MCP server

`watchfire mcp install` (interactive picker), or name a client:
`watchfire mcp install claude-code|codex|gemini|opencode|copilot|custom`. It is
idempotent, never clobbers an existing config, and prints manual instructions if
it cannot write. `--print` shows the config without writing.

The server is **stdio-only** — it never opens a listening socket, enforced by a
source-parsing test (`TestNoListeningSocketInServePath`) and an `lsof` check in
the e2e run. `watchfire mcp serve --read-only` serves only observation
tools; write and run tools are not registered at all, so they fail as unknown.

## Things only the CLI can do

- `watchfire init` — register a new project.
- `watchfire daemon start|stop|status`.
- `watchfire chat` — attach an interactive session in the terminal.
- `watchfire generate` / `plan` — definition and task generation.
- `watchfire integrations …` — configure webhooks/Slack/Discord/GitHub.
- `watchfire mcp install` — onboarding.
- `watchfire update` — self-update.

## Rules

- **Never start wildfire** unless the user explicitly asked for autonomous operation.
- **Never claim a task succeeded** on `status: "done"` alone — check `success`,
  then read `get_task_diff`.
- **Never hand-edit** task or project YAML when a tool exists; validation is in
  the write path.
- **Never pass a bot token** through a tool call if the GUI/TUI is available.
- **Do not poll in a tight loop.** `wait_for_task` is the blocking primitive;
  `timed_out: true` means call it again.
- **Report failures with their evidence** — `failure_reason`, the screen, the log.
  "It didn't work" is not a report.
