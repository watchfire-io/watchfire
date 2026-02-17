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
}

declare global {
  interface Window {
    watchfire: WatchfireAPI
  }
}

export {}
