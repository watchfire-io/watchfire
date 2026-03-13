import { ipcMain, dialog } from 'electron'
import { existsSync } from 'fs'
import { join } from 'path'
import { getDaemonInfo, ensureDaemon } from './daemon'
import { installCLI, needsInstall } from './cli-installer'
import * as ptyManager from './pty-manager'

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
}
