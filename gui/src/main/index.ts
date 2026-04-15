import { app, BrowserWindow, protocol, shell, net } from 'electron'
import { join } from 'path'
import { existsSync } from 'fs'
import { pathToFileURL } from 'url'
import { homedir } from 'os'
import { electronApp, optimizer, is } from '@electron-toolkit/utils'
import { loadWindowState, trackWindowState } from './window-state'
import { setupIpc } from './ipc'
import { ensureDaemon, getDaemonInfo } from './daemon'
import { checkAndInstallCLI } from './cli-installer'
import { initAutoUpdater } from './auto-updater'
import { setWindow as setPtyWindow, destroyAll as destroyAllPtys } from './pty-manager'

const DAEMON_YAML = join(homedir(), '.watchfire', 'daemon.yaml')

let mainWindow: BrowserWindow | null = null
let daemonWatcherInterval: ReturnType<typeof setInterval> | null = null
let watchedDaemonPid: number | null = null

function startDaemonWatcher(): void {
  // Clean up any existing watcher
  if (daemonWatcherInterval) clearInterval(daemonWatcherInterval)

  ensureDaemon()
    .then((info) => {
      watchedDaemonPid = info.pid
      mainWindow?.webContents.send('daemon-ready')

      daemonWatcherInterval = setInterval(async () => {
        try {
          process.kill(watchedDaemonPid!, 0)
        } catch {
          clearInterval(daemonWatcherInterval!)
          daemonWatcherInterval = null

          if (!existsSync(DAEMON_YAML)) {
            // daemon.yaml removed → graceful shutdown, quit the app
            console.log('Daemon shut down gracefully, quitting GUI...')
            mainWindow?.webContents.send('daemon-shutdown')
            setTimeout(() => app.quit(), 3000)
          } else {
            // daemon.yaml still exists with stale PID → crash, restart
            console.log('Daemon process gone, restarting...')
            startDaemonWatcher()
          }
        }
      }, 2000)
    })
    .catch((err) => {
      console.error('Failed to start daemon:', err)
      // Retry after a delay
      setTimeout(() => startDaemonWatcher(), 5000)
    })
}

function createWindow(): void {
  const savedState = loadWindowState()
  const usePosition = savedState.x !== -1 && savedState.y !== -1

  mainWindow = new BrowserWindow({
    width: savedState.width,
    height: savedState.height,
    ...(usePosition ? { x: savedState.x, y: savedState.y } : {}),
    minWidth: 900,
    minHeight: 600,
    show: false,
    title: 'Watchfire',
    ...(process.platform === 'darwin'
      ? { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 16, y: 16 } }
      : { frame: true }),
    backgroundColor: '#16181d',
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      sandbox: false,
      contextIsolation: true,
      nodeIntegration: false
    }
  })

  trackWindowState(mainWindow)

  mainWindow.on('ready-to-show', () => {
    mainWindow?.show()
    // Auto-open DevTools in dev so any residual renderer error is visible
    // to anyone running `npm run dev` without requiring Cmd+Opt+I.
    if (is.dev) {
      mainWindow?.webContents.openDevTools({ mode: 'detach' })
    }
  })

  mainWindow.webContents.setWindowOpenHandler((details) => {
    shell.openExternal(details.url)
    return { action: 'deny' }
  })

  // Load renderer. In production, serve via a custom `app://` scheme instead
  // of `file://`. Vite emits `<script type="module" crossorigin>` for the
  // entry bundle, and Chromium treats `crossorigin` modules loaded from a
  // `file://` origin as cross-origin requests — they are silently blocked,
  // producing the exact blank-window symptom seen on macOS. Loading from a
  // standard `app://` scheme gives the renderer a real origin and restores
  // module execution. Dev mode keeps using the Vite dev server.
  if (is.dev && process.env['ELECTRON_RENDERER_URL']) {
    mainWindow.loadURL(process.env['ELECTRON_RENDERER_URL'])
  } else {
    mainWindow.loadURL('app://renderer/index.html')
  }
}

const RENDERER_DIR = join(__dirname, '../renderer')

function registerAppProtocol(): void {
  protocol.handle('app', (request) => {
    const url = new URL(request.url)
    // Strip leading slash and normalize. `app://renderer/index.html` ->
    // `index.html` inside RENDERER_DIR. Any sub-path (assets/foo.js)
    // resolves under the same root.
    const relative = decodeURIComponent(url.pathname).replace(/^\/+/, '')
    const resolved = join(RENDERER_DIR, relative)
    // Guard against path traversal escaping the renderer dir.
    if (!resolved.startsWith(RENDERER_DIR)) {
      return new Response('Forbidden', { status: 403 })
    }
    return net.fetch(pathToFileURL(resolved).toString())
  })
}

// Privileged schemes must be declared before `app.whenReady()` resolves.
protocol.registerSchemesAsPrivileged([
  {
    scheme: 'app',
    privileges: {
      standard: true,
      secure: true,
      supportFetchAPI: true,
      corsEnabled: true
    }
  }
])

app.whenReady().then(async () => {
  electronApp.setAppUserModelId('io.watchfire.app')

  registerAppProtocol()

  // Optimize for development
  app.on('browser-window-created', (_, window) => {
    optimizer.watchWindowShortcuts(window)
  })

  // First-launch CLI installation (only when packaged)
  await checkAndInstallCLI()

  // Ensure daemon is running and auto-restart if it dies
  startDaemonWatcher()

  // Set up IPC handlers
  setupIpc()

  createWindow()

  // Wire PTY manager to main window
  if (mainWindow) {
    setPtyWindow(mainWindow)
  }

  // Initialize auto-updater
  if (mainWindow) {
    initAutoUpdater(mainWindow)
  }

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow()
    }
  })
})

app.on('window-all-closed', () => {
  destroyAllPtys()
  // Don't stop the daemon when GUI closes
  if (process.platform !== 'darwin') {
    app.quit()
  }
})

app.on('before-quit', () => {
  destroyAllPtys()
})
