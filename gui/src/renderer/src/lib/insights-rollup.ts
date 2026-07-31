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

/** formatInt renders an integer count with thousands separators. Accepts
 *  the number | bigint the generated proto fields surface. */
export function formatInt(value: number | bigint): string {
  const n = typeof value === 'bigint' ? Number(value) : value
  if (!Number.isFinite(n)) return '0'
  return Math.round(n).toLocaleString()
}

/** formatSignedLines renders a net-line delta with an explicit sign, using a
 *  real minus glyph (U+2212) so "−97" lines up under "+412". Zero renders as
 *  a bare "0". */
export function formatSignedLines(value: number | bigint): string {
  const n = typeof value === 'bigint' ? Number(value) : value
  if (!Number.isFinite(n) || n === 0) return '0'
  if (n > 0) return `+${Math.round(n).toLocaleString()}`
  return `−${Math.round(Math.abs(n)).toLocaleString()}`
}

/** formatLinesPair renders the added/removed churn pair the "shipped" line
 *  uses ("+412 / −97"). */
export function formatLinesPair(added: number | bigint, removed: number | bigint): string {
  const a = typeof added === 'bigint' ? Number(added) : added
  const r = typeof removed === 'bigint' ? Number(removed) : removed
  return `+${Math.round(a).toLocaleString()} / −${Math.round(r).toLocaleString()}`
}

/** mergeRate returns the merged-tasks ratio in [0, 1]. Returns 0 when no
 *  tasks are in scope so the KPI shows "0%" instead of NaN. */
export function mergeRate(merged: number | bigint, total: number | bigint): number {
  const m = typeof merged === 'bigint' ? Number(merged) : merged
  const t = typeof total === 'bigint' ? Number(total) : total
  if (t <= 0) return 0
  return m / t
}

/** codeCoverageNote builds the honest "based on N of M tasks" caption for
 *  the code KPIs, given the missing-code counter and the task total. Returns
 *  null when every task carried code data (or there are no tasks) so the
 *  caller can omit the caption entirely. */
export function codeCoverageNote(
  missingCode: number | bigint,
  tasksTotal: number | bigint
): string | null {
  const missing = typeof missingCode === 'bigint' ? Number(missingCode) : missingCode
  const total = typeof tasksTotal === 'bigint' ? Number(tasksTotal) : tasksTotal
  if (total <= 0 || missing <= 0) return null
  const covered = Math.max(0, total - missing)
  return `Code stats based on ${covered} of ${total} tasks`
}

/** ProjectChurn is the per-project shipped-code slice the mission-control
 *  card "shipped" line reads. Derived from a fleet rollup's top-projects rows
 *  so the home window needs only the single global insights fetch — no
 *  per-card recompute. */
export interface ProjectChurn {
  linesAdded: number
  linesRemoved: number
  netLines: number
  commits: number
  merges: number
}

/** churnByProjectId indexes a fleet rollup's top-projects rows by project id,
 *  keeping only projects that actually shipped code in the window (added,
 *  removed, or merges). Projects with no code signal are omitted entirely so
 *  the caller can degrade gracefully (no `+0 / −0` noise) rather than render a
 *  zeroed line. Returns an empty map for a null/absent rollup. */
export function churnByProjectId(
  insights: { topProjects: ReadonlyArray<GlobalInsights['topProjects'][number]> } | null
): Map<string, ProjectChurn> {
  const map = new Map<string, ProjectChurn>()
  if (!insights) return map
  for (const p of insights.topProjects) {
    const added = Number(p.linesAdded)
    const removed = Number(p.linesRemoved)
    const merges = Number(p.merges)
    if (added <= 0 && removed <= 0 && merges <= 0) continue
    map.set(p.projectId, {
      linesAdded: added,
      linesRemoved: removed,
      netLines: Number(p.netLines),
      commits: Number(p.commits),
      merges
    })
  }
  return map
}

/** formatShippedLine renders the compact per-card "shipped" summary
 *  ("+412 / −97 · 3 merges"). The merge clause is dropped when no task merged
 *  in the window so a churn-only project still reads cleanly. */
export function formatShippedLine(churn: ProjectChurn): string {
  const pair = formatLinesPair(churn.linesAdded, churn.linesRemoved)
  if (churn.merges > 0) {
    return `${pair} · ${churn.merges} merge${churn.merges === 1 ? '' : 's'}`
  }
  return pair
}

/** ChurnCell is the per-day sizing the code-churn-by-day chart reads —
 *  parallel to DayCell but stacking added (up) over removed so the bar height
 *  reflects total churn for the day. */
export interface ChurnCell {
  date: string
  addedHeight: number
  removedHeight: number
  added: number
  removed: number
}

/** churnBarHeights converts the lines-added/removed series into stacked
 *  pixel heights, scaled to the busiest day's total churn. Mirrors
 *  dayBarHeights so the churn chart can reuse the tasks-by-day layout. */
export function churnBarHeights(
  buckets: ReadonlyArray<{ date: string; linesAdded: number | bigint; linesRemoved: number | bigint }>,
  maxHeightPx: number
): ChurnCell[] {
  if (buckets.length === 0) return []
  const rows = buckets.map((b) => ({
    date: b.date,
    added: typeof b.linesAdded === 'bigint' ? Number(b.linesAdded) : b.linesAdded,
    removed: typeof b.linesRemoved === 'bigint' ? Number(b.linesRemoved) : b.linesRemoved
  }))
  const peak = rows.reduce((m, r) => Math.max(m, r.added + r.removed), 0)
  if (peak <= 0) {
    return rows.map((r) => ({ date: r.date, addedHeight: 0, removedHeight: 0, added: 0, removed: 0 }))
  }
  return rows.map((r) => ({
    date: r.date,
    addedHeight: Math.round((r.added / peak) * maxHeightPx),
    removedHeight: Math.round((r.removed / peak) * maxHeightPx),
    added: r.added,
    removed: r.removed
  }))
}

/** hasCodeData reports whether a rollup carries any shipped-code signal, so
 *  the UI can hide the code section (rather than render a row of zeros) for
 *  windows made entirely of pre-v8.0 tasks. */
export function hasCodeData(
  insights: { totalCommits: number | bigint; totalLinesAdded: number | bigint; totalLinesRemoved: number | bigint; tasksMerged: number | bigint }
): boolean {
  const n = (v: number | bigint) => (typeof v === 'bigint' ? Number(v) : v)
  return (
    n(insights.totalCommits) > 0 ||
    n(insights.totalLinesAdded) > 0 ||
    n(insights.totalLinesRemoved) > 0 ||
    n(insights.tasksMerged) > 0
  )
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

/** ChartSummary is the always-visible headline for a bar chart (v9.2). The
 *  charts previously carried no numbers at all — only a native `title` you
 *  had to discover by hovering — so a glance told you the *shape* of the
 *  window but never its magnitude. Peak + total fix that without adding an
 *  axis. Code fields are zero for windows made of pre-v8.0 tasks. */
export interface ChartSummary {
  peak: number
  total: number
  succeeded: number
  failed: number
  linesAdded: number
  linesRemoved: number
  days: number
  /** Days with at least one completed task — the denominator for "N of M
   *  days active", which is more honest than dividing by the raw window. */
  activeDays: number
}

export function dayChartSummary(
  buckets: ReadonlyArray<{
    date: string
    count: number | bigint
    succeeded: number | bigint
    failed: number | bigint
    linesAdded?: number | bigint
    linesRemoved?: number | bigint
  }>
): ChartSummary {
  const n = (v: number | bigint | undefined) =>
    v === undefined ? 0 : typeof v === 'bigint' ? Number(v) : v
  const summary: ChartSummary = {
    peak: 0,
    total: 0,
    succeeded: 0,
    failed: 0,
    linesAdded: 0,
    linesRemoved: 0,
    days: buckets.length,
    activeDays: 0
  }
  for (const b of buckets) {
    const count = n(b.count)
    summary.total += count
    summary.succeeded += n(b.succeeded)
    summary.failed += n(b.failed)
    summary.linesAdded += n(b.linesAdded)
    summary.linesRemoved += n(b.linesRemoved)
    if (count > summary.peak) summary.peak = count
    if (count > 0) summary.activeDays++
  }
  return summary
}

/** formatBucketDate renders a `YYYY-MM-DD` bucket key as "Fri 24 Jul" for
 *  the hover card. The key is parsed as a *local* calendar date — passing it
 *  to `new Date(string)` would parse it as UTC midnight and shift the
 *  weekday backwards for anyone west of Greenwich. Unparseable keys are
 *  returned verbatim rather than rendering "Invalid Date". */
export function formatBucketDate(date: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return date
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  if (Number.isNaN(d.getTime())) return date
  const weekday = d.toLocaleDateString(undefined, { weekday: 'short' })
  const day = d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
  return `${weekday} ${day}`
}

/** TooltipAnchor positions a hover card over a bar without letting it spill
 *  outside the card. Bars near either edge flip their alignment instead of
 *  centring, which would clip the card against the panel border. */
export interface TooltipAnchor {
  leftPercent: number
  transform: string
}

export function tooltipAnchor(index: number, count: number): TooltipAnchor {
  if (count <= 0) return { leftPercent: 0, transform: 'translateX(0)' }
  // Centre of the index-th slot, as a percentage of the track width.
  const leftPercent = ((index + 0.5) / count) * 100
  if (leftPercent < 20) return { leftPercent, transform: 'translateX(0)' }
  if (leftPercent > 80) return { leftPercent, transform: 'translateX(-100%)' }
  return { leftPercent, transform: 'translateX(-50%)' }
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
