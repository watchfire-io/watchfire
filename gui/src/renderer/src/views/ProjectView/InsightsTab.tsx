// v6.0 Ember per-project Insights tab.
//
// The companion to the v6.0 dashboard rollup card. Rendered as a fifth
// (sixth, with this tab) center tab on the per-project view. Layout:
// KPI strip → tasks-per-day stacked bar → agent donut + table → duration
// histogram. Window selector chip group at the top (7d / 30d / 90d / All).
//
// Charts are drawn with raw SVG / flex divs to keep the dep tree light —
// matching the approach the dashboard rollup card already uses. The cost
// KPI surfaces a partial-data caveat icon when some completed tasks
// don't carry cost numbers (task 0056 still gates that data).

import { useMemo, useState } from 'react'
import { AlertTriangle, Sparkles } from 'lucide-react'
import { useProjectInsights } from '../../hooks/useProjectInsights'
import {
  agentSegmentWidths,
  dayBarHeights,
  formatCost,
  formatDuration,
  formatPercent,
  INSIGHTS_WINDOWS,
  readSavedWindow,
  saveWindow,
  type InsightsWindow
} from '../../lib/insights-rollup'
import { cn } from '../../lib/utils'
import type { ProjectInsights } from '../../generated/watchfire_pb'

interface Props {
  projectId: string
}

const BAR_HEIGHT_PX = 96
const MAX_DAY_CELLS = 90

const AGENT_PALETTE = [
  '#f97316',
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

export function InsightsTab({ projectId }: Props) {
  const [window, setWindow] = useState<InsightsWindow>(readSavedWindow)
  const { insights, loading, error } = useProjectInsights(projectId, window)

  const updateWindow = (next: InsightsWindow) => {
    setWindow(next)
    saveWindow(next)
  }

  return (
    <div className="flex-1 flex flex-col overflow-auto">
      <div className="px-6 py-4 flex flex-col gap-4 max-w-5xl">
        <header className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Sparkles size={16} className="text-[var(--wf-fire)]" />
            <h3 className="text-sm font-semibold text-[var(--wf-text-primary)]">
              Project insights
            </h3>
          </div>
          <WindowSelector value={window} onChange={updateWindow} />
        </header>

        {error ? (
          <p className="text-xs text-[var(--wf-warning)]">
            Couldn&apos;t load insights: {error.message}
          </p>
        ) : loading && !insights ? (
          <InsightsSkeleton />
        ) : !insights || insights.tasksTotal === 0 ? (
          <EmptyState />
        ) : (
          <InsightsBody insights={insights} />
        )}
      </div>
    </div>
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
      className="inline-flex items-center gap-0.5 p-0.5 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-elevated)]"
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
    <div className="rounded-[var(--wf-radius-md)] border border-dashed border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] p-8 text-center">
      <p className="text-sm text-[var(--wf-text-muted)]">
        No completed tasks yet — run a task to populate insights.
      </p>
    </div>
  )
}

function InsightsSkeleton() {
  return (
    <div className="space-y-4 animate-pulse" aria-busy="true">
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-16 rounded bg-[var(--wf-bg-elevated)]" />
        ))}
      </div>
      <div className="h-32 rounded bg-[var(--wf-bg-elevated)]" />
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        <div className="h-48 rounded bg-[var(--wf-bg-elevated)]" />
        <div className="h-48 rounded bg-[var(--wf-bg-elevated)]" />
      </div>
    </div>
  )
}

interface BodyProps {
  insights: ProjectInsights
}

function InsightsBody({ insights }: BodyProps) {
  const partialCost = insights.tasksMissingCost > 0

  const successPct = useMemo(() => {
    const total = Number(insights.tasksTotal)
    if (total <= 0) return formatPercent(0)
    return formatPercent(Number(insights.tasksSucceeded) / total)
  }, [insights])
  const totalDuration = useMemo(
    () => formatDuration(insights.totalDurationMs),
    [insights]
  )
  const costLabel = useMemo(
    () => formatCost(insights.totalCostUsd, insights.tasksMissingCost),
    [insights]
  )

  return (
    <div className="space-y-4">
      <section className="grid grid-cols-2 sm:grid-cols-4 gap-3" aria-label="Headline metrics">
        <KpiCell label="Tasks" value={String(insights.tasksTotal)} />
        <KpiCell label="Success" value={successPct} />
        <KpiCell label="Time" value={totalDuration} />
        <KpiCell
          label="Cost"
          value={costLabel}
          warn={partialCost}
          warnHint={`${insights.tasksMissingCost} task${insights.tasksMissingCost === 1 ? '' : 's'} missing cost`}
        />
      </section>

      <section
        aria-label="Tasks per day"
        className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] p-4"
      >
        <header className="flex items-center justify-between mb-2">
          <h4 className="text-xs font-semibold text-[var(--wf-text-secondary)]">
            Tasks per day
          </h4>
          <LegendDot label="Succeeded" color="var(--wf-success, #22c55e)">
            <LegendDot label="Failed" color="var(--wf-warning, #ef4444)" />
          </LegendDot>
        </header>
        <DayStackedBar buckets={insights.tasksByDay} />
      </section>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        <section
          aria-label="Agent breakdown"
          className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] p-4"
        >
          <h4 className="text-xs font-semibold text-[var(--wf-text-secondary)] mb-3">
            Agent breakdown
          </h4>
          <AgentDonut agents={insights.agentBreakdown} />
          <AgentTable agents={insights.agentBreakdown} />
        </section>

        <section
          aria-label="Duration"
          className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] p-4"
        >
          <h4 className="text-xs font-semibold text-[var(--wf-text-secondary)] mb-3">
            Duration
          </h4>
          <DurationHistogram insights={insights} />
        </section>
      </div>
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
    <div className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] px-4 py-3">
      <div className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)]">
        {label}
      </div>
      <div className="text-base font-semibold text-[var(--wf-text-primary)] tabular-nums flex items-center gap-1.5 mt-0.5">
        <span>{value}</span>
        {warn && (
          <span title={warnHint} aria-label={warnHint} className="text-[var(--wf-warning)]">
            <AlertTriangle size={13} />
          </span>
        )}
      </div>
    </div>
  )
}

interface LegendDotProps {
  label: string
  color: string
  children?: React.ReactNode
}

function LegendDot({ label, color, children }: LegendDotProps) {
  return (
    <span className="inline-flex items-center gap-3 text-[10px] text-[var(--wf-text-muted)]">
      <span className="inline-flex items-center gap-1">
        <span aria-hidden="true" className="inline-block w-2 h-2 rounded-sm" style={{ backgroundColor: color }} />
        {label}
      </span>
      {children}
    </span>
  )
}

interface DayStackedBarProps {
  buckets: ProjectInsights['tasksByDay']
}

function DayStackedBar({ buckets }: DayStackedBarProps) {
  const slice = buckets.length > MAX_DAY_CELLS ? buckets.slice(-MAX_DAY_CELLS) : buckets
  const cells = useMemo(() => dayBarHeights(slice, BAR_HEIGHT_PX), [slice])

  if (cells.length === 0) {
    return (
      <div
        className="h-24 rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] flex items-center justify-center"
      >
        <span className="text-[10px] text-[var(--wf-text-muted)]">No daily activity</span>
      </div>
    )
  }

  return (
    <div className="rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] p-3">
      <div className="flex items-end gap-0.5" style={{ height: `${BAR_HEIGHT_PX}px` }}>
        {cells.map((c) => (
          <div
            key={c.date}
            title={`${c.date}: ${c.total} tasks`}
            className="flex-1 flex flex-col-reverse min-w-[3px]"
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

interface AgentDonutProps {
  agents: ProjectInsights['agentBreakdown']
}

const DONUT_SIZE = 140
const DONUT_RADIUS = 56
const DONUT_STROKE = 22

function AgentDonut({ agents }: AgentDonutProps) {
  const segments = useMemo(() => agentSegmentWidths(agents), [agents])
  const totalCount = useMemo(
    () => agents.reduce((sum, a) => sum + Number(a.count), 0),
    [agents]
  )

  if (segments.length === 0) {
    return (
      <div className="h-32 flex items-center justify-center text-[10px] text-[var(--wf-text-muted)]">
        No agents in this window
      </div>
    )
  }

  const cx = DONUT_SIZE / 2
  const cy = DONUT_SIZE / 2
  const circumference = 2 * Math.PI * DONUT_RADIUS

  let offset = 0
  return (
    <div className="flex items-center justify-center pb-3">
      <svg width={DONUT_SIZE} height={DONUT_SIZE} viewBox={`0 0 ${DONUT_SIZE} ${DONUT_SIZE}`}>
        <circle
          cx={cx}
          cy={cy}
          r={DONUT_RADIUS}
          fill="none"
          stroke="var(--wf-bg-elevated)"
          strokeWidth={DONUT_STROKE}
        />
        {segments.map((seg, i) => {
          const dash = (seg.widthPercent / 100) * circumference
          const gap = circumference - dash
          const result = (
            <circle
              key={seg.agent}
              cx={cx}
              cy={cy}
              r={DONUT_RADIUS}
              fill="none"
              stroke={agentColor(i)}
              strokeWidth={DONUT_STROKE}
              strokeDasharray={`${dash} ${gap}`}
              strokeDashoffset={-offset}
              transform={`rotate(-90 ${cx} ${cy})`}
            >
              <title>{`${seg.agent}: ${seg.count}`}</title>
            </circle>
          )
          offset += dash
          return result
        })}
        <text
          x={cx}
          y={cy - 2}
          textAnchor="middle"
          className="fill-[var(--wf-text-primary)]"
          style={{ fontSize: 18, fontWeight: 600 }}
        >
          {totalCount}
        </text>
        <text
          x={cx}
          y={cy + 14}
          textAnchor="middle"
          className="fill-[var(--wf-text-muted)]"
          style={{ fontSize: 10 }}
        >
          tasks
        </text>
      </svg>
    </div>
  )
}

interface AgentTableProps {
  agents: ProjectInsights['agentBreakdown']
}

function AgentTable({ agents }: AgentTableProps) {
  if (agents.length === 0) return null
  return (
    <table className="w-full text-[11px]">
      <thead className="text-[var(--wf-text-muted)]">
        <tr>
          <th className="text-left font-medium pb-1">Agent</th>
          <th className="text-right font-medium pb-1">Tasks</th>
          <th className="text-right font-medium pb-1">Success</th>
          <th className="text-right font-medium pb-1">Avg</th>
        </tr>
      </thead>
      <tbody>
        {agents.map((a, i) => (
          <tr key={a.agent} className="border-t border-[var(--wf-border)]">
            <td className="py-1.5 text-[var(--wf-text-primary)]">
              <span className="inline-flex items-center gap-1.5">
                <span
                  aria-hidden="true"
                  className="inline-block w-2 h-2 rounded-full"
                  style={{ backgroundColor: agentColor(i) }}
                />
                <span className="font-medium">{a.agent}</span>
              </span>
            </td>
            <td className="text-right tabular-nums py-1.5">{Number(a.count)}</td>
            <td className="text-right tabular-nums py-1.5">{formatPercent(a.successRate)}</td>
            <td className="text-right tabular-nums py-1.5 text-[var(--wf-text-muted)]">
              {formatDuration(a.avgDurationMs)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

interface DurationHistogramProps {
  insights: ProjectInsights
}

function DurationHistogram({ insights }: DurationHistogramProps) {
  const rows = [
    { label: 'avg', valueMs: Number(insights.avgDurationMs) },
    { label: 'p50', valueMs: Number(insights.p50DurationMs) },
    { label: 'p95', valueMs: Number(insights.p95DurationMs) }
  ]
  const peak = Math.max(1, ...rows.map((r) => r.valueMs))

  return (
    <div className="space-y-2">
      {rows.map((r) => {
        const widthPct = peak > 0 ? Math.round((r.valueMs / peak) * 100) : 0
        return (
          <div key={r.label} className="flex items-center gap-2">
            <div className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)] w-8">
              {r.label}
            </div>
            <div className="flex-1 h-3 rounded-full bg-[var(--wf-bg-elevated)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{ width: `${widthPct}%`, backgroundColor: 'var(--wf-fire, #f97316)' }}
              />
            </div>
            <div className="text-[11px] tabular-nums text-[var(--wf-text-primary)] w-20 text-right">
              {formatDuration(r.valueMs)}
            </div>
          </div>
        )
      })}
    </div>
  )
}
