import { ipcMain, dialog, shell, BrowserWindow, Notification } from 'electron'
import { existsSync, readFileSync, readdirSync, statSync } from 'fs'
import { join, delimiter } from 'path'
import { homedir } from 'os'
import { spawn } from 'child_process'
import { getDaemonInfo, ensureDaemon } from './daemon'
import { installCLI, needsInstall } from './cli-installer'
import * as ptyManager from './pty-manager'
import {
  createHomeWindow,
  createMonitorWindow,
  createProjectWindow,
  getHomeWindow,
  getMostRecentlyFocusedWindow,
  getOpenProjectWindowIds
} from './windows'

// IDE launch command specs. Each entry resolves to an (argv, options) tuple for child_process.spawn.
// Commands are looked up on PATH via shell: true so common installer-provided shims work cross-platform.
type IDESpec =
  | { kind: 'cli'; cmd: string; extraArgs?: string[] }
  | { kind: 'open-with-app'; app: string } // macOS-only: `open -a <App>`
  | { kind: 'reveal' } // Open the folder in the OS file manager

const IDE_COMMANDS: Record<string, IDESpec> = {
  vscode: { kind: 'cli', cmd: 'code' },
  cursor: { kind: 'cli', cmd: 'cursor' },
  windsurf: { kind: 'cli', cmd: 'windsurf' },
  zed: { kind: 'cli', cmd: 'zed' },
  webstorm: { kind: 'cli', cmd: 'webstorm' },
  idea: { kind: 'cli', cmd: 'idea' },
  sublime: { kind: 'cli', cmd: 'subl' },
  fleet: { kind: 'cli', cmd: 'fleet' },
  xcode: { kind: 'open-with-app', app: 'Xcode' },
  finder: { kind: 'reveal' }
}

// macOS GUI launches inherit a minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin)
// because path_helper only runs from login shells. spawn(..., {shell: true})
// uses /bin/sh -c, which is non-login, so the user's profile PATH is never
// loaded. That hides common IDE CLIs (`code` at /usr/local/bin/code, Homebrew
// shims at /opt/homebrew/bin, etc.). Prepend the well-known locations so
// IDE launches don't depend on how the GUI was started.
function spawnEnv(): NodeJS.ProcessEnv {
  const extras: string[] =
    process.platform === 'darwin'
      ? [
          '/usr/local/bin',
          '/usr/local/sbin',
          '/opt/homebrew/bin',
          '/opt/homebrew/sbin',
          join(homedir(), '.local', 'bin'),
          join(homedir(), 'bin')
        ]
      : process.platform === 'linux'
        ? [join(homedir(), '.local', 'bin'), join(homedir(), 'bin')]
        : []
  const current = process.env.PATH ?? ''
  const seen = new Set(current.split(delimiter).filter(Boolean))
  const additions = extras.filter((p) => !seen.has(p))
  const merged = [...additions, current].filter(Boolean).join(delimiter)
  return { ...process.env, PATH: merged }
}

async function openInIDE(ide: string, projectPath: string): Promise<{ ok: boolean; error?: string }> {
  const spec = IDE_COMMANDS[ide]
  if (!spec) return { ok: false, error: `Unknown IDE: ${ide}` }
  if (!existsSync(projectPath)) return { ok: false, error: `Path does not exist: ${projectPath}` }

  try {
    if (spec.kind === 'reveal') {
      const err = await shell.openPath(projectPath)
      if (err) return { ok: false, error: err }
      return { ok: true }
    }

    if (spec.kind === 'open-with-app') {
      if (process.platform !== 'darwin') {
        return { ok: false, error: `${spec.app} is only available on macOS` }
      }
      const child = spawn('open', ['-a', spec.app, projectPath], {
        detached: true,
        stdio: 'ignore'
      })
      child.on('error', () => {})
      child.unref()
      return { ok: true }
    }

    // CLI mode: rely on the IDE's shell helper being on PATH. shell: true uses
    // /bin/sh -c on macOS, which doesn't source the user's login profile, so
    // we pass an env with /usr/local/bin and the Homebrew prefixes prepended.
    const args = [...(spec.extraArgs ?? []), projectPath]
    const child = spawn(spec.cmd, args, {
      detached: true,
      stdio: 'ignore',
      shell: true,
      env: spawnEnv()
    })
    return await new Promise((resolve) => {
      let settled = false
      child.on('error', (err) => {
        if (settled) return
        settled = true
        resolve({ ok: false, error: `${spec.cmd}: ${err.message}` })
      })
      // With shell: true, a missing IDE binary surfaces as a quick non-zero exit
      // (sh prints "command not found" and exits 127). Wait briefly to catch that
      // before treating the launch as successful.
      child.on('exit', (code) => {
        if (settled) return
        if (code !== 0) {
          settled = true
          resolve({ ok: false, error: `${spec.cmd} failed to launch (exit ${code}). Is it installed and on PATH?` })
        }
      })
      setTimeout(() => {
        if (settled) return
        settled = true
        child.unref()
        resolve({ ok: true })
      }, 500)
    })
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) }
  }
}

export function setupIpc(): void {
  // PTY handlers. Resolve the originating window from the IPC sender so the
  // session routes its output back to the window that asked for it — not a
  // module-global "current" window that would bleed across project windows.
  ipcMain.handle('pty-create', (ev, cwd: string) => {
    const windowId = BrowserWindow.fromWebContents(ev.sender)?.id
    if (windowId === undefined) throw new Error('pty-create: no originating window')
    return ptyManager.createPty(cwd, windowId)
  })

  // Browse for a custom shell binary. Used by the global settings UI's
  // "Terminal shell" field (issue #32). Returns the absolute path on pick,
  // null on cancel. The renderer is responsible for posting the result back
  // through the SettingsService.UpdateSettings RPC; we don't validate here
  // because the daemon-side `validateExecutablePath` is the source of truth.
  ipcMain.handle('browse-shell-binary', async () => {
    const result = await dialog.showOpenDialog({
      properties: ['openFile'],
      title: 'Select Shell Binary',
      defaultPath: '/bin'
    })
    if (result.canceled || result.filePaths.length === 0) return null
    return result.filePaths[0]
  })
  ipcMain.handle('pty-write', (_ev, id: string, data: string) => ptyManager.writePty(id, data))
  ipcMain.handle('pty-resize', (_ev, id: string, cols: number, rows: number) => ptyManager.resizePty(id, cols, rows))
  ipcMain.handle('pty-destroy', (_ev, id: string) => ptyManager.destroyPty(id))
  ipcMain.handle('pty-destroy-all', () => ptyManager.destroyAll())
  ipcMain.handle('get-daemon-info', () => {
    return getDaemonInfo()
  })

  ipcMain.handle('ensure-daemon', async () => {
    return ensureDaemon()
  })

  ipcMain.handle('open-folder-dialog', async () => {
    const result = await dialog.showOpenDialog({
      properties: ['openDirectory', 'createDirectory'],
      title: 'Select Project Folder'
    })
    if (result.canceled || result.filePaths.length === 0) return null
    return result.filePaths[0]
  })

  ipcMain.handle('check-project-exists', (_event, folderPath: string) => {
    return existsSync(join(folderPath, '.watchfire', 'project.yaml'))
  })

  ipcMain.handle('get-version', () => {
    return require('../../package.json').version
  })

  ipcMain.handle('install-cli', async () => {
    return installCLI()
  })

  ipcMain.handle('check-cli-status', () => {
    return needsInstall()
  })

  ipcMain.handle('open-in-ide', (_event, ide: string, projectPath: string) => {
    return openInIDE(ide, projectPath)
  })

  // v8 Inferno — boot-scoped windows. A project window's "Open another
  // project" affordance routes back to the home/dashboard surface; the
  // registry focuses an existing home window or creates one.
  ipcMain.handle('open-home-window', () => {
    createHomeWindow()
  })

  // Open (or focus) a specific project's window. One window per project — a
  // repeat call just focuses the existing one (handled inside the registry).
  ipcMain.handle('open-project-window', (_event, projectId: string) => {
    if (typeof projectId === 'string' && projectId) createProjectWindow(projectId)
  })

  // v8 Inferno (stretch) — open (or focus) the always-on-top mini-monitor.
  // Singleton, handled inside the registry.
  ipcMain.handle('open-monitor-window', () => {
    createMonitorWindow()
  })

  // Open (or focus) a project's window and route its renderer to a surface.
  // Drives the home window's needs-attention click-through (v8 Inferno —
  // mission control). Mirrors the notification-click path: a freshly-created
  // window's renderer isn't loaded yet, so the routing message is deferred to
  // `did-finish-load`.
  ipcMain.handle(
    'focus-project-window',
    (_event, projectId: string, target?: string, taskNumber?: number) => {
      if (typeof projectId !== 'string' || !projectId) return
      const win = createProjectWindow(projectId)
      if (!win || win.isDestroyed()) return
      if (win.isMinimized()) win.restore()
      win.show()
      win.focus()

      const send = (): void => {
        if (win.isDestroyed()) return
        win.webContents.send('project-focus', { projectId, target, taskNumber })
      }
      if (win.webContents.isLoading()) {
        win.webContents.once('did-finish-load', send)
      } else {
        send()
      }
    }
  )

  // The set of projects that currently have their own window. The home/dashboard
  // renderer reads this once at mount and then keeps it fresh via the
  // `project-windows-changed` broadcast, so each card can show "focus existing
  // window" instead of implying a duplicate would be spawned.
  ipcMain.handle('list-project-windows', () => getOpenProjectWindowIds())

  // Bring a window to the foreground. Called by the renderer's focus
  // subscriber on every incoming tray event so a click in the menu bar
  // always lands the user back on the GUI. Targets the most-recently-focused
  // window (falling back to the home window) rather than an arbitrary one.
  ipcMain.handle('focus-window', () => {
    const win = getMostRecentlyFocusedWindow() ?? BrowserWindow.getAllWindows()[0]
    if (!win || win.isDestroyed()) return
    if (win.isMinimized()) win.restore()
    win.show()
    win.focus()
  })

  // Show a native OS notification. The renderer subscribes to the daemon's
  // NotificationService.Subscribe stream, decides whether to play its own
  // sound (via the bundled WAV files), and then hands the metadata off here
  // so Electron can attribute the system toast / banner properly. We pass
  // `silent: true` unconditionally — the renderer is the source of sound
  // truth (so the OS and the renderer never double-fire), and the optional
  // .wav playback was wired up in task 0051.
  ipcMain.handle(
    'notifications:emit',
    (
      _event,
      payload: { id: string; kind: string; projectId: string; taskNumber: number; title: string; body: string }
    ) => {
      if (!Notification.isSupported()) return
      const n = new Notification({
        title: payload.title || 'Watchfire',
        body: payload.body,
        silent: true
      })
      // Click → focus the right window and route by kind. TASK_FAILED /
      // RUN_COMPLETE open (or focus) the project's own window; WEEKLY_DIGEST
      // is global, so it surfaces on the home window. A freshly-opened project
      // window hasn't loaded its renderer yet, so defer the IPC send until the
      // contents finish loading.
      n.on('click', () => {
        const isProjectEvent = payload.kind !== 'WEEKLY_DIGEST' && !!payload.projectId
        const win = isProjectEvent
          ? createProjectWindow(payload.projectId)
          : getHomeWindow() ?? getMostRecentlyFocusedWindow()
        if (!win || win.isDestroyed()) return

        // createProjectWindow already focuses an existing window; ensure a
        // minimized/hidden window is surfaced regardless of path.
        if (win.isMinimized()) win.restore()
        win.show()
        win.focus()

        const send = (): void => {
          if (win.isDestroyed()) return
          win.webContents.send('notifications:click', {
            kind: payload.kind,
            projectId: payload.projectId,
            taskNumber: payload.taskNumber
          })
        }
        if (win.webContents.isLoading()) {
          win.webContents.once('did-finish-load', send)
        } else {
          send()
        }
      })
      n.show()
    }
  )

  // v6.0 Ember — read a single digest's persisted Markdown. The daemon's
  // digest scheduler writes one file per fire date under
  // `~/.watchfire/digests/<YYYY-MM-DD>.md`; the GUI's DigestModal reads it
  // back here via IPC rather than streaming the body over gRPC (the body
  // is already on the local filesystem; no need to round-trip).
  ipcMain.handle('digests:read', (_event, dateKey: string) => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(dateKey)) return null
    const path = join(homedir(), '.watchfire', 'digests', `${dateKey}.md`)
    if (!existsSync(path)) return null
    try {
      return readFileSync(path, 'utf-8')
    } catch {
      return null
    }
  })

  // v6.0 Ember — list available digests, newest first. Returns the date keys
  // (e.g. ["2026-05-04", "2026-04-27", ...]) so the in-app notification
  // center can populate a "Digests" tab without re-reading every file.
  ipcMain.handle('digests:list', () => {
    const dir = join(homedir(), '.watchfire', 'digests')
    if (!existsSync(dir)) return []
    try {
      const out: { date: string; mtimeMs: number }[] = []
      for (const name of readdirSync(dir)) {
        if (!/^\d{4}-\d{2}-\d{2}\.md$/.test(name)) continue
        try {
          const st = statSync(join(dir, name))
          out.push({ date: name.slice(0, 10), mtimeMs: st.mtimeMs })
        } catch {
          /* ignore */
        }
      }
      out.sort((a, b) => b.mtimeMs - a.mtimeMs)
      return out.map((e) => e.date)
    } catch {
      return []
    }
  })
}
