// v6.0 Ember dashboard rollup card.
//
// Renders the cross-project insights summary directly under the v4 Beacon
// status bar — KPI strip, mini stacked-bar of tasks-per-day, top-projects
// pill list, and an agent stacked bar. Designed to be glanceable: every
// element has a fixed footprint so nothing reflows when the window
// selector flips between 7d / 30d / 90d / All.

import { useMemo, useState, type ReactNode } from 'react'
import { AlertTriangle, Sparkles } from 'lucide-react'
import { useAppStore } from '../../stores/app-store'
import {
  agentSegmentWidths,
  aggregateDayBuckets,
  churnBarHeights,
  classifyRollup,
  codeCoverageNote,
  dayBarHeights,
  dayChartSummary,
  formatBucketLabel,
  formatCost,
  formatDuration,
  formatInt,
  formatLinesPair,
  formatPercent,
  formatSignedLines,
  hasCodeData,
  INSIGHTS_GRANULARITIES,
  INSIGHTS_WINDOWS,
  mergeRate,
  readSavedCompare,
  readSavedGranularity,
  saveCompare,
  saveGranularity,
  successRate,
  tooltipAnchor,
  type InsightsGranularity,
  type InsightsWindow
} from '../../lib/insights-rollup'
import { cn } from '../../lib/utils'
import type { GlobalInsights } from '../../generated/watchfire_pb'

const BAR_HEIGHT_PX = 64
const MAX_DAY_CELLS = 30

// MAX_TOP_PILLS caps the two leaderboard pill rows. As of v9.2 the daemon
// returns *every* active project in `top_projects` — the dashboard list rows
// need the full set to look up per-project churn — so truncating to a
// single-row leaderboard is this renderer's job.
const MAX_TOP_PILLS = 5

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

interface InsightsRollupCardProps {
  insights: GlobalInsights | null
  loading: boolean
  error: Error | null
  window: InsightsWindow
  onWindowChange: (next: InsightsWindow) => void
}

// The fleet-insights fetch + window state is lifted to Dashboard so the
// rollup card and the per-card "shipped" lines share one query (v8 Inferno —
// mission control). This component is now purely presentational.
export function InsightsRollupCard({
  insights,
  loading,
  error,
  window,
  onWindowChange
}: InsightsRollupCardProps) {
  const state = classifyRollup(insights, loading)

  // Chart controls (v10 Torch): bucket granularity + tasks-vs-lines compare.
  // Persisted like the window preset so the dashboard reopens as configured.
  const [granularity, setGranularity] = useState<InsightsGranularity>(readSavedGranularity)
  const [compareLines, setCompareLines] = useState<boolean>(readSavedCompare)
  const updateGranularity = (g: InsightsGranularity) => {
    setGranularity(g)
    saveGranularity(g)
  }
  const updateCompare = (on: boolean) => {
    setCompareLines(on)
    saveCompare(on)
  }

  return (
    <section
      aria-label="Fleet insights"
      className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] p-4"
    >
      <header className="flex items-center justify-between gap-2 mb-3">
        <div className="flex items-center gap-2 min-w-0">
          <Sparkles size={14} className="text-[var(--wf-fire)] shrink-0" />
          <h3 className="text-sm font-semibold text-[var(--wf-text-primary)] truncate">
            Fleet insights
          </h3>
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
          <CompareToggle on={compareLines} onChange={updateCompare} />
          <GranularitySelector value={granularity} onChange={updateGranularity} />
          <WindowSelector value={window} onChange={onWindowChange} />
        </div>
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
        <RollupBody
          insights={insights as GlobalInsights}
          granularity={granularity}
          compareLines={compareLines}
        />
      )}
    </section>
  )
}

const GRANULARITY_LABEL: Record<InsightsGranularity, string> = {
  day: 'D',
  week: 'W',
  month: 'M'
}

const GRANULARITY_TITLE: Record<InsightsGranularity, string> = {
  day: 'Aggregate per day',
  week: 'Aggregate per week',
  month: 'Aggregate per month'
}

function GranularitySelector({
  value,
  onChange
}: {
  value: InsightsGranularity
  onChange: (next: InsightsGranularity) => void
}) {
  return (
    <div
      role="group"
      aria-label="Chart granularity"
      className="inline-flex items-center gap-0.5 p-0.5 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-elevated)] shrink-0"
    >
      {INSIGHTS_GRANULARITIES.map((g) => {
        const active = g === value
        return (
          <button
            key={g}
            type="button"
            title={GRANULARITY_TITLE[g]}
            aria-pressed={active}
            onClick={() => onChange(g)}
            className={cn(
              'px-2 py-0.5 text-[11px] rounded-[var(--wf-radius-sm)] transition-colors',
              active
                ? 'bg-[var(--wf-bg-secondary)] text-[var(--wf-text-primary)] font-medium'
                : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]'
            )}
          >
            {GRANULARITY_LABEL[g]}
          </button>
        )
      })}
    </div>
  )
}

function CompareToggle({ on, onChange }: { on: boolean; onChange: (next: boolean) => void }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      title="Pair the tasks chart with an aligned lines-of-code chart"
      onClick={() => onChange(!on)}
      className={cn(
        'px-2 py-0.5 text-[11px] rounded-[var(--wf-radius-md)] border transition-colors shrink-0',
        on
          ? 'border-fire-500/40 bg-fire-500/15 text-fire-500 font-medium'
          : 'border-[var(--wf-border)] bg-[var(--wf-bg-elevated)] text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]'
      )}
    >
      vs lines
    </button>
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
  granularity: InsightsGranularity
  compareLines: boolean
}

function RollupBody({ insights, granularity, compareLines }: RollupBodyProps) {
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

      <ShippedKpis insights={insights} />

      <DayStackedBar
        buckets={insights.tasksByDay}
        granularity={granularity}
        compareLines={compareLines}
      />

      <TopProjectsPills
        projects={insights.topProjects}
        onPick={(projectId) => selectProject(projectId)}
      />

      <TopProjectsByChurn
        projects={insights.topProjects}
        onPick={(projectId) => selectProject(projectId)}
      />

      <AgentStackedBar agents={insights.agentBreakdown} />
    </div>
  )
}

// ShippedKpis — v8.0 Inferno fleet code-output strip. Renders commit volume,
// net-line delta and merge rate beneath the task KPIs, with an honest
// coverage caption. Hidden entirely when no task in the window carried code
// metrics (a fleet of pre-v8.0 tasks).
function ShippedKpis({ insights }: { insights: GlobalInsights }) {
  const coverage = useMemo(
    () => codeCoverageNote(insights.metricsMissingCode, insights.tasksTotal),
    [insights]
  )
  const mergePct = useMemo(
    () => formatPercent(mergeRate(insights.tasksMerged, insights.tasksTotal)),
    [insights]
  )

  if (!hasCodeData(insights)) return null

  return (
    <div aria-label="Shipped code" className="space-y-1">
      <div className="grid grid-cols-3 gap-2">
        <KpiCell label="Commits" value={formatInt(insights.totalCommits)} />
        <KpiCell
          label="Net lines"
          value={formatSignedLines(insights.netLines)}
          sub={formatLinesPair(insights.totalLinesAdded, insights.totalLinesRemoved)}
        />
        <KpiCell label="Merge rate" value={mergePct} />
      </div>
      {coverage && (
        <p className="text-[10px] text-[var(--wf-text-muted)] flex items-center gap-1">
          <AlertTriangle size={10} className="text-[var(--wf-warning)]" />
          {coverage}
        </p>
      )}
    </div>
  )
}

interface KpiCellProps {
  label: string
  value: string
  warn?: boolean
  warnHint?: string
  sub?: string
}

function KpiCell({ label, value, warn, warnHint, sub }: KpiCellProps) {
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
      {sub && (
        <div className="text-[9px] tabular-nums text-[var(--wf-text-muted)] mt-0.5 truncate">
          {sub}
        </div>
      )}
    </div>
  )
}

interface DayStackedBarProps {
  buckets: GlobalInsights['tasksByDay']
  granularity: InsightsGranularity
  compareLines: boolean
}

const CHURN_BAR_HEIGHT_PX = 36

const GRANULARITY_NOUN: Record<InsightsGranularity, string> = {
  day: 'day',
  week: 'week',
  month: 'month'
}

function DayStackedBar({ buckets, granularity, compareLines }: DayStackedBarProps) {
  // Re-bucket the daemon's per-day series to the selected granularity, then
  // (day view only) trim to the last MAX_DAY_CELLS so a 90d window doesn't
  // crush the chart into invisible slivers.
  const aggregated = useMemo(
    () => aggregateDayBuckets(buckets, granularity),
    [buckets, granularity]
  )
  const slice =
    granularity === 'day' && aggregated.length > MAX_DAY_CELLS
      ? aggregated.slice(-MAX_DAY_CELLS)
      : aggregated
  const cells = useMemo(() => dayBarHeights(slice, BAR_HEIGHT_PX), [slice])
  const summary = useMemo(() => dayChartSummary(slice), [slice])
  const churnCells = useMemo(
    () => (compareLines ? churnBarHeights(slice, CHURN_BAR_HEIGHT_PX) : []),
    [slice, compareLines]
  )
  const [hovered, setHovered] = useState<number | null>(null)

  const noun = GRANULARITY_NOUN[granularity]

  if (cells.length === 0) {
    return (
      <div
        className="h-16 rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] flex items-center justify-center"
        aria-label={`Tasks per ${noun}`}
      >
        <span className="text-[10px] text-[var(--wf-text-muted)]">No activity</span>
      </div>
    )
  }

  const truncated = aggregated.length > slice.length
  const hasChurn = summary.linesAdded > 0 || summary.linesRemoved > 0
  const showChurnRow = compareLines && hasChurn
  const bucket = hovered === null ? null : slice[hovered]
  const anchor = hovered === null ? null : tooltipAnchor(hovered, cells.length)

  return (
    <div
      className="rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] p-2"
      role="img"
      aria-label={
        `Tasks per ${noun} over ${summary.days} ${noun}s: ${summary.total} tasks, ` +
        `peak ${summary.peak} in a ${noun}, ${summary.succeeded} succeeded, ${summary.failed} failed`
      }
    >
      <ChartHeader
        label={truncated ? `Tasks per ${noun} · last ${slice.length}d` : `Tasks per ${noun}`}
        stats={
          <>
            <span title={`Busiest single ${noun} in the window`}>peak {formatInt(summary.peak)}</span>
            <Dot />
            <span title={`${summary.activeDays} of ${summary.days} ${noun}s had activity`}>
              {formatInt(summary.total)} total
            </span>
            {hasChurn && (
              <>
                <Dot />
                <span title="Lines added / removed across the window">
                  {formatLinesPair(summary.linesAdded, summary.linesRemoved)}
                </span>
              </>
            )}
          </>
        }
      />

      <div className="relative" onMouseLeave={() => setHovered(null)}>
        <div className="flex items-stretch gap-0.5" style={{ height: `${BAR_HEIGHT_PX}px` }}>
          {cells.map((c, i) => (
            <div
              key={c.date}
              onMouseEnter={() => setHovered(i)}
              // Full-column hit area — a 2px-tall bar on a quiet day would
              // otherwise be almost impossible to hover.
              className={cn(
                'flex-1 flex flex-col-reverse min-w-[2px] h-full rounded-[1px] transition-opacity',
                hovered !== null && hovered !== i && 'opacity-40',
                hovered === i && 'bg-[var(--wf-bg-secondary)]'
              )}
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

        {/* Aligned lines-of-code row ("vs lines"): same buckets, same column
            order and shared hover, so tasks and churn compare per-column. */}
        {showChurnRow && (
          <>
            <div className="mt-1.5 mb-0.5 text-[9px] uppercase tracking-wide text-[var(--wf-text-muted)]">
              Lines changed
            </div>
            <div
              className="flex items-stretch gap-0.5"
              style={{ height: `${CHURN_BAR_HEIGHT_PX}px` }}
              aria-label={`Lines changed per ${noun}`}
            >
              {churnCells.map((c, i) => (
                <div
                  key={c.date}
                  onMouseEnter={() => setHovered(i)}
                  className={cn(
                    'flex-1 flex flex-col-reverse min-w-[2px] h-full rounded-[1px] transition-opacity',
                    hovered !== null && hovered !== i && 'opacity-40',
                    hovered === i && 'bg-[var(--wf-bg-secondary)]'
                  )}
                >
                  <div style={{ height: `${c.addedHeight}px`, backgroundColor: '#3b82f6' }} />
                  <div style={{ height: `${c.removedHeight}px`, backgroundColor: '#94a3b8' }} />
                </div>
              ))}
            </div>
            <div className="mt-1 flex items-center gap-3 text-[9px] text-[var(--wf-text-muted)]">
              <span className="inline-flex items-center gap-1">
                <span className="inline-block w-1.5 h-1.5 rounded-full" style={{ backgroundColor: '#3b82f6' }} />
                added
              </span>
              <span className="inline-flex items-center gap-1">
                <span className="inline-block w-1.5 h-1.5 rounded-full" style={{ backgroundColor: '#94a3b8' }} />
                removed
              </span>
            </div>
          </>
        )}

        {bucket && anchor && (
          <ChartTooltip anchor={anchor} title={formatBucketLabel(bucket.date, granularity)}>
            <TooltipRow
              label={`${formatInt(bucket.count)} task${bucket.count === 1 ? '' : 's'}`}
              detail={
                bucket.failed > 0
                  ? `${formatInt(bucket.succeeded)} ok · ${formatInt(bucket.failed)} failed`
                  : undefined
              }
            />
            {(bucket.linesAdded > 0 || bucket.linesRemoved > 0) && (
              <TooltipRow
                label={`${formatLinesPair(bucket.linesAdded, bucket.linesRemoved)} lines`}
              />
            )}
          </ChartTooltip>
        )}
      </div>
    </div>
  )
}

// --- Shared chart chrome (v9.2) -----------------------------------------
// Before v9.2 both charts were number-free and leaned on the native `title`
// attribute, which has a ~1s delay, can't be styled, and shows one flat
// string. These give every chart an always-visible headline plus an instant,
// styled hover card.

function ChartHeader({ label, stats }: { label: string; stats: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-2 mb-1.5">
      <span className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)] shrink-0">
        {label}
      </span>
      <span className="flex items-center text-[10px] tabular-nums text-[var(--wf-text-secondary)] truncate">
        {stats}
      </span>
    </div>
  )
}

function Dot() {
  return <span className="mx-1.5 text-[var(--wf-border)]">·</span>
}

interface ChartTooltipProps {
  anchor: { leftPercent: number; transform: string }
  title: string
  swatch?: string
  children: ReactNode
}

function ChartTooltip({ anchor, title, swatch, children }: ChartTooltipProps) {
  return (
    <div
      // pointer-events-none so the card can never steal the hover from the
      // bar underneath it and cause a flicker loop.
      className="pointer-events-none absolute z-20 bottom-full mb-1.5 whitespace-nowrap rounded-[var(--wf-radius-sm)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] px-2 py-1.5 shadow-lg"
      style={{ left: `${anchor.leftPercent}%`, transform: anchor.transform }}
      role="tooltip"
    >
      <div className="flex items-center gap-1.5 text-[11px] font-medium text-[var(--wf-text-primary)]">
        {swatch && (
          <span
            aria-hidden="true"
            className="inline-block w-1.5 h-1.5 rounded-full shrink-0"
            style={{ backgroundColor: swatch }}
          />
        )}
        {title}
      </div>
      <div className="mt-0.5 space-y-0.5">{children}</div>
    </div>
  )
}

function TooltipRow({ label, detail }: { label: string; detail?: string }) {
  return (
    <div className="text-[10px] tabular-nums text-[var(--wf-text-secondary)]">
      {label}
      {detail && (
        <>
          <Dot />
          <span className="text-[var(--wf-text-muted)]">{detail}</span>
        </>
      )}
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
  const shown = projects.slice(0, MAX_TOP_PILLS)
  const more = projects.length - shown.length
  return (
    <div className="flex flex-wrap gap-1.5" aria-label="Top projects">
      <span className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)] self-center pr-1">
        Top
      </span>
      {shown.map((p) => (
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
      {more > 0 && <MorePill count={more} />}
    </div>
  )
}

// MorePill keeps the leaderboard honest about truncation now that the daemon
// returns every active project (v9.2) — silently showing five of twelve read
// as "these are all my projects".
function MorePill({ count }: { count: number }) {
  return (
    <span
      className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] text-[var(--wf-text-muted)]"
      title={`${count} more project${count === 1 ? '' : 's'} active in this window`}
    >
      +{count} more
    </span>
  )
}

interface TopProjectsByChurnProps {
  projects: GlobalInsights['topProjects']
  onPick: (projectId: string) => void
}

// TopProjectsByChurn — v8.0 Inferno. Re-ranks the surfaced top projects by
// net lines shipped so the fleet view answers "who shipped the most code?",
// not only "who closed the most tasks?". Hidden when no project carried code
// metrics in the window.
function TopProjectsByChurn({ projects, onPick }: TopProjectsByChurnProps) {
  const ranked = useMemo(() => {
    return projects
      .filter((p) => Number(p.linesAdded) > 0 || Number(p.linesRemoved) > 0)
      .slice()
      .sort((a, b) => Number(b.netLines) - Number(a.netLines))
  }, [projects])

  if (ranked.length === 0) return null

  const shown = ranked.slice(0, MAX_TOP_PILLS)
  const more = ranked.length - shown.length

  return (
    <div className="flex flex-wrap gap-1.5" aria-label="Top projects by churn">
      <span className="text-[10px] uppercase tracking-wide text-[var(--wf-text-muted)] self-center pr-1">
        Churn
      </span>
      {shown.map((p) => (
        <button
          key={p.projectId}
          type="button"
          onClick={() => onPick(p.projectId)}
          title={`+${Number(p.linesAdded)} / −${Number(p.linesRemoved)} · ${Number(p.commits)} commits`}
          className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] bg-[var(--wf-bg-elevated)] hover:bg-[var(--wf-bg-tertiary,var(--wf-bg-elevated))] text-[var(--wf-text-secondary)] hover:text-[var(--wf-text-primary)] transition-colors"
        >
          <span
            aria-hidden="true"
            className="inline-block w-2 h-2 rounded-full"
            style={{ backgroundColor: p.projectColor || 'var(--wf-fire)' }}
          />
          <span className="font-medium">{p.projectName}</span>
          <span className="text-[var(--wf-text-muted)] tabular-nums">
            {formatSignedLines(p.netLines)}
          </span>
        </button>
      ))}
      {more > 0 && <MorePill count={more} />}
    </div>
  )
}

interface AgentStackedBarProps {
  agents: GlobalInsights['agentBreakdown']
}

function AgentStackedBar({ agents }: AgentStackedBarProps) {
  const segments = useMemo(() => agentSegmentWidths(agents), [agents])
  const rowByAgent = useMemo(
    () => new Map(agents.map((a) => [a.agent, a])),
    [agents]
  )
  // Per-agent net-line lookup so the legend can compare output, not just
  // task share. Only surfaced when some agent shipped code.
  const netByAgent = useMemo(() => {
    const map = new Map<string, number>()
    let any = false
    for (const a of agents) {
      const net = Number(a.linesAdded) - Number(a.linesRemoved)
      if (Number(a.linesAdded) > 0 || Number(a.linesRemoved) > 0) any = true
      map.set(a.agent, net)
    }
    return any ? map : null
  }, [agents])
  const totalTasks = useMemo(
    () => segments.reduce((sum, s) => sum + s.count, 0),
    [segments]
  )
  const [hovered, setHovered] = useState<number | null>(null)

  if (segments.length === 0) {
    return null
  }

  const seg = hovered === null ? null : segments[hovered]
  const row = seg ? rowByAgent.get(seg.agent) : undefined
  // Segments are variable-width, so anchor the card at the midpoint of the
  // hovered segment rather than at an even slot boundary.
  const segLeft =
    hovered === null
      ? 0
      : segments.slice(0, hovered).reduce((sum, s) => sum + s.widthPercent, 0) +
        segments[hovered].widthPercent / 2
  const anchor =
    hovered === null
      ? null
      : {
          leftPercent: segLeft,
          transform:
            segLeft < 20
              ? 'translateX(0)'
              : segLeft > 80
                ? 'translateX(-100%)'
                : 'translateX(-50%)'
        }

  return (
    <div aria-label={`Agent breakdown: ${formatInt(totalTasks)} tasks across ${segments.length} agents`}>
      <ChartHeader
        label="By agent"
        stats={
          <>
            <span>{formatInt(totalTasks)} tasks</span>
            <Dot />
            <span>
              {segments.length} agent{segments.length === 1 ? '' : 's'}
            </span>
          </>
        }
      />
      <div className="relative" onMouseLeave={() => setHovered(null)}>
        <div className="h-2 w-full flex rounded-full overflow-hidden bg-[var(--wf-bg-elevated)]">
          {segments.map((s, i) => (
            <div
              key={s.agent}
              onMouseEnter={() => setHovered(i)}
              className={cn('transition-opacity', hovered !== null && hovered !== i && 'opacity-40')}
              style={{ width: `${s.widthPercent}%`, backgroundColor: agentColor(i) }}
            />
          ))}
        </div>
        {seg && anchor && (
          <ChartTooltip anchor={anchor} title={seg.agent} swatch={agentColor(hovered as number)}>
            <TooltipRow
              label={`${formatInt(seg.count)} task${seg.count === 1 ? '' : 's'}`}
              detail={`${seg.widthPercent}% of fleet`}
            />
            {row && (
              <TooltipRow
                label={`${formatPercent(row.successRate)} success`}
                detail={
                  Number(row.avgDurationMs) > 0
                    ? `${formatDuration(row.avgDurationMs)} avg`
                    : undefined
                }
              />
            )}
            {row && (Number(row.linesAdded) > 0 || Number(row.linesRemoved) > 0) && (
              <TooltipRow
                label={`${formatLinesPair(row.linesAdded, row.linesRemoved)} lines`}
                detail={
                  Number(row.commits) > 0
                    ? `${formatInt(row.commits)} commit${Number(row.commits) === 1 ? '' : 's'}`
                    : undefined
                }
              />
            )}
          </ChartTooltip>
        )}
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
            {netByAgent && (
              <span className="tabular-nums text-[var(--wf-text-muted)]">
                · {formatSignedLines(netByAgent.get(seg.agent) ?? 0)}
              </span>
            )}
          </span>
        ))}
      </div>
    </div>
  )
}
