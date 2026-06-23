// This window's scope, derived from the URL query the main process set when it
// created the window. `createHomeWindow()` loads the renderer with no query;
// `createProjectWindow()` loads it with `?project=<id>`. Until the renderer is
// fully boot-scoped (Inferno Feature 1, follow-up task), this is the single
// source of truth for "am I the home window?".
//
// D1 (single OS-notifier): with multiple windows open, only the HOME window's
// renderer owns the daemon notification stream + sound, so exactly one OS toast
// fires per notification regardless of how many project windows are open. The
// notifications store gates `start()` on this.
export type WindowScope = { kind: 'home' } | { kind: 'project'; projectId: string }

export function getWindowScope(): WindowScope {
  if (typeof window === 'undefined') return { kind: 'home' }
  const projectId = new URLSearchParams(window.location.search).get('project')
  return projectId ? { kind: 'project', projectId } : { kind: 'home' }
}

export function isHomeWindow(): boolean {
  return getWindowScope().kind === 'home'
}
