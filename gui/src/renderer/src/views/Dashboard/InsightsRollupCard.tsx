// v6.0 Ember dashboard rollup card.
//
// Renders the cross-project insights summary directly under the v4 Beacon
// status bar — KPI strip, mini stacked-bar of tasks-per-day, top-projects
// pill list, and an agent stacked bar. Designed to be glanceable: every
// element has a fixed footprint so nothing reflows when the window
// selector flips between 7d / 30d / 90d / All.

import { useMemo, useState } from 'react'
import { AlertTriangle, Sparkles } from 'lucide-react'
import { useAppStore } from '../../stores/app-store'
import { useGlobalInsights } from '../../hooks/useGlobalInsights'
import {
  agentSegmentWidths,
  classifyRollup,
  dayBarHeights,
  formatCost,
  formatDuration,
  formatPercent,
  INSIGHTS_WINDOWS,
  readSavedWindow,
  saveWindow,
  successRate,
  type InsightsWindow
} from '../../lib/insights-rollup'
import { cn } from '../../lib/utils'
import type { GlobalInsights } from '../../generated/watchfire_pb'

const BAR_HEIGHT_PX = 64
const MAX_DAY_CELLS = 30

const AGENT_PALETTE = [
  '#f97316', // orange — primary accent
  '#3b82f6',
  '#22c55e',
  '#a855f7',
  '#06b6d4',
  '#ec4899',
  '#eab308'
]

function agentColor(idx: number): string {
  return AGENT_PALETTE[idx % AGENT_PALETTE.length]
}

const WINDOW_LABEL: Record<InsightsWindow, string> = {
  '7d': '7d',
  '30d': '30d',
  '90d': '90d',
  all: 'All'
}

export function InsightsRollupCard() {
  const [window, setWindow] = useState<InsightsWindow>(readSavedWindow)
  const { insights, loading, error } = useGlobalInsights(window)

  const updateWindow = (next: InsightsWindow) => {
    setWindow(next)
    saveWindow(next)
  }

  const state = classifyRollup(insights, loading)

  return (
    <section
      aria-label="Fleet insights"
      className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] p-4"
    >
      <header className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <Sparkles size={14} className="text-[var(--wf-fire)]" />
          <h3 className="text-sm font-semibold text-[var(--wf-text-primary)]">
            Fleet insights
          </h3>
        </div>
        <WindowSelector value={window} onChange={updateWindow} />
      </header>

      {error ? (
        <p className="text-xs text-[var(--wf-warning)]">
          Couldn&apos;t load insights: {error.message}
        </p>
      ) : state === 'loading' ? (
        <RollupSkeleton />
      ) : state === 'empty' ? (
        <EmptyState />
      ) : (
        <RollupBody insights={insights as GlobalInsights} />
      )}
    </section>
  )
}

interface WindowSelectorProps {
  value: InsightsWindow
  onChange: (next: InsightsWindow) => void
}

function WindowSelector({ value, onChange }: WindowSelectorProps) {
  return (
    <div
      role="group"
      aria-label="Insights window"
      className="inline-flex items-center gap-0.5 p-0.5 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-elevated)] shrink-0"
    >
      {INSIGHTS_WINDOWS.map((w) => {
        const active = w === value
        return (
          <button
            key={w}
            type="button"
            aria-pressed={active}
            onClick={() => onChange(w)}
            className={cn(
              'px-2 py-0.5 text-[11px] rounded-[var(--wf-radius-sm)] transition-colors',
              active
                ? 'bg-[var(--wf-bg-secondary)] text-[var(--wf-text-primary)] font-medium'
                : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]'
            )}
          >
            {WINDOW_LABEL[w]}
          </button>
        )
      })}
    </div>
  )
}

function EmptyState() {
  return (
    <div className="py-3 text-xs text-[var(--wf-text-muted)] text-center">
      No completed tasks in this window — run a task to populate insights.
    </div>
  )
}

function RollupSkeleton() {
  return (
    <div className="space-y-3 animate-pulse" aria-busy="true">
      <div className="grid grid-cols-4 gap-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-12 rounded bg-[var(--wf-bg-elevated)]" />
        ))}
      </div>
      <div className="h-16 rounded bg-[var(--wf-bg-elevated)]" />
      <div className="h-6 rounded bg-[var(--wf-bg-elevated)]" />
    </div>
  )
}

interface RollupBodyProps {
  insights: GlobalInsights
}

function RollupBody({ insights }: RollupBodyProps) {
  const selectProject = useAppStore((s) => s.selectProject)
  const partialCost = insights.tasksMissingCost > 0

  const successPct = useMemo(() => formatPercent(successRate(insights)), [insights])
  const durationLabel = useMemo(() => formatDuration(insights.totalDurationMs), [insights])
  const costLabel = useMemo(
    () => formatCost(insights.totalCostUsd, insights.tasksMissingCost),
    [insights]
  )

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
        <KpiCell label="Tasks" value={String(insights.tasksTotal)} />
        <KpiCell label="Success" value={successPct} />
        <KpiCell label="Time" value={durationLabel} />
        <KpiCell
          label="Cost"
          value={costLabel}
          warn={partialCost}
          warnHint={`${insights.tasksMissingCost} task${insights.tasksMissingCost === 1 ? '' : 's'} missing cost`}
        />
      </div>

      <DayStackedBar buckets={insights.tasksByDay} />

      <TopProjectsPills
        projects={insights.topProjects}
        onPick={(projectId) => selectProject(projectId)}
      />

      <AgentStackedBar agents={insights.agentBreakdown} />
    </div>
  )
}

interface KpiCellProps {
  label: string
  value: string
  warn?: boolean
  warnHint?: string
}

function KpiCell({ label, value, warn, warnHint }: KpiCellProps) {
  return (
    <div className="rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] px-3 py-2">
      <div className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)]">
        {label}
      </div>
      <div className="text-sm font-semibold text-[var(--wf-text-primary)] tabular-nums flex items-center gap-1">
        <span>{value}</span>
        {warn && (
          <span title={warnHint} aria-label={warnHint} className="text-[var(--wf-warning)]">
            <AlertTriangle size={11} />
          </span>
        )}
      </div>
    </div>
  )
}

interface DayStackedBarProps {
  buckets: GlobalInsights['tasksByDay']
}

function DayStackedBar({ buckets }: DayStackedBarProps) {
  // Trim to the last MAX_DAY_CELLS so a 90d window doesn't crush the
  // chart into invisible slivers; the SVG width adapts with flex.
  const slice = buckets.length > MAX_DAY_CELLS ? buckets.slice(-MAX_DAY_CELLS) : buckets
  const cells = useMemo(() => dayBarHeights(slice, BAR_HEIGHT_PX), [slice])

  if (cells.length === 0) {
    return (
      <div
        className="h-16 rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] flex items-center justify-center"
        aria-label="Tasks per day"
      >
        <span className="text-[10px] text-[var(--wf-text-muted)]">No daily activity</span>
      </div>
    )
  }

  return (
    <div
      className="rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] p-2"
      aria-label="Tasks per day"
    >
      <div className="flex items-end gap-0.5" style={{ height: `${BAR_HEIGHT_PX}px` }}>
        {cells.map((c) => (
          <div
            key={c.date}
            title={`${c.date}: ${c.total} tasks`}
            className="flex-1 flex flex-col-reverse min-w-[2px]"
          >
            <div
              style={{ height: `${c.succeededHeight}px`, backgroundColor: 'var(--wf-success, #22c55e)' }}
            />
            <div
              style={{ height: `${c.failedHeight}px`, backgroundColor: 'var(--wf-warning, #ef4444)' }}
            />
          </div>
        ))}
      </div>
    </div>
  )
}

interface TopProjectsPillsProps {
  projects: GlobalInsights['topProjects']
  onPick: (projectId: string) => void
}

function TopProjectsPills({ projects, onPick }: TopProjectsPillsProps) {
  if (projects.length === 0) {
    return null
  }
  return (
    <div className="flex flex-wrap gap-1.5" aria-label="Top projects">
      <span className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)] self-center pr-1">
        Top
      </span>
      {projects.map((p) => (
        <button
          key={p.projectId}
          type="button"
          onClick={() => onPick(p.projectId)}
          className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] bg-[var(--wf-bg-elevated)] hover:bg-[var(--wf-bg-tertiary,var(--wf-bg-elevated))] text-[var(--wf-text-secondary)] hover:text-[var(--wf-text-primary)] transition-colors"
        >
          <span
            aria-hidden="true"
            className="inline-block w-2 h-2 rounded-full"
            style={{ backgroundColor: p.projectColor || 'var(--wf-fire)' }}
          />
          <span className="font-medium">{p.projectName}</span>
          <span className="text-[var(--wf-text-muted)] tabular-nums">{p.count}</span>
        </button>
      ))}
    </div>
  )
}

interface AgentStackedBarProps {
  agents: GlobalInsights['agentBreakdown']
}

function AgentStackedBar({ agents }: AgentStackedBarProps) {
  const segments = useMemo(() => agentSegmentWidths(agents), [agents])
  if (segments.length === 0) {
    return null
  }
  return (
    <div aria-label="Agent breakdown">
      <div className="h-2 w-full flex rounded-full overflow-hidden bg-[var(--wf-bg-elevated)]">
        {segments.map((seg, i) => (
          <div
            key={seg.agent}
            title={`${seg.agent}: ${seg.count}`}
            style={{ width: `${seg.widthPercent}%`, backgroundColor: agentColor(i) }}
          />
        ))}
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-[var(--wf-text-muted)]">
        {segments.map((seg, i) => (
          <span key={seg.agent} className="inline-flex items-center gap-1">
            <span
              aria-hidden="true"
              className="inline-block w-1.5 h-1.5 rounded-full"
              style={{ backgroundColor: agentColor(i) }}
            />
            <span className="text-[var(--wf-text-secondary)] font-medium">{seg.agent}</span>
            <span className="tabular-nums">{seg.count}</span>
          </span>
        ))}
      </div>
    </div>
  )
}
