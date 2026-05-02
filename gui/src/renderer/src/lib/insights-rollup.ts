// Pure helpers for the v6.0 Ember dashboard rollup card. Kept out of the
// React component so the formatting + bar-sizing logic is unit-testable
// without spinning up a React renderer (matching the .test.mjs pattern
// used by useExportReport).

import type { GlobalInsights } from '../generated/watchfire_pb'

export const INSIGHTS_WINDOWS = ['7d', '30d', '90d', 'all'] as const
export type InsightsWindow = (typeof INSIGHTS_WINDOWS)[number]

export const INSIGHTS_WINDOW_KEY = 'wf-insights-window'
export const DEFAULT_INSIGHTS_WINDOW: InsightsWindow = '30d'

/** windowToRange returns ISO Date bounds for the given preset. `all` returns
 *  `{ start: undefined, end: undefined }` so the daemon treats it as
 *  unbounded. The dashboard end is always "now" so today's tasks show. */
export function windowToRange(
  window: InsightsWindow,
  now: Date = new Date()
): { start?: Date; end?: Date } {
  if (window === 'all') return {}
  const days = window === '7d' ? 7 : window === '90d' ? 90 : 30
  const start = new Date(now)
  start.setDate(start.getDate() - days)
  return { start, end: now }
}

export function readSavedWindow(): InsightsWindow {
  try {
    const saved = localStorage.getItem(INSIGHTS_WINDOW_KEY)
    if (saved && (INSIGHTS_WINDOWS as readonly string[]).includes(saved)) {
      return saved as InsightsWindow
    }
  } catch {
    /* storage unavailable — fall through */
  }
  return DEFAULT_INSIGHTS_WINDOW
}

export function saveWindow(window: InsightsWindow): void {
  try {
    localStorage.setItem(INSIGHTS_WINDOW_KEY, window)
  } catch {
    /* storage unavailable — ignore */
  }
}

/** formatDuration turns a duration in milliseconds into the compact
 *  display the rollup KPI strip uses ("71h 04m"). Hours dominate the
 *  format because the dashboard window is always days-long; sub-minute
 *  totals collapse to "0m" rather than seconds noise. */
export function formatDuration(ms: number | bigint): string {
  const total = typeof ms === 'bigint' ? Number(ms) : ms
  if (!Number.isFinite(total) || total <= 0) return '0m'
  const totalMin = Math.floor(total / 60_000)
  if (totalMin < 60) return `${totalMin}m`
  const hr = Math.floor(totalMin / 60)
  const minPad = String(totalMin % 60).padStart(2, '0')
  return `${hr}h ${minPad}m`
}

/** formatCost formats a USD total with a partial-data caveat suffix when
 *  some completed tasks didn't carry a cost number. */
export function formatCost(cost: number, missing: number): string {
  const usd = `$${cost.toFixed(2)}`
  if (missing > 0) {
    return `${usd} (${missing} part)`
  }
  return usd
}

/** formatPercent renders a 0..1 ratio as a whole-number percentage. */
export function formatPercent(rate: number): string {
  if (!Number.isFinite(rate) || rate <= 0) return '0%'
  return `${Math.round(rate * 100)}%`
}

/** successRate computes the rollup-level success ratio as a number in
 *  [0, 1]. Returns 0 when no tasks are in scope (so the KPI shows "0%"
 *  instead of NaN). */
export function successRate(insights: Pick<GlobalInsights, 'tasksTotal' | 'tasksSucceeded'>): number {
  const total = Number(insights.tasksTotal)
  if (total <= 0) return 0
  return Number(insights.tasksSucceeded) / total
}

/** dayBarHeights converts the tasks_by_day series into a list of
 *  pixel-relative cell heights so the SVG bars can be laid out without
 *  a chart library. The returned list keeps the original ordering. */
export interface DayCell {
  date: string
  succeededHeight: number
  failedHeight: number
  total: number
}

export function dayBarHeights(
  buckets: ReadonlyArray<{ date: string; succeeded: number; failed: number; count: number }>,
  maxHeightPx: number
): DayCell[] {
  if (buckets.length === 0) return []
  const peak = buckets.reduce((m, b) => Math.max(m, b.count), 0)
  if (peak <= 0) {
    return buckets.map((b) => ({ date: b.date, succeededHeight: 0, failedHeight: 0, total: 0 }))
  }
  return buckets.map((b) => {
    const succeededHeight = (b.succeeded / peak) * maxHeightPx
    const failedHeight = (b.failed / peak) * maxHeightPx
    return {
      date: b.date,
      succeededHeight: Math.round(succeededHeight),
      failedHeight: Math.round(failedHeight),
      total: b.count
    }
  })
}

/** agentSegmentWidths spreads agent counts proportionally across the bar
 *  width. Zero-count agents are filtered out. The widths are rounded to
 *  one decimal — the residual rounding error is absorbed into the last
 *  segment so the bar always sums to 100%. */
export interface AgentSegment {
  agent: string
  count: number
  widthPercent: number
}

export function agentSegmentWidths(
  agents: ReadonlyArray<{ agent: string; count: number }>
): AgentSegment[] {
  const filtered = agents.filter((a) => a.count > 0)
  const total = filtered.reduce((sum, a) => sum + a.count, 0)
  if (total <= 0) return []
  let runningPct = 0
  const out: AgentSegment[] = []
  for (let i = 0; i < filtered.length; i++) {
    const a = filtered[i]
    const isLast = i === filtered.length - 1
    const raw = (a.count / total) * 100
    const widthPercent = isLast ? Math.max(0, 100 - runningPct) : Math.round(raw * 10) / 10
    runningPct += widthPercent
    out.push({ agent: a.agent, count: a.count, widthPercent })
  }
  return out
}

/** PartialDataState summarises the three render modes the rollup card
 *  cycles through. Used by snapshot-style tests + the empty/loading
 *  branches in the React component. */
export type RollupRenderState = 'empty' | 'partial' | 'full' | 'loading'

export function classifyRollup(
  insights: GlobalInsights | null,
  loading: boolean
): RollupRenderState {
  if (loading && !insights) return 'loading'
  if (!insights || insights.tasksTotal === 0) return 'empty'
  if (insights.tasksMissingCost > 0 || insights.topProjects.length === 0) return 'partial'
  return 'full'
}
