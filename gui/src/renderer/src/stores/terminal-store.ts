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
  setActiveSession: (id: string) => void
  togglePanel: () => void
  setPanelHeight: (height: number) => void
}

const MAX_SESSIONS = 5

function nextLabel(sessions: TerminalSession[]): string {
  const used = new Set(sessions.map((s) => s.label))
  for (let i = 1; i <= MAX_SESSIONS + 1; i++) {
    const label = `Shell ${i}`
    if (!used.has(label)) return label
  }
  return `Shell ${sessions.length + 1}`
}

export const useTerminalStore = create<TerminalState>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  panelOpen: safeGetItem('wf-terminal-panel-open') === 'true',
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
    if (state.sessions.length >= MAX_SESSIONS) return

    state.ensureListeners()

    const id = await window.watchfire.ptyCreate(cwd)
    const label = nextLabel(state.sessions)
    const session: TerminalSession = { id, label, projectId, exited: false }

    set((s) => ({
      sessions: [...s.sessions, session],
      activeSessionId: id
    }))
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

  setActiveSession: (id) => set({ activeSessionId: id }),

  togglePanel: () => {
    const next = !get().panelOpen
    safeSetItem('wf-terminal-panel-open', String(next))
    set({ panelOpen: next })
  },

  setPanelHeight: (height) => {
    safeSetItem('wf-terminal-panel-height', String(height))
    set({ panelHeight: height })
  }
}))
