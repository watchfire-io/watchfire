import { useEffect, useMemo, useRef, useState } from 'react'
import type { AgentInfo } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { useAppStore } from '../../stores/app-store'
import { getSettingsClient } from '../../lib/grpc-client'
import { DefaultsSection } from './DefaultsSection'
import { AppearanceSection } from './AppearanceSection'
import { AgentPathsSection } from './AgentPathsSection'
import { NotificationsSection } from './NotificationsSection'
import { IntegrationsSection } from './IntegrationsSection'
import { InboundSection } from './InboundSection'
import { UpdatesSection } from './UpdatesSection'
import { AboutSection } from './AboutSection'
import { SettingsSidebar } from './SettingsSidebar'
import {
  CATEGORY_LABELS,
  SETTINGS_CATEGORIES,
  matchSearchEntries,
  type SettingsCategoryId,
  type SettingsSearchEntry
} from './searchIndex'

const VALID_CATEGORY_IDS = new Set(SETTINGS_CATEGORIES.map((c) => c.id))

function isCategoryId(value: string): value is SettingsCategoryId {
  return VALID_CATEGORY_IDS.has(value as SettingsCategoryId)
}

// Read window.location.hash and return the matching category id or null. The
// hash is a short slug (e.g. `#integrations`) that any view router can deep-
// link to, matching macOS System Settings' behaviour.
function readHashCategory(): SettingsCategoryId | null {
  const raw = (window.location.hash || '').replace(/^#/, '').trim().toLowerCase()
  if (!raw) return null
  return isCategoryId(raw) ? raw : null
}

// Briefly highlight the element matching the given fieldId. Idempotent — if
// an existing animation is mid-flight on the same node, we restart it by
// removing and re-adding the class on the next frame.
function pulseField(fieldId: string) {
  const el = document.querySelector<HTMLElement>(`[data-setting-field-id="${fieldId}"]`)
  if (!el) return
  el.scrollIntoView({ behavior: 'smooth', block: 'center' })
  el.classList.remove('settings-highlight-pulse')
  // Force a reflow so removal+addition counts as a fresh animation cycle.
  void el.offsetWidth
  el.classList.add('settings-highlight-pulse')
  window.setTimeout(() => {
    el.classList.remove('settings-highlight-pulse')
  }, 1600)
}

export function GlobalSettings() {
  const settings = useSettingsStore((s) => s.settings)
  const fetchSettings = useSettingsStore((s) => s.fetchSettings)
  const loading = useSettingsStore((s) => s.loading)
  const connected = useAppStore((s) => s.connected)
  const [version, setVersion] = useState<string>('')
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [agentsLoaded, setAgentsLoaded] = useState(false)

  const [selected, setSelected] = useState<SettingsCategoryId>(
    () => readHashCategory() ?? 'appearance'
  )
  const [query, setQuery] = useState('')
  const [resultCursor, setResultCursor] = useState(0)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const resultsRef = useRef<HTMLUListElement>(null)

  useEffect(() => {
    fetchSettings()
    window.watchfire.getVersion().then(setVersion)
  }, [])

  useEffect(() => {
    if (connected) fetchSettings()
  }, [connected])

  useEffect(() => {
    if (!connected) return
    let cancelled = false
    setAgentsLoaded(false)
    ;(async () => {
      try {
        const res = await getSettingsClient().listAgents({})
        if (!cancelled) {
          setAgents(res.agents)
          setAgentsLoaded(true)
        }
      } catch {
        if (!cancelled) setAgentsLoaded(true)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [connected])

  // Honour deep-link hash changes that happen after mount (other panes may
  // route here with a fresh `#category`).
  useEffect(() => {
    const handler = () => {
      const cat = readHashCategory()
      if (cat) setSelected(cat)
    }
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [])

  const results: SettingsSearchEntry[] = useMemo(
    () => matchSearchEntries(query),
    [query]
  )

  // Reset the keyboard cursor whenever the result set shrinks/grows so we
  // don't point past the end.
  useEffect(() => {
    setResultCursor(0)
  }, [query])

  // Activate a search result: switch to its category, blur the input so the
  // pulse is visible without the search overlay grabbing focus, then trigger
  // the pulse on the next paint (the section needs to mount first).
  const activateResult = (entry: SettingsSearchEntry) => {
    setSelected(entry.category)
    setQuery('')
    requestAnimationFrame(() => {
      requestAnimationFrame(() => pulseField(entry.fieldId))
    })
  }

  // Global keyboard shortcuts. Cmd/Ctrl+F focuses the search; Esc clears it
  // when the input is focused; Up/Down + Enter drive result navigation when
  // the input has focus and there is a query.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const isFind = (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'f'
      if (isFind) {
        e.preventDefault()
        searchInputRef.current?.focus()
        searchInputRef.current?.select()
        return
      }
      const inputFocused = document.activeElement === searchInputRef.current
      if (!inputFocused) return

      if (e.key === 'Escape') {
        if (query) {
          e.preventDefault()
          setQuery('')
        } else {
          searchInputRef.current?.blur()
        }
        return
      }
      if (results.length === 0) return

      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setResultCursor((n) => Math.min(n + 1, results.length - 1))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setResultCursor((n) => Math.max(n - 1, 0))
      } else if (e.key === 'Enter') {
        e.preventDefault()
        const entry = results[resultCursor]
        if (entry) activateResult(entry)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [query, results, resultCursor])

  // Keep the hash in sync with the active category so refreshing or sharing
  // a window stays on the same pane.
  useEffect(() => {
    const desired = `#${selected}`
    if (window.location.hash !== desired) {
      window.history.replaceState(null, '', desired)
    }
  }, [selected])

  if (loading && !settings) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="w-6 h-6 border-2 border-fire-500 border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  const renderSection = () => {
    if (!settings && selected !== 'about') {
      return null
    }
    switch (selected) {
      case 'appearance':
        return settings ? <AppearanceSection settings={settings} /> : null
      case 'defaults':
        return settings ? (
          <DefaultsSection settings={settings} agents={agents} agentsLoaded={agentsLoaded} />
        ) : null
      case 'agent-paths':
        return settings ? (
          <AgentPathsSection settings={settings} agents={agents} agentsLoaded={agentsLoaded} />
        ) : null
      case 'notifications':
        return settings ? <NotificationsSection settings={settings} /> : null
      case 'integrations':
        return <IntegrationsSection />
      case 'inbound':
        return <InboundSection />
      case 'updates':
        return settings ? <UpdatesSection settings={settings} /> : null
      case 'about':
        return <AboutSection version={version} />
    }
  }

  return (
    <div className="flex-1 flex overflow-hidden min-h-0">
      <SettingsSidebar
        ref={searchInputRef}
        selected={selected}
        query={query}
        onQueryChange={setQuery}
        onSelectCategory={(id) => {
          setSelected(id)
          setQuery('')
        }}
        onSelectResult={activateResult}
        resultCursor={resultCursor}
        onResultCursorChange={setResultCursor}
        resultsRef={resultsRef}
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="flex items-baseline gap-3 mb-6">
          <h2 className="font-heading text-xl font-semibold">{CATEGORY_LABELS[selected]}</h2>
          <span className="text-xs text-[var(--wf-text-muted)]">Settings</span>
        </div>
        <div className="max-w-lg">{renderSection()}</div>
      </div>
    </div>
  )
}
