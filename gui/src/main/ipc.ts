import { ipcMain, dialog, shell, BrowserWindow, Notification } from 'electron'
import { existsSync } from 'fs'
import { join } from 'path'
import { spawn } from 'child_process'
import { getDaemonInfo, ensureDaemon } from './daemon'
import { installCLI, needsInstall } from './cli-installer'
import * as ptyManager from './pty-manager'

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

    // CLI mode: rely on the IDE's shell helper being on PATH.
    // shell: true is required because Electron's inherited PATH on macOS GUI launches
    // is minimal — the shell will load the user's login PATH via their profile.
    const args = [...(spec.extraArgs ?? []), projectPath]
    const child = spawn(spec.cmd, args, {
      detached: true,
      stdio: 'ignore',
      shell: true
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
  // PTY handlers
  ipcMain.handle('pty-create', (_ev, cwd: string) => ptyManager.createPty(cwd))
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

  // Bring the main window to the foreground. Called by the renderer's focus
  // subscriber on every incoming tray event so a click in the menu bar
  // always lands the user back on the GUI.
  ipcMain.handle('focus-window', () => {
    const wins = BrowserWindow.getAllWindows()
    if (wins.length === 0) return
    const win = wins[0]
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
      // Click → focus the GUI window and route to the failing project's
      // TasksTab. The renderer-side focus-store pattern is reused: dispatch
      // an IPC event that App listens to and turns into a requestFocus.
      n.on('click', () => {
        const wins = BrowserWindow.getAllWindows()
        if (wins.length === 0) return
        const win = wins[0]
        if (win.isMinimized()) win.restore()
        win.show()
        win.focus()
        if (payload.projectId) {
          win.webContents.send('notifications:click', {
            projectId: payload.projectId,
            taskNumber: payload.taskNumber
          })
        }
      })
      n.show()
    }
  )
}
