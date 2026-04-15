import { create } from 'zustand'

function safeGetItem(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function safeSetItem(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* storage unavailable — ignore */
  }
}

export type AppView = 'dashboard' | 'project' | 'add-project' | 'settings'

interface AppState {
  view: AppView
  selectedProjectId: string | null
  connected: boolean
  daemonPort: number | null
  theme: 'system' | 'light' | 'dark'
  sidebarCollapsed: boolean

  setView: (view: AppView) => void
  selectProject: (projectId: string) => void
  setConnected: (connected: boolean, port?: number) => void
  setTheme: (theme: 'system' | 'light' | 'dark') => void
  toggleSidebar: () => void
}

export const useAppStore = create<AppState>((set) => ({
  view: 'dashboard',
  selectedProjectId: null,
  connected: false,
  daemonPort: null,
  theme: 'system',
  sidebarCollapsed: safeGetItem('wf-sidebar-collapsed') === 'true',

  setView: (view) => set({ view }),

  selectProject: (projectId) =>
    set({ view: 'project', selectedProjectId: projectId }),

  setConnected: (connected, port) =>
    set({ connected, daemonPort: port ?? null }),

  setTheme: (theme) => {
    const root = document.documentElement
    if (theme === 'light') {
      root.setAttribute('data-theme', 'light')
    } else {
      root.removeAttribute('data-theme')
    }
    set({ theme })
  },

  toggleSidebar: () =>
    set((s) => {
      const next = !s.sidebarCollapsed
      safeSetItem('wf-sidebar-collapsed', String(next))
      return { sidebarCollapsed: next }
    })
}))
