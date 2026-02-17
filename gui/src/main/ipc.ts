import { ipcMain, dialog } from 'electron'
import { existsSync } from 'fs'
import { join } from 'path'
import { getDaemonInfo } from './daemon'
import { installCLI, needsInstall } from './cli-installer'

export function setupIpc(): void {
  ipcMain.handle('get-daemon-info', () => {
    return getDaemonInfo()
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
}
