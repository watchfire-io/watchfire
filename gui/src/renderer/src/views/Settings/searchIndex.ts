import {
  Palette,
  Sliders,
  Terminal,
  Bell,
  Webhook,
  ArrowDownLeft,
  RefreshCw,
  Info,
  type LucideIcon
} from 'lucide-react'

export type SettingsCategoryId =
  | 'appearance'
  | 'defaults'
  | 'agent-paths'
  | 'notifications'
  | 'integrations'
  | 'inbound'
  | 'updates'
  | 'about'

export interface SettingsCategory {
  id: SettingsCategoryId
  label: string
  icon: LucideIcon
}

export interface SettingsSearchEntry {
  category: SettingsCategoryId
  fieldId: string
  label: string
  keywords: string[]
}

export const SETTINGS_CATEGORIES: SettingsCategory[] = [
  { id: 'appearance', label: 'Appearance', icon: Palette },
  { id: 'defaults', label: 'Defaults', icon: Sliders },
  { id: 'agent-paths', label: 'Agent Paths', icon: Terminal },
  { id: 'notifications', label: 'Notifications', icon: Bell },
  { id: 'integrations', label: 'Integrations', icon: Webhook },
  { id: 'inbound', label: 'Inbound', icon: ArrowDownLeft },
  { id: 'updates', label: 'Updates', icon: RefreshCw },
  { id: 'about', label: 'About', icon: Info }
]

export const CATEGORY_LABELS: Record<SettingsCategoryId, string> =
  SETTINGS_CATEGORIES.reduce(
    (acc, c) => ({ ...acc, [c.id]: c.label }),
    {} as Record<SettingsCategoryId, string>
  )

// Source of truth for in-settings search. Each entry maps a single editable
// control to its category + a stable fieldId that the section template echoes
// back via `data-setting-field-id="<id>"` so the layout can scroll to and
// pulse the matched element. Keep this in sync with internal/tui/globalsettings.go.
export const SETTINGS_SEARCH_INDEX: SettingsSearchEntry[] = [
  { category: 'appearance', fieldId: 'theme', label: 'Theme', keywords: ['light', 'dark', 'system', 'mode', 'color scheme'] },

  { category: 'defaults', fieldId: 'default-agent', label: 'Default Agent', keywords: ['agent', 'claude', 'codex', 'opencode', 'gemini', 'copilot'] },
  { category: 'defaults', fieldId: 'auto-merge', label: 'Auto-merge', keywords: ['merge', 'branch', 'task'] },
  { category: 'defaults', fieldId: 'auto-delete-branch', label: 'Auto-delete branches', keywords: ['delete', 'cleanup', 'branch', 'worktree'] },
  { category: 'defaults', fieldId: 'auto-start-tasks', label: 'Auto-start tasks', keywords: ['chain', 'queue', 'task'] },
  { category: 'defaults', fieldId: 'terminal-shell', label: 'Terminal shell', keywords: ['shell', 'bash', 'zsh', 'fish', 'terminal'] },

  { category: 'agent-paths', fieldId: 'agent-paths', label: 'Agent binary paths', keywords: ['path', 'binary', 'executable', 'claude', 'codex'] },

  { category: 'notifications', fieldId: 'notifications-enabled', label: 'Enable notifications', keywords: ['notify', 'alert', 'master'] },
  { category: 'notifications', fieldId: 'notifications-task-failed', label: 'Notify on task failure', keywords: ['fail', 'error', 'task'] },
  { category: 'notifications', fieldId: 'notifications-run-complete', label: 'Notify on run complete', keywords: ['done', 'finished', 'wildfire'] },
  { category: 'notifications', fieldId: 'notifications-weekly-digest', label: 'Send weekly digest', keywords: ['digest', 'summary', 'weekly'] },
  { category: 'notifications', fieldId: 'notifications-digest-schedule', label: 'Digest schedule', keywords: ['cron', 'schedule', 'time'] },
  { category: 'notifications', fieldId: 'notifications-sounds', label: 'Play sounds', keywords: ['sound', 'audio'] },
  { category: 'notifications', fieldId: 'notifications-sound-task-failed', label: 'Sound on task failure', keywords: ['sound', 'fail'] },
  { category: 'notifications', fieldId: 'notifications-sound-run-complete', label: 'Sound on run complete', keywords: ['sound', 'done'] },
  { category: 'notifications', fieldId: 'notifications-volume', label: 'Volume', keywords: ['loud', 'audio', 'sound'] },
  { category: 'notifications', fieldId: 'notifications-quiet-hours', label: 'Quiet hours', keywords: ['mute', 'do not disturb', 'dnd'] },

  { category: 'integrations', fieldId: 'integrations-list', label: 'Integrations', keywords: ['webhook', 'slack', 'discord', 'github'] },

  { category: 'inbound', fieldId: 'inbound-listen-addr', label: 'Inbound listen address', keywords: ['port', 'host', 'echo', 'http'] },
  { category: 'inbound', fieldId: 'inbound-public-url', label: 'Public URL', keywords: ['ngrok', 'tunnel', 'url', 'https'] },
  { category: 'inbound', fieldId: 'inbound-enabled', label: 'Enable inbound', keywords: ['echo', 'webhook', 'receiver'] },
  { category: 'inbound', fieldId: 'inbound-github-secret', label: 'GitHub webhook secret', keywords: ['github', 'hmac', 'secret'] },
  { category: 'inbound', fieldId: 'inbound-slack-secret', label: 'Slack signing secret', keywords: ['slack', 'signing', 'secret'] },
  { category: 'inbound', fieldId: 'inbound-discord-public-key', label: 'Discord public key', keywords: ['discord', 'ed25519', 'key'] },
  { category: 'inbound', fieldId: 'inbound-discord-app-id', label: 'Discord application ID', keywords: ['discord', 'app id'] },
  { category: 'inbound', fieldId: 'inbound-discord-bot-token', label: 'Discord bot token', keywords: ['discord', 'bot', 'token'] },

  { category: 'updates', fieldId: 'updates-check-startup', label: 'Check on startup', keywords: ['update', 'startup', 'launch'] },
  { category: 'updates', fieldId: 'updates-auto-download', label: 'Auto-download', keywords: ['download', 'update'] },
  { category: 'updates', fieldId: 'updates-frequency', label: 'Update frequency', keywords: ['daily', 'weekly', 'launch'] },
  { category: 'updates', fieldId: 'updates-check-now', label: 'Check Now', keywords: ['update', 'manual'] },

  { category: 'about', fieldId: 'about-version', label: 'Version', keywords: ['version', 'build', 'about'] }
]

// matchSearchEntries returns entries whose label or keywords contain every
// whitespace-delimited token from the query (case-insensitive). Empty
// queries return an empty list — the caller decides whether to render the
// full sidebar instead.
export function matchSearchEntries(
  query: string,
  index: SettingsSearchEntry[] = SETTINGS_SEARCH_INDEX
): SettingsSearchEntry[] {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (tokens.length === 0) return []
  return index.filter((entry) => {
    const haystack = (entry.label + ' ' + entry.keywords.join(' ')).toLowerCase()
    return tokens.every((t) => haystack.includes(t))
  })
}

// matchCategories returns the category subset whose label matches the query.
// Used to filter the sidebar list itself; entries also surface their own
// matched controls below the category list via matchSearchEntries.
export function matchCategories(
  query: string,
  categories: SettingsCategory[] = SETTINGS_CATEGORIES
): SettingsCategory[] {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (tokens.length === 0) return categories
  return categories.filter((c) =>
    tokens.every((t) => c.label.toLowerCase().includes(t))
  )
}
