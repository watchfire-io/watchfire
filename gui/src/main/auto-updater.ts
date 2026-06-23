import { autoUpdater } from 'electron-updater'
import { ipcMain } from 'electron'
import { checkAndInstallCLI } from './cli-installer'
import { broadcast } from './windows'

export function initAutoUpdater(): void {
  // Configure auto-updater
  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true

  // Events → every open window. The update banner lives in each renderer, so
  // a project window should learn about an available/downloaded update too.
  autoUpdater.on('update-available', (info) => {
    broadcast('update-available', {
      version: info.version,
      releaseNotes: info.releaseNotes
    })
  })

  autoUpdater.on('download-progress', (progress) => {
    broadcast('update-progress', progress.percent)
  })

  autoUpdater.on('update-downloaded', () => {
    broadcast('update-downloaded')
  })

  autoUpdater.on('error', (err) => {
    broadcast('update-error', err.message)
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
