import { forwardRef, useMemo } from 'react'
import { Search, X } from 'lucide-react'
import { cn } from '../../lib/utils'
import {
  SETTINGS_CATEGORIES,
  type SettingsCategory,
  type SettingsCategoryId,
  type SettingsSearchEntry,
  matchCategories,
  matchSearchEntries
} from './searchIndex'

export interface SettingsSidebarProps {
  selected: SettingsCategoryId
  query: string
  onQueryChange: (q: string) => void
  onSelectCategory: (id: SettingsCategoryId) => void
  onSelectResult: (entry: SettingsSearchEntry) => void
  resultCursor: number
  onResultCursorChange: (n: number) => void
  resultsRef?: React.RefObject<HTMLUListElement | null>
}

export const SettingsSidebar = forwardRef<HTMLInputElement, SettingsSidebarProps>(
  function SettingsSidebar(props, searchRef) {
    const {
      selected,
      query,
      onQueryChange,
      onSelectCategory,
      onSelectResult,
      resultCursor,
      onResultCursorChange,
      resultsRef
    } = props

    const visibleCategories: SettingsCategory[] = useMemo(
      () => matchCategories(query),
      [query]
    )
    const results: SettingsSearchEntry[] = useMemo(
      () => matchSearchEntries(query),
      [query]
    )
    const searching = query.trim().length > 0

    return (
      <aside className="w-56 shrink-0 border-r border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] flex flex-col h-full">
        <div className="p-3 border-b border-[var(--wf-border)]">
          <div className="relative">
            <Search
              size={14}
              className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--wf-text-muted)] pointer-events-none"
            />
            <input
              ref={searchRef}
              type="text"
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder="Search"
              aria-label="Search settings"
              className="w-full pl-8 pr-7 py-1.5 text-sm rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 focus:ring-1 focus:ring-fire-500/30 transition-colors"
            />
            {query && (
              <button
                type="button"
                onClick={() => onQueryChange('')}
                aria-label="Clear search"
                className="absolute right-1.5 top-1/2 -translate-y-1/2 p-0.5 rounded text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] hover:bg-[var(--wf-bg-elevated)]"
              >
                <X size={12} />
              </button>
            )}
          </div>
        </div>

        <nav className="flex-1 overflow-y-auto py-2">
          {visibleCategories.length === 0 && !searching && (
            <p className="px-3 text-xs text-[var(--wf-text-muted)]">No categories.</p>
          )}
          <ul className="space-y-0.5 px-2">
            {visibleCategories.map((c) => {
              const Icon = c.icon
              const active = c.id === selected
              return (
                <li key={c.id}>
                  <button
                    type="button"
                    onClick={() => onSelectCategory(c.id)}
                    className={cn(
                      'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-[var(--wf-radius-md)] text-sm transition-colors',
                      active
                        ? 'bg-fire-500/15 text-fire-500'
                        : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)]'
                    )}
                  >
                    <Icon size={15} className={active ? 'text-fire-500' : 'text-[var(--wf-text-muted)]'} />
                    <span className="font-medium">{c.label}</span>
                  </button>
                </li>
              )
            })}
          </ul>

          {searching && (
            <div className="mt-3 pt-3 border-t border-[var(--wf-border)] px-2">
              <p className="px-1 mb-1.5 text-[10px] uppercase tracking-wider font-semibold text-[var(--wf-text-muted)]">
                {results.length === 0 ? 'No results' : `Results (${results.length})`}
              </p>
              <ul ref={resultsRef} className="space-y-0.5">
                {results.map((entry, i) => {
                  const cat = SETTINGS_CATEGORIES.find((c) => c.id === entry.category)
                  const active = i === resultCursor
                  return (
                    <li key={`${entry.category}-${entry.fieldId}`}>
                      <button
                        type="button"
                        onMouseEnter={() => onResultCursorChange(i)}
                        onClick={() => onSelectResult(entry)}
                        className={cn(
                          'w-full text-left px-2.5 py-1.5 rounded-[var(--wf-radius-md)] text-sm transition-colors',
                          active
                            ? 'bg-fire-500/15 text-[var(--wf-text-primary)]'
                            : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)]'
                        )}
                      >
                        <div className="font-medium leading-tight">{entry.label}</div>
                        <div className="text-[10px] text-[var(--wf-text-muted)] leading-tight mt-0.5">
                          {cat?.label}
                        </div>
                      </button>
                    </li>
                  )
                })}
              </ul>
            </div>
          )}
        </nav>
      </aside>
    )
  }
)
