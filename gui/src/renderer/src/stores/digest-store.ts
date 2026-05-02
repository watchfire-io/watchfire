import { create } from 'zustand'

// digest-store tracks the v6.0 Ember weekly-digest modal state — which date
// is open (if any), the loaded Markdown body, and a list of recently emitted
// digest dates for the in-app notification center. The persisted Markdown
// lives in `~/.watchfire/digests/<YYYY-MM-DD>.md` and is loaded via the
// `readDigest(dateKey)` IPC bridge in the main process.

interface DigestStoreState {
  // Currently-open digest date (YYYY-MM-DD) or null when the modal is closed.
  openDate: string | null
  // The loaded Markdown body for openDate, or null while it's being fetched.
  body: string | null
  // List of available digest dates, newest first. Populated lazily by
  // refreshList() on first open of the notification center.
  list: string[]
  loading: boolean
  open: (dateKey: string) => Promise<void>
  close: () => void
  refreshList: () => Promise<void>
}

export const useDigestStore = create<DigestStoreState>((set) => ({
  openDate: null,
  body: null,
  list: [],
  loading: false,

  open: async (dateKey) => {
    set({ openDate: dateKey, body: null, loading: true })
    try {
      const body = await window.watchfire.readDigest(dateKey)
      // Only apply the result if the user is still on the same digest —
      // a fast cancel-then-reopen shouldn't race a stale body in.
      set((s) => (s.openDate === dateKey ? { body, loading: false } : s))
    } catch (err) {
      console.warn('readDigest failed', err)
      set((s) => (s.openDate === dateKey ? { body: null, loading: false } : s))
    }
  },

  close: () => {
    set({ openDate: null, body: null, loading: false })
  },

  refreshList: async () => {
    try {
      const list = await window.watchfire.listDigests()
      set({ list })
    } catch (err) {
      console.warn('listDigests failed', err)
    }
  }
}))
