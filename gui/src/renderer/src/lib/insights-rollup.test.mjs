// Snapshot-style tests for the v6.0 Ember dashboard rollup card.
//
// React rendering isn't booted here (matches the useExportReport test
// pattern) — instead we exercise the pure helpers + classifier that the
// component layout reads, asserting the contract of empty / partial /
// full render states.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Mirror of lib/insights-rollup ---------------------------------------

const INSIGHTS_WINDOWS = ['7d', '30d', '90d', 'all']

function windowToRange(window, now = new Date('2026-05-02T12:00:00Z')) {
  if (window === 'all') return {}
  const days = window === '7d' ? 7 : window === '90d' ? 90 : 30
  const start = new Date(now)
  start.setDate(start.getDate() - days)
  return { start, end: now }
}

function formatDuration(ms) {
  if (!Number.isFinite(ms) || ms <= 0) return '0m'
  const totalMin = Math.floor(ms / 60_000)
  if (totalMin < 60) return `${totalMin}m`
  const hr = Math.floor(totalMin / 60)
  const minPad = String(totalMin % 60).padStart(2, '0')
  return `${hr}h ${minPad}m`
}

function formatCost(cost, missing) {
  const usd = `$${cost.toFixed(2)}`
  if (missing > 0) return `${usd} (${missing} part)`
  return usd
}

function formatPercent(rate) {
  if (!Number.isFinite(rate) || rate <= 0) return '0%'
  return `${Math.round(rate * 100)}%`
}

function successRate(insights) {
  const total = Number(insights.tasksTotal)
  if (total <= 0) return 0
  return Number(insights.tasksSucceeded) / total
}

function dayBarHeights(buckets, maxHeightPx) {
  if (buckets.length === 0) return []
  const peak = buckets.reduce((m, b) => Math.max(m, b.count), 0)
  if (peak <= 0) return buckets.map((b) => ({ date: b.date, succeededHeight: 0, failedHeight: 0, total: 0 }))
  return buckets.map((b) => ({
    date: b.date,
    succeededHeight: Math.round((b.succeeded / peak) * maxHeightPx),
    failedHeight: Math.round((b.failed / peak) * maxHeightPx),
    total: b.count
  }))
}

function agentSegmentWidths(agents) {
  const filtered = agents.filter((a) => a.count > 0)
  const total = filtered.reduce((sum, a) => sum + a.count, 0)
  if (total <= 0) return []
  let runningPct = 0
  const out = []
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

function classifyRollup(insights, loading) {
  if (loading && !insights) return 'loading'
  if (!insights || insights.tasksTotal === 0) return 'empty'
  if (insights.tasksMissingCost > 0 || insights.topProjects.length === 0) return 'partial'
  return 'full'
}

// --- Snapshot fixtures ----------------------------------------------------

const EMPTY_INSIGHTS = {
  tasksTotal: 0,
  tasksSucceeded: 0,
  tasksFailed: 0,
  tasksByDay: [],
  topProjects: [],
  agentBreakdown: [],
  totalDurationMs: 0n,
  totalCostUsd: 0,
  tasksMissingCost: 0
}

const PARTIAL_INSIGHTS = {
  tasksTotal: 8,
  tasksSucceeded: 6,
  tasksFailed: 2,
  tasksByDay: [
    { date: '2026-04-30', count: 3, succeeded: 3, failed: 0 },
    { date: '2026-05-01', count: 5, succeeded: 3, failed: 2 }
  ],
  topProjects: [], // empty top-projects → partial render
  agentBreakdown: [{ agent: 'claude-code', count: 8, successRate: 0.75 }],
  totalDurationMs: 1_800_000n,
  totalCostUsd: 0,
  tasksMissingCost: 8 // every task missing cost → partial
}

const FULL_INSIGHTS = {
  tasksTotal: 412,
  tasksSucceeded: 322,
  tasksFailed: 90,
  tasksByDay: Array.from({ length: 14 }, (_, i) => ({
    date: `2026-04-${String(20 + i).padStart(2, '0')}`,
    count: i + 5,
    succeeded: i + 3,
    failed: 2
  })),
  topProjects: [
    { projectId: 'p1', projectName: 'watchfire', projectColor: '#ef4444', count: 142, successRate: 0.81 },
    { projectId: 'p2', projectName: 'scratch', projectColor: '#22c55e', count: 86, successRate: 0.7 },
    { projectId: 'p3', projectName: 'blog', projectColor: '#3b82f6', count: 64, successRate: 0.78 }
  ],
  agentBreakdown: [
    { agent: 'claude-code', count: 198, successRate: 0.78 },
    { agent: 'codex', count: 91, successRate: 0.82 },
    { agent: 'opencode', count: 64, successRate: 0.75 },
    { agent: 'gemini', count: 59, successRate: 0.71 }
  ],
  totalDurationMs: 71n * 3_600_000n + 4n * 60_000n,
  totalCostUsd: 23.71,
  tasksMissingCost: 0
}

// --- Tests ----------------------------------------------------------------

test('classifyRollup: empty when no projects have completed tasks', () => {
  assert.equal(classifyRollup(EMPTY_INSIGHTS, false), 'empty')
})

test('classifyRollup: empty when insights is null', () => {
  assert.equal(classifyRollup(null, false), 'empty')
})

test('classifyRollup: partial when missing cost data', () => {
  assert.equal(classifyRollup(PARTIAL_INSIGHTS, false), 'partial')
})

test('classifyRollup: full when every dimension present', () => {
  assert.equal(classifyRollup(FULL_INSIGHTS, false), 'full')
})

test('classifyRollup: loading shown while initial fetch is pending', () => {
  assert.equal(classifyRollup(null, true), 'loading')
})

test('full state rollup KPIs render expected strings', () => {
  assert.equal(formatPercent(successRate(FULL_INSIGHTS)), '78%')
  assert.equal(formatDuration(Number(FULL_INSIGHTS.totalDurationMs)), '71h 04m')
  assert.equal(formatCost(FULL_INSIGHTS.totalCostUsd, FULL_INSIGHTS.tasksMissingCost), '$23.71')
})

test('partial state surfaces the missing-cost caveat', () => {
  assert.equal(formatCost(PARTIAL_INSIGHTS.totalCostUsd, PARTIAL_INSIGHTS.tasksMissingCost), '$0.00 (8 part)')
})

test('empty state KPIs collapse to zero rather than NaN', () => {
  assert.equal(formatPercent(successRate(EMPTY_INSIGHTS)), '0%')
  assert.equal(formatDuration(Number(EMPTY_INSIGHTS.totalDurationMs)), '0m')
})

test('day bars scale to peak rather than absolute counts', () => {
  const cells = dayBarHeights(FULL_INSIGHTS.tasksByDay, 64)
  const heights = cells.map((c) => c.succeededHeight + c.failedHeight)
  assert.ok(Math.max(...heights) <= 64, 'no cell taller than the bar')
  // The largest day should be at the cap (within rounding).
  assert.ok(Math.max(...heights) >= 60, 'tallest day reaches near the cap')
})

test('agent segment widths sum to 100 (rounding absorbed in last)', () => {
  const segments = agentSegmentWidths(FULL_INSIGHTS.agentBreakdown)
  const total = segments.reduce((s, x) => s + x.widthPercent, 0)
  assert.ok(Math.abs(total - 100) < 1e-6, `widths must sum to 100, got ${total}`)
})

test('agent segment widths skip zero-count rows', () => {
  const segments = agentSegmentWidths([
    { agent: 'claude-code', count: 5 },
    { agent: 'codex', count: 0 }
  ])
  assert.equal(segments.length, 1)
  assert.equal(segments[0].agent, 'claude-code')
  assert.equal(segments[0].widthPercent, 100)
})

test('window selector preset boundaries: 7d / 30d / 90d / all', () => {
  assert.deepEqual(INSIGHTS_WINDOWS, ['7d', '30d', '90d', 'all'])
  const r7 = windowToRange('7d')
  const r30 = windowToRange('30d')
  const r90 = windowToRange('90d')
  const rAll = windowToRange('all')
  assert.ok(r7.start && r30.start && r90.start)
  assert.ok(r7.start.getTime() > r30.start.getTime())
  assert.ok(r30.start.getTime() > r90.start.getTime())
  assert.deepEqual(rAll, {})
})

// --- v8.0 Inferno code-output helpers (mirror of lib/insights-rollup) ------

function formatInt(value) {
  const n = typeof value === 'bigint' ? Number(value) : value
  if (!Number.isFinite(n)) return '0'
  return Math.round(n).toLocaleString()
}

function formatSignedLines(value) {
  const n = typeof value === 'bigint' ? Number(value) : value
  if (!Number.isFinite(n) || n === 0) return '0'
  if (n > 0) return `+${Math.round(n).toLocaleString()}`
  return `−${Math.round(Math.abs(n)).toLocaleString()}`
}

function formatLinesPair(added, removed) {
  const a = typeof added === 'bigint' ? Number(added) : added
  const r = typeof removed === 'bigint' ? Number(removed) : removed
  return `+${Math.round(a).toLocaleString()} / −${Math.round(r).toLocaleString()}`
}

function mergeRate(merged, total) {
  const m = typeof merged === 'bigint' ? Number(merged) : merged
  const t = typeof total === 'bigint' ? Number(total) : total
  if (t <= 0) return 0
  return m / t
}

function codeCoverageNote(missingCode, tasksTotal) {
  const missing = typeof missingCode === 'bigint' ? Number(missingCode) : missingCode
  const total = typeof tasksTotal === 'bigint' ? Number(tasksTotal) : tasksTotal
  if (total <= 0 || missing <= 0) return null
  const covered = Math.max(0, total - missing)
  return `Code stats based on ${covered} of ${total} tasks`
}

function churnBarHeights(buckets, maxHeightPx) {
  if (buckets.length === 0) return []
  const rows = buckets.map((b) => ({
    date: b.date,
    added: typeof b.linesAdded === 'bigint' ? Number(b.linesAdded) : b.linesAdded,
    removed: typeof b.linesRemoved === 'bigint' ? Number(b.linesRemoved) : b.linesRemoved
  }))
  const peak = rows.reduce((m, r) => Math.max(m, r.added + r.removed), 0)
  if (peak <= 0) return rows.map((r) => ({ date: r.date, addedHeight: 0, removedHeight: 0, added: 0, removed: 0 }))
  return rows.map((r) => ({
    date: r.date,
    addedHeight: Math.round((r.added / peak) * maxHeightPx),
    removedHeight: Math.round((r.removed / peak) * maxHeightPx),
    added: r.added,
    removed: r.removed
  }))
}

function hasCodeData(insights) {
  const n = (v) => (typeof v === 'bigint' ? Number(v) : v)
  return (
    n(insights.totalCommits) > 0 ||
    n(insights.totalLinesAdded) > 0 ||
    n(insights.totalLinesRemoved) > 0 ||
    n(insights.tasksMerged) > 0
  )
}

test('formatInt groups thousands and tolerates bigint', () => {
  assert.equal(formatInt(1234), '1,234')
  assert.equal(formatInt(0), '0')
  assert.equal(formatInt(42n), '42')
})

test('formatSignedLines uses real sign glyphs; zero is bare', () => {
  assert.equal(formatSignedLines(412), '+412')
  assert.equal(formatSignedLines(-97), '−97')
  assert.equal(formatSignedLines(0), '0')
})

test('formatLinesPair renders the +added / −removed churn pair', () => {
  assert.equal(formatLinesPair(412, 97), '+412 / −97')
})

test('mergeRate is a ratio in [0,1], 0 when no tasks', () => {
  assert.equal(mergeRate(3, 4), 0.75)
  assert.equal(mergeRate(0, 0), 0)
  assert.equal(formatPercent(mergeRate(3, 4)), '75%')
})

test('codeCoverageNote only fires when some tasks lack code data', () => {
  assert.equal(codeCoverageNote(0, 10), null)
  assert.equal(codeCoverageNote(3, 0), null)
  assert.equal(codeCoverageNote(3, 10), 'Code stats based on 7 of 10 tasks')
})

test('churn bars scale to the busiest day total churn', () => {
  const buckets = [
    { date: 'd1', linesAdded: 10, linesRemoved: 5 },
    { date: 'd2', linesAdded: 80, linesRemoved: 20 } // peak = 100
  ]
  const cells = churnBarHeights(buckets, 64)
  const totals = cells.map((c) => c.addedHeight + c.removedHeight)
  assert.ok(Math.max(...totals) <= 64)
  assert.ok(Math.max(...totals) >= 60, 'busiest day reaches near the cap')
})

test('churn bars collapse to zero height with no churn', () => {
  const cells = churnBarHeights([{ date: 'd1', linesAdded: 0, linesRemoved: 0 }], 64)
  assert.deepEqual(cells, [{ date: 'd1', addedHeight: 0, removedHeight: 0, added: 0, removed: 0 }])
})

test('hasCodeData gates the code section honestly', () => {
  assert.equal(hasCodeData({ totalCommits: 0, totalLinesAdded: 0, totalLinesRemoved: 0, tasksMerged: 0 }), false)
  assert.equal(hasCodeData({ totalCommits: 0, totalLinesAdded: 0, totalLinesRemoved: 0, tasksMerged: 2 }), true)
  assert.equal(hasCodeData({ totalCommits: 0, totalLinesAdded: 5, totalLinesRemoved: 0, tasksMerged: 0 }), true)
})

// --- v8.0 Inferno per-card "shipped" line (mirror of lib/insights-rollup) ---

function churnByProjectId(insights) {
  const map = new Map()
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

function formatShippedLine(churn) {
  const pair = formatLinesPair(churn.linesAdded, churn.linesRemoved)
  if (churn.merges > 0) {
    return `${pair} · ${churn.merges} merge${churn.merges === 1 ? '' : 's'}`
  }
  return pair
}

test('churnByProjectId indexes only projects that shipped code', () => {
  const map = churnByProjectId({
    topProjects: [
      { projectId: 'a', linesAdded: 412, linesRemoved: 97, netLines: 315, commits: 8, merges: 3 },
      { projectId: 'b', linesAdded: 0, linesRemoved: 0, netLines: 0, commits: 0, merges: 0 },
      { projectId: 'c', linesAdded: 0, linesRemoved: 0, netLines: 0, commits: 0, merges: 2 }
    ]
  })
  assert.equal(map.size, 2, 'zero-everywhere project b is omitted')
  assert.deepEqual(map.get('a'), { linesAdded: 412, linesRemoved: 97, netLines: 315, commits: 8, merges: 3 })
  assert.ok(map.has('c'), 'a merge with no line churn still counts as shipped')
  assert.equal(map.has('b'), false)
})

test('churnByProjectId returns an empty map for a null rollup', () => {
  assert.equal(churnByProjectId(null).size, 0)
})

test('formatShippedLine renders churn with pluralized merge count', () => {
  assert.equal(
    formatShippedLine({ linesAdded: 412, linesRemoved: 97, netLines: 315, commits: 8, merges: 3 }),
    '+412 / −97 · 3 merges'
  )
  assert.equal(
    formatShippedLine({ linesAdded: 10, linesRemoved: 2, netLines: 8, commits: 1, merges: 1 }),
    '+10 / −2 · 1 merge'
  )
})

test('formatShippedLine drops the merge clause when nothing merged', () => {
  assert.equal(
    formatShippedLine({ linesAdded: 30, linesRemoved: 4, netLines: 26, commits: 2, merges: 0 }),
    '+30 / −4'
  )
})
