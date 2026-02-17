import { create } from 'zustand'

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
  sidebarCollapsed: false,

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
    set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed }))
}))
