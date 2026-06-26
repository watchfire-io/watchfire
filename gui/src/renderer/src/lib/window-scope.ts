// This window's scope, derived from the URL query the main process set when it
// created the window. `createHomeWindow()` loads the renderer with no query;
// `createProjectWindow()` loads it with `?project=<id>`; `createMonitorWindow()`
// loads it with `?monitor=1`. Until the renderer is fully boot-scoped (Inferno
// Feature 1, follow-up task), this is the single source of truth for "what kind
// of window am I?".
//
// D1 (single OS-notifier): with multiple windows open, only the HOME window's
// renderer owns the daemon notification stream + sound, so exactly one OS toast
// fires per notification regardless of how many project / monitor windows are
// open. The notifications store gates `start()` on `isHomeWindow()`.
export type WindowScope =
  | { kind: 'home' }
  | { kind: 'project'; projectId: string }
  | { kind: 'monitor' }

export function getWindowScope(): WindowScope {
  if (typeof window === 'undefined') return { kind: 'home' }
  const params = new URLSearchParams(window.location.search)
  if (params.get('monitor')) return { kind: 'monitor' }
  const projectId = params.get('project')
  return projectId ? { kind: 'project', projectId } : { kind: 'home' }
}

export function isHomeWindow(): boolean {
  return getWindowScope().kind === 'home'
}

export function isMonitorWindow(): boolean {
  return getWindowScope().kind === 'monitor'
}
