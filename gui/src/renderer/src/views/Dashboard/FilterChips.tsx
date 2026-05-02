import { cn } from '../../lib/utils'
import type { DashboardCounts, DashboardFilter } from '../../lib/dashboard-filters'

interface FilterChipsProps {
  active: DashboardFilter
  counts: DashboardCounts
  onChange: (filter: DashboardFilter) => void
}

interface ChipDef {
  value: DashboardFilter
  label: string
}

const CHIPS: ChipDef[] = [
  { value: 'all', label: 'All' },
  { value: 'working', label: 'Working' },
  { value: 'needs-attention', label: 'Needs attention' },
  { value: 'idle', label: 'Idle' },
  { value: 'has-ready', label: 'Has ready tasks' }
]

export function FilterChips({ active, counts, onChange }: FilterChipsProps) {
  return (
    <div role="group" aria-label="Filter projects" className="flex flex-wrap items-center gap-1.5">
      {CHIPS.map(({ value, label }) => {
        const isActive = active === value
        return (
          <button
            key={value}
            type="button"
            aria-pressed={isActive}
            onClick={() => onChange(isActive && value !== 'all' ? 'all' : value)}
            className={cn(
              'inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium transition-colors',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fire-500/50',
              isActive
                ? 'bg-[var(--wf-fire)] text-white'
                : 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-secondary)] hover:text-[var(--wf-text-primary)]'
            )}
          >
            <span>{label}</span>
            <span
              className={cn(
                'tabular-nums',
                isActive ? 'text-white/80' : 'text-[var(--wf-text-muted)]'
              )}
            >
              ({counts[value]})
            </span>
          </button>
        )
      })}
    </div>
  )
}
