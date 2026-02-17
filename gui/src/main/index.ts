import { app, BrowserWindow, shell } from 'electron'
import { join } from 'path'
import { electronApp, optimizer, is } from '@electron-toolkit/utils'
import { setupIpc } from './ipc'
import { ensureDaemon, getDaemonInfo } from './daemon'
import { checkAndInstallCLI } from './cli-installer'
import { initAutoUpdater } from './auto-updater'

let mainWindow: BrowserWindow | null = null

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

  // Ensure daemon is running and watch for shutdown
  try {
    const daemonInfo = await ensureDaemon()
    const daemonPid = daemonInfo.pid
    const pidCheck = setInterval(() => {
      try {
        process.kill(daemonPid, 0)
      } catch {
        clearInterval(pidCheck)
        mainWindow?.webContents.send('daemon-shutdown')
        setTimeout(() => app.quit(), 3000)
      }
    }, 2000)
  } catch (err) {
    console.error('Failed to start daemon:', err)
  }

  // Set up IPC handlers
  setupIpc()

  createWindow()

  // Initialize auto-updater (only when packaged)
  if (app.isPackaged && mainWindow) {
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
