import { autoUpdater } from 'electron-updater'
import { BrowserWindow, ipcMain } from 'electron'
import { checkAndInstallCLI } from './cli-installer'

let mainWindow: BrowserWindow | null = null

export function initAutoUpdater(window: BrowserWindow): void {
  mainWindow = window

  // Configure auto-updater
  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true

  // Events → renderer
  autoUpdater.on('update-available', (info) => {
    mainWindow?.webContents.send('update-available', {
      version: info.version,
      releaseNotes: info.releaseNotes
    })
  })

  autoUpdater.on('download-progress', (progress) => {
    mainWindow?.webContents.send('update-progress', progress.percent)
  })

  autoUpdater.on('update-downloaded', () => {
    mainWindow?.webContents.send('update-downloaded')
  })

  autoUpdater.on('error', (err) => {
    mainWindow?.webContents.send('update-error', err.message)
  })

  // IPC handlers
  ipcMain.handle('check-for-updates', async () => {
    await autoUpdater.checkForUpdates()
  })

  ipcMain.handle('download-update', async () => {
    await autoUpdater.downloadUpdate()
  })

  ipcMain.handle('install-update', async () => {
    // After restart, checkAndInstallCLI() will update the CLI/daemon binaries
    // since they're bundled in the new app version
    autoUpdater.quitAndInstall()
  })

  // Check for updates on startup
  autoUpdater.checkForUpdates().catch((err) => {
    console.log('Auto-update check failed:', err.message)
  })
}
