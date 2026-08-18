import { BrowserWindow } from 'electron'
import { randomUUID } from 'crypto'
import { createRequire } from 'module'
import { existsSync, readFileSync, statSync } from 'fs'
import { homedir, platform } from 'os'
import { join } from 'path'
import { parse } from 'yaml'
import { loginShellEnv } from './login-shell'
import { resolveShellCommand } from './shell-command'

// Use createRequire to load native module at runtime — bypasses Rollup bundling
const _require = createRequire(import.meta.url || __filename)
const pty = _require('node-pty') as typeof import('node-pty')

interface PtySession {
  pty: import('node-pty').IPty
  id: string
  windowId: number
}

let sessions: Map<string, PtySession> = new Map()

// Route a PTY event to the window that owns the session. With one window per
// project, terminal bytes must land only in the originating window — sending
// to a module-global "current" window would bleed project A's output into
// whatever window was focused last. Resolve the window by id each time and
// guard destruction (the window may have closed while bytes were in flight).
function send(windowId: number, channel: string, data: unknown): void {
  const win = BrowserWindow.fromId(windowId)
  if (win && !win.isDestroyed() && !win.webContents.isDestroyed()) {
    win.webContents.send(channel, data)
  }
}

// Read defaults.terminal_shell from ~/.watchfire/settings.yaml. Returns the
// configured path iff it points at an executable; otherwise null. We re-read
// the file on each PTY spawn (not cached) so a settings change takes effect
// on the next NEW terminal without an app relaunch — existing terminal
// sessions keep the shell they were spawned with. The file is tiny and the
// stat/read cost is dwarfed by the PTY spawn itself.
function readConfiguredShell(): string | null {
  try {
    const path = join(homedir(), '.watchfire', 'settings.yaml')
    if (!existsSync(path)) return null
    const raw = readFileSync(path, 'utf-8')
    const parsed = parse(raw) as { defaults?: { terminal_shell?: string } } | null
    const shell = parsed?.defaults?.terminal_shell?.trim()
    if (!shell) return null
    const info = statSync(shell)
    if (!info.isFile()) return null
    if ((info.mode & 0o111) === 0) return null
    return shell
  } catch {
    return null
  }
}

export async function createPty(cwd: string, windowId: number): Promise<string> {
  const id = randomUUID()
  const env = await loginShellEnv()
  // Spawn as a login shell (`-l` on macOS/Linux) so ~/.zprofile / ~/.profile
  // and macOS's path_helper run and PATH matches a native terminal (#32).
  const { file, args } = resolveShellCommand(readConfiguredShell(), process.env.SHELL, platform())

  const p = pty.spawn(file, args, {
    name: 'xterm-256color',
    cols: 80,
    rows: 24,
    cwd,
    env: env as Record<string, string>
  })

  p.onData((data) => {
    send(windowId, 'pty-output', { id, data })
  })

  p.onExit(({ exitCode }) => {
    send(windowId, 'pty-exit', { id, exitCode })
    sessions.delete(id)
  })

  sessions.set(id, { pty: p, id, windowId })
  return id
}

export function writePty(id: string, data: string): void {
  sessions.get(id)?.pty.write(data)
}

export function resizePty(id: string, cols: number, rows: number): void {
  try {
    sessions.get(id)?.pty.resize(cols, rows)
  } catch {
    // ignore resize errors on dead PTY
  }
}

export function destroyPty(id: string): void {
  const session = sessions.get(id)
  if (session) {
    session.pty.kill()
    sessions.delete(id)
  }
}

// Kill and forget every session owned by a single window. Wired to each
// project window's `closed` event so closing one window tears down only its
// terminals, leaving other windows' PTYs alive.
export function destroyForWindow(windowId: number): void {
  for (const [id, session] of sessions) {
    if (session.windowId === windowId) {
      session.pty.kill()
      sessions.delete(id)
    }
  }
}

export function destroyAll(): void {
  for (const [, session] of sessions) {
    session.pty.kill()
  }
  sessions.clear()
}
