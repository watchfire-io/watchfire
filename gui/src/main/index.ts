import { app, BrowserWindow, shell } from 'electron'
import { join } from 'path'
import { existsSync } from 'fs'
import { homedir } from 'os'
import { electronApp, optimizer, is } from '@electron-toolkit/utils'
import { setupIpc } from './ipc'
import { ensureDaemon, getDaemonInfo } from './daemon'
import { checkAndInstallCLI } from './cli-installer'
import { initAutoUpdater } from './auto-updater'

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
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    show: false,
    title: 'Watchfire',
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 16, y: 16 },
    backgroundColor: '#16181d',
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      sandbox: false,
      contextIsolation: true,
      nodeIntegration: false
    }
  })

  mainWindow.on('ready-to-show', () => {
    mainWindow?.show()
  })

  mainWindow.webContents.setWindowOpenHandler((details) => {
    shell.openExternal(details.url)
    return { action: 'deny' }
  })

  // Load renderer
  if (is.dev && process.env['ELECTRON_RENDERER_URL']) {
    mainWindow.loadURL(process.env['ELECTRON_RENDERER_URL'])
  } else {
    mainWindow.loadFile(join(__dirname, '../renderer/index.html'))
  }
}

app.whenReady().then(async () => {
  electronApp.setAppUserModelId('io.watchfire.app')

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
  // Don't stop the daemon when GUI closes
  if (process.platform !== 'darwin') {
    app.quit()
  }
})
