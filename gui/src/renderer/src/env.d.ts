/// <reference types="vite/client" />

interface DaemonInfo {
  host: string
  port: number
  pid: number
  started_at: string
}

interface CLIInstallStatus {
  needed: boolean
  reason: 'missing' | 'outdated' | 'none'
}

interface UpdateInfo {
  version: string
  releaseNotes?: string
}

interface WatchfireAPI {
  getDaemonInfo(): Promise<DaemonInfo | null>
  ensureDaemon(): Promise<DaemonInfo>
  openFolderDialog(): Promise<string | null>
  checkProjectExists(folderPath: string): Promise<boolean>
  getVersion(): Promise<string>
  onDaemonShutdown(callback: () => void): void

  // CLI installation
  installCLI(): Promise<boolean>
  checkCLIStatus(): Promise<CLIInstallStatus>

  // Auto-updates
  checkForUpdates(): Promise<void>
  downloadUpdate(): Promise<void>
  installUpdate(): Promise<void>
  onUpdateAvailable(callback: (info: UpdateInfo) => void): void
  onUpdateReady(callback: () => void): void
  onUpdateProgress(callback: (percent: number) => void): void
  onUpdateError(callback: (error: string) => void): void

  // Terminal PTY
  ptyCreate(cwd: string): Promise<string>
  ptyWrite(id: string, data: string): Promise<void>
  ptyResize(id: string, cols: number, rows: number): Promise<void>
  ptyDestroy(id: string): Promise<void>
  ptyDestroyAll(): Promise<void>
  onPtyOutput(callback: (data: { id: string; data: string }) => void): void
  offPtyOutput(): void
  onPtyExit(callback: (data: { id: string; exitCode: number }) => void): void
  offPtyExit(): void

  // Open a project path in an external IDE / file manager
  openInIDE(ide: string, projectPath: string): Promise<{ ok: boolean; error?: string }>

  // Bring the main window to the foreground
  focusWindow(): Promise<void>

  // Native OS notifications (v5.0 Pulse)
  emitNotification(payload: {
    id: string
    kind: string
    projectId: string
    taskNumber: number
    title: string
    body: string
  }): Promise<void>
  onNotificationClick(callback: (payload: { projectId: string; taskNumber: number }) => void): void
}

declare global {
  interface Window {
    watchfire: WatchfireAPI
  }
}

export {}
