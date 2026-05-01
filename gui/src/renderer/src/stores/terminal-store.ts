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

export interface TerminalSession {
  id: string
  label: string
  projectId: string
  exited: boolean
  lastOutputAt: number
}

interface TerminalState {
  sessions: TerminalSession[]
  activeSessionId: string | null
  panelOpen: boolean
  panelHeight: number
  listenerRegistered: boolean

  outputCallbacks: Map<string, (data: string) => void>
  exitCallbacks: Map<string, (exitCode: number) => void>
  registerOutputCallback: (id: string, cb: (data: string) => void) => void
  unregisterOutputCallback: (id: string) => void
  registerExitCallback: (id: string, cb: (exitCode: number) => void) => void
  unregisterExitCallback: (id: string) => void

  ensureListeners: () => void
  createSession: (projectId: string, cwd: string) => Promise<void>
  destroySession: (id: string) => Promise<void>
  destroyAllSessions: () => Promise<void>
  destroyProjectSessions: (projectId: string) => Promise<void>
  setActiveSession: (id: string) => void
  expandPanel: () => void
  collapsePanel: () => void
  togglePanel: () => void
  setPanelHeight: (height: number) => void

  getProjectSessions: (projectId: string) => TerminalSession[]
}

const MAX_SESSIONS_PER_PROJECT = 5
const PANEL_OPEN_KEY = 'wf-terminal-panel-open'

function nextLabel(projectSessions: TerminalSession[]): string {
  const used = new Set(projectSessions.map((s) => s.label))
  for (let i = 1; i <= MAX_SESSIONS_PER_PROJECT + 1; i++) {
    const label = `Shell ${i}`
    if (!used.has(label)) return label
  }
  return `Shell ${projectSessions.length + 1}`
}

export const useTerminalStore = create<TerminalState>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  panelOpen: safeGetItem(PANEL_OPEN_KEY) === 'true',
  panelHeight: Number(safeGetItem('wf-terminal-panel-height')) || 250,
  listenerRegistered: false,

  outputCallbacks: new Map(),
  exitCallbacks: new Map(),

  registerOutputCallback: (id, cb) => {
    get().outputCallbacks.set(id, cb)
  },

  unregisterOutputCallback: (id) => {
    get().outputCallbacks.delete(id)
  },

  registerExitCallback: (id, cb) => {
    get().exitCallbacks.set(id, cb)
  },

  unregisterExitCallback: (id) => {
    get().exitCallbacks.delete(id)
  },

  ensureListeners: () => {
    if (get().listenerRegistered) return
    set({ listenerRegistered: true })

    window.watchfire.onPtyOutput(({ id, data }) => {
      get().outputCallbacks.get(id)?.(data)
      const now = Date.now()
      set((s) => {
        const idx = s.sessions.findIndex((sess) => sess.id === id)
        if (idx === -1) return s
        const current = s.sessions[idx]
        // Throttle store updates: only re-emit if >250ms since last bump,
        // so rapid output streams don't cause a render storm.
        if (now - current.lastOutputAt < 250) return s
        const next = s.sessions.slice()
        next[idx] = { ...current, lastOutputAt: now }
        return { sessions: next }
      })
    })

    window.watchfire.onPtyExit(({ id, exitCode }) => {
      get().exitCallbacks.get(id)?.(exitCode)
      set((s) => ({
        sessions: s.sessions.map((sess) =>
          sess.id === id ? { ...sess, exited: true } : sess
        )
      }))
    })
  },

  createSession: async (projectId, cwd) => {
    const state = get()
    const projectSessions = state.sessions.filter((s) => s.projectId === projectId)
    if (projectSessions.length >= MAX_SESSIONS_PER_PROJECT) return

    state.ensureListeners()

    const id = await window.watchfire.ptyCreate(cwd)
    const label = nextLabel(projectSessions)
    const session: TerminalSession = {
      id,
      label,
      projectId,
      exited: false,
      lastOutputAt: Date.now()
    }

    set((s) => ({
      sessions: [...s.sessions, session],
      activeSessionId: id,
      panelOpen: true
    }))
    safeSetItem(PANEL_OPEN_KEY, 'true')
  },

  destroySession: async (id) => {
    await window.watchfire.ptyDestroy(id)
    const state = get()
    const remaining = state.sessions.filter((s) => s.id !== id)
    state.outputCallbacks.delete(id)
    state.exitCallbacks.delete(id)

    set({
      sessions: remaining,
      activeSessionId:
        state.activeSessionId === id
          ? remaining[remaining.length - 1]?.id ?? null
          : state.activeSessionId
    })
  },

  destroyAllSessions: async () => {
    await window.watchfire.ptyDestroyAll()
    const state = get()
    state.outputCallbacks.clear()
    state.exitCallbacks.clear()
    set({ sessions: [], activeSessionId: null })
  },

  destroyProjectSessions: async (projectId) => {
    const state = get()
    const targets = state.sessions.filter((s) => s.projectId === projectId)
    await Promise.all(targets.map((s) => window.watchfire.ptyDestroy(s.id)))
    for (const t of targets) {
      state.outputCallbacks.delete(t.id)
      state.exitCallbacks.delete(t.id)
    }
    const remaining = state.sessions.filter((s) => s.projectId !== projectId)
    const stillActive = remaining.find((s) => s.id === state.activeSessionId)
    set({
      sessions: remaining,
      activeSessionId: stillActive ? state.activeSessionId : remaining[remaining.length - 1]?.id ?? null
    })
  },

  setActiveSession: (id) => set({ activeSessionId: id }),

  expandPanel: () => {
    safeSetItem(PANEL_OPEN_KEY, 'true')
    set({ panelOpen: true })
  },

  collapsePanel: () => {
    safeSetItem(PANEL_OPEN_KEY, 'false')
    set({ panelOpen: false })
  },

  togglePanel: () => {
    const next = !get().panelOpen
    safeSetItem(PANEL_OPEN_KEY, String(next))
    set({ panelOpen: next })
  },

  setPanelHeight: (height) => {
    safeSetItem('wf-terminal-panel-height', String(height))
    set({ panelHeight: height })
  },

  getProjectSessions: (projectId) =>
    get().sessions.filter((s) => s.projectId === projectId)
}))
