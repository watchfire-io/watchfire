// Update policy for the GUI's electron-updater wiring (issue #61).
//
// The settings UI has always offered "Check on startup", "Auto-download" and
// a check frequency, persisted by the daemon under `updates:` in
// ~/.watchfire/settings.yaml — but the main process never read them: it
// checked on every launch and downloaded whatever it found. These pure
// helpers turn the parsed settings file into the decisions the updater makes.
//
// Kept free of Electron / fs imports so update-policy.test.mjs can mirror it.

export type CheckFrequency = 'every_launch' | 'daily' | 'weekly'

export interface UpdatePolicy {
  checkOnStartup: boolean
  autoDownload: boolean
  checkFrequency: CheckFrequency
}

// Mirrors models.DefaultSettings() in internal/models/settings.go — what the
// daemon assumes when the file or a key is missing.
export const DEFAULT_UPDATE_POLICY: UpdatePolicy = {
  checkOnStartup: true,
  autoDownload: true,
  checkFrequency: 'every_launch'
}

const DAY_MS = 24 * 60 * 60 * 1000

// Build the policy from the parsed settings.yaml document. Only a real
// boolean overrides a default, so a hand-edited `auto_download: "no"` cannot
// be misread as either value.
export function parseUpdatePolicy(doc: unknown): UpdatePolicy {
  const policy = { ...DEFAULT_UPDATE_POLICY }
  const updates = (doc as { updates?: Record<string, unknown> } | null)?.updates
  if (!updates || typeof updates !== 'object') return policy
  if (typeof updates.check_on_startup === 'boolean') policy.checkOnStartup = updates.check_on_startup
  if (typeof updates.auto_download === 'boolean') policy.autoDownload = updates.auto_download
  const freq = updates.check_frequency
  if (freq === 'every_launch' || freq === 'daily' || freq === 'weekly') policy.checkFrequency = freq
  return policy
}

// Whether the launch-time check should run. `lastCheckedMs` is the GUI's own
// record of its last check (null if it never checked) — not the daemon's
// `updates.last_checked`, which the daemon refreshes on its own startup check
// and would otherwise always make the GUI look freshly checked.
export function shouldCheckOnStartup(
  policy: UpdatePolicy,
  lastCheckedMs: number | null,
  nowMs: number
): boolean {
  if (!policy.checkOnStartup) return false
  if (lastCheckedMs === null || lastCheckedMs > nowMs) return true
  const since = nowMs - lastCheckedMs
  if (policy.checkFrequency === 'daily') return since >= DAY_MS
  if (policy.checkFrequency === 'weekly') return since >= 7 * DAY_MS
  return true
}
