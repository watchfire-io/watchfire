import { create } from 'zustand'
import { getWindowScope, type WindowScope } from '../lib/window-scope'

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
export type FocusRequestTarget = 'main' | 'tasks' | 'task'

export interface FocusRequest {
  // Monotonic id so consumers can detect a fresh request even when the
  // payload (projectId / target) is identical to the previous one.
  id: number
  projectId: string
  target: FocusRequestTarget
  taskNumber?: number
}

interface AppState {
  // The scope this window booted into, derived ONCE from the URL the main
  // process set when it created the window (`?project=<id>` ⇒ project window,
  // no query ⇒ home window). It never changes for the window's lifetime — it's
  // the boot identity, not navigation state. The home window keeps the sidebar
  // + dashboard; a project window renders ProjectView full-bleed. See
  // `lib/window-scope.ts` (v8 "Inferno" Feature 1).
  windowScope: WindowScope
  view: AppView
  selectedProjectId: string | null
  connected: boolean
  daemonPort: number | null
  theme: 'system' | 'light' | 'dark'
  sidebarCollapsed: boolean
  focusRequest: FocusRequest | null

  setView: (view: AppView) => void
  selectProject: (projectId: string) => void
  setConnected: (connected: boolean, port?: number) => void
  setTheme: (theme: 'system' | 'light' | 'dark') => void
  toggleSidebar: () => void
  requestFocus: (req: Omit<FocusRequest, 'id'>) => void
}

let focusRequestSeq = 0

// Read the boot scope exactly once, at store-factory time. A project window
// boots straight into its ProjectView (view='project', project preselected) so
// there's no flash of the dashboard before navigation catches up.
const bootScope = getWindowScope()

export const useAppStore = create<AppState>((set) => ({
  windowScope: bootScope,
  view: bootScope.kind === 'project' ? 'project' : 'dashboard',
  selectedProjectId: bootScope.kind === 'project' ? bootScope.projectId : null,
  connected: false,
  daemonPort: null,
  theme: 'system',
  sidebarCollapsed: safeGetItem('wf-sidebar-collapsed') === 'true',
  focusRequest: null,

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
    }),

  requestFocus: (req) =>
    set({
      view: req.projectId ? 'project' : 'dashboard',
      selectedProjectId: req.projectId || null,
      focusRequest: { id: ++focusRequestSeq, ...req }
    })
}))
