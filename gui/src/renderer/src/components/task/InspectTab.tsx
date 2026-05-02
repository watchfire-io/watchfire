// v6.0 Ember inline diff viewer.
//
// Tab on the per-task detail panel that renders the structured diff the
// daemon produces from `InsightsService.GetTaskDiff`. Layout: file list
// sidebar on the left (with +/- counts per file), diff body on the right.
// Side-by-side at >960px panel width, unified below that.
//
// We render the structured `FileDiffSet` directly instead of pulling in
// `react-diff-view` — the wire format already gives us per-line kind
// records, so a minimal custom renderer keeps the dep tree light.
//
// Keyboard:
//   j / k        next / prev file
//   /            filter files
//   r            refresh diff (bypasses the renderer cache)
//   Escape       close filter
import { useEffect, useMemo, useRef, useState } from 'react'
import {
  FilePlus,
  FileMinus,
  FilePen,
  FileSymlink,
  RefreshCw,
  Search,
  X
} from 'lucide-react'
import {
  DiffLine_Kind,
  FileDiff_Status,
  type DiffLine,
  type FileDiff,
  type FileDiffSet,
  type Hunk
} from '../../generated/watchfire_pb'
import { selectDiff, useDiffStore } from '../../stores/diff-store'
import { cn } from '../../lib/utils'

interface Props {
  projectId: string
  taskNumber: number
}

const SIDE_BY_SIDE_BREAKPOINT_PX = 960

export function InspectTab({ projectId, taskNumber }: Props) {
  const fetch = useDiffStore((s) => s.fetch)
  const entry = useDiffStore(selectDiff(projectId, taskNumber))

  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [filterOpen, setFilterOpen] = useState(false)
  const [containerWidth, setContainerWidth] = useState(0)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const filterInputRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    void fetch(projectId, taskNumber)
  }, [fetch, projectId, taskNumber])

  // Track container width so we can swap unified ↔ side-by-side responsively.
  useEffect(() => {
    if (!containerRef.current) return
    const observer = new ResizeObserver((entries) => {
      for (const e of entries) setContainerWidth(e.contentRect.width)
    })
    observer.observe(containerRef.current)
    return () => observer.disconnect()
  }, [])

  const data = entry?.data ?? null
  const loading = entry?.loading ?? !data
  const error = entry?.error ?? null

  const filteredFiles = useMemo(() => {
    if (!data) return []
    if (!filter.trim()) return data.files
    const needle = filter.toLowerCase()
    return data.files.filter((f) => f.path.toLowerCase().includes(needle))
  }, [data, filter])

  // Default-select the first file as soon as data lands.
  useEffect(() => {
    if (!data || data.files.length === 0) return
    if (!selectedPath || !data.files.some((f) => f.path === selectedPath)) {
      setSelectedPath(data.files[0].path)
    }
  }, [data, selectedPath])

  const selectedFile = useMemo(() => {
    if (!data) return null
    return data.files.find((f) => f.path === selectedPath) ?? null
  }, [data, selectedPath])

  // Keyboard nav. Filter input intercepts j/k while focused so users can
  // type inside it without skipping files.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (filterInputRef.current === document.activeElement) {
        if (e.key === 'Escape') {
          setFilterOpen(false)
          setFilter('')
          filterInputRef.current?.blur()
        }
        return
      }

      const target = e.target as HTMLElement | null
      const inForm =
        !!target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.isContentEditable)
      if (inForm) return

      if (e.key === 'j') {
        moveSelection(filteredFiles, selectedPath, 1, setSelectedPath)
      } else if (e.key === 'k') {
        moveSelection(filteredFiles, selectedPath, -1, setSelectedPath)
      } else if (e.key === '/') {
        e.preventDefault()
        setFilterOpen(true)
        // Focus on next tick so the input is mounted.
        requestAnimationFrame(() => filterInputRef.current?.focus())
      } else if (e.key === 'r') {
        void fetch(projectId, taskNumber, true)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [fetch, filteredFiles, projectId, selectedPath, taskNumber])

  const useSideBySide = containerWidth >= SIDE_BY_SIDE_BREAKPOINT_PX

  return (
    <div ref={containerRef} className="flex-1 flex flex-col overflow-hidden">
      <Header
        data={data}
        loading={loading}
        filterOpen={filterOpen}
        filter={filter}
        onFilterChange={setFilter}
        onToggleFilter={() => {
          if (filterOpen) {
            setFilter('')
            setFilterOpen(false)
          } else {
            setFilterOpen(true)
            requestAnimationFrame(() => filterInputRef.current?.focus())
          }
        }}
        onRefresh={() => void fetch(projectId, taskNumber, true)}
        filterInputRef={filterInputRef}
      />

      {error ? (
        <ErrorState message={error} />
      ) : loading && !data ? (
        <LoadingSkeleton />
      ) : !data || data.files.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="flex-1 flex overflow-hidden min-h-0">
          <FileSidebar
            files={filteredFiles}
            selectedPath={selectedPath}
            onSelect={setSelectedPath}
          />
          <div className="flex-1 flex flex-col overflow-hidden">
            {selectedFile && (
              <DiffBody file={selectedFile} sideBySide={useSideBySide} />
            )}
            {data.truncated && (
              <div className="px-4 py-2 text-[11px] text-[var(--wf-warning)] border-t border-[var(--wf-border)]">
                Diff truncated &mdash; view the full change in <code>git</code>.
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function moveSelection(
  files: FileDiff[],
  current: string | null,
  delta: number,
  set: (path: string) => void
) {
  if (files.length === 0) return
  const idx = files.findIndex((f) => f.path === current)
  const next = Math.max(0, Math.min(files.length - 1, (idx === -1 ? 0 : idx) + delta))
  set(files[next].path)
}

interface HeaderProps {
  data: FileDiffSet | null
  loading: boolean
  filterOpen: boolean
  filter: string
  onFilterChange: (next: string) => void
  onToggleFilter: () => void
  onRefresh: () => void
  filterInputRef: React.RefObject<HTMLInputElement | null>
}

function Header({
  data,
  loading,
  filterOpen,
  filter,
  onFilterChange,
  onToggleFilter,
  onRefresh,
  filterInputRef
}: HeaderProps) {
  const fileCount = data?.files.length ?? 0
  return (
    <div className="px-4 py-2 border-b border-[var(--wf-border)] flex items-center gap-3">
      <div className="text-xs text-[var(--wf-text-muted)] flex items-center gap-3">
        <span>{fileCount} file{fileCount === 1 ? '' : 's'}</span>
        {data && (
          <>
            <span className="text-[var(--wf-success)] tabular-nums">+{data.totalAdditions}</span>
            <span className="text-[var(--wf-warning)] tabular-nums">&minus;{data.totalDeletions}</span>
          </>
        )}
      </div>
      <div className="flex-1" />
      {filterOpen ? (
        <div className="flex items-center gap-1.5 px-2 py-1 rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] border border-[var(--wf-border)]">
          <Search size={12} className="text-[var(--wf-text-muted)]" />
          <input
            ref={filterInputRef}
            value={filter}
            onChange={(e) => onFilterChange(e.target.value)}
            placeholder="Filter files…"
            className="bg-transparent text-xs text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] outline-none w-40"
          />
          <button
            onClick={onToggleFilter}
            className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]"
            aria-label="Close filter"
          >
            <X size={12} />
          </button>
        </div>
      ) : (
        <button
          onClick={onToggleFilter}
          className="px-2 py-1 text-xs text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors flex items-center gap-1.5"
          title="Filter files (/)"
        >
          <Search size={12} /> Filter
        </button>
      )}
      <button
        onClick={onRefresh}
        disabled={loading}
        className={cn(
          'px-2 py-1 text-xs transition-colors flex items-center gap-1.5',
          loading
            ? 'text-[var(--wf-text-muted)]'
            : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]'
        )}
        title="Refresh diff (r)"
      >
        <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
        Refresh
      </button>
    </div>
  )
}

function FileSidebar({
  files,
  selectedPath,
  onSelect
}: {
  files: FileDiff[]
  selectedPath: string | null
  onSelect: (path: string) => void
}) {
  if (files.length === 0) {
    return (
      <div className="w-64 shrink-0 border-r border-[var(--wf-border)] p-3 text-xs text-[var(--wf-text-muted)]">
        No matches.
      </div>
    )
  }
  return (
    <div className="w-64 shrink-0 overflow-y-auto border-r border-[var(--wf-border)]">
      <ul role="list" className="py-1">
        {files.map((f) => (
          <li key={f.path}>
            <button
              type="button"
              onClick={() => onSelect(f.path)}
              className={cn(
                'w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 transition-colors',
                selectedPath === f.path
                  ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)]'
                  : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)]'
              )}
            >
              <StatusIcon status={f.status} />
              <span className="flex-1 truncate font-mono" title={f.path}>
                {f.path}
              </span>
              <FileLineCounts file={f} />
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

function StatusIcon({ status }: { status: FileDiff_Status }) {
  const props = { size: 13, className: 'shrink-0' }
  switch (status) {
    case FileDiff_Status.ADDED:
      return <FilePlus {...props} className="shrink-0 text-[var(--wf-success)]" />
    case FileDiff_Status.DELETED:
      return <FileMinus {...props} className="shrink-0 text-[var(--wf-warning)]" />
    case FileDiff_Status.RENAMED:
      return <FileSymlink {...props} className="shrink-0 text-[var(--wf-text-muted)]" />
    default:
      return <FilePen {...props} className="shrink-0 text-[var(--wf-text-muted)]" />
  }
}

function FileLineCounts({ file }: { file: FileDiff }) {
  let adds = 0
  let dels = 0
  for (const h of file.hunks) {
    for (const l of h.lines) {
      if (l.kind === DiffLine_Kind.ADD) adds++
      else if (l.kind === DiffLine_Kind.DEL) dels++
    }
  }
  return (
    <span className="shrink-0 text-[10px] tabular-nums flex gap-1">
      {adds > 0 && <span className="text-[var(--wf-success)]">+{adds}</span>}
      {dels > 0 && <span className="text-[var(--wf-warning)]">&minus;{dels}</span>}
    </span>
  )
}

function DiffBody({ file, sideBySide }: { file: FileDiff; sideBySide: boolean }) {
  const isBinary = file.hunks.length === 1 && file.hunks[0].lines.length === 0

  return (
    <div className="flex-1 overflow-auto bg-[var(--wf-bg-primary)]">
      <div className="px-3 py-2 border-b border-[var(--wf-border)] flex items-center gap-2">
        <span className="text-xs font-mono text-[var(--wf-text-primary)] truncate" title={file.path}>
          {file.path}
        </span>
        {file.oldPath && file.oldPath !== file.path && (
          <span className="text-[10px] text-[var(--wf-text-muted)]">renamed from {file.oldPath}</span>
        )}
      </div>
      {isBinary ? (
        <div className="px-3 py-4 text-xs text-[var(--wf-text-muted)]">
          {file.hunks[0]?.header || 'Binary file changed'}
        </div>
      ) : file.hunks.length === 0 ? (
        <div className="px-3 py-4 text-xs text-[var(--wf-text-muted)]">No textual changes.</div>
      ) : sideBySide ? (
        <SideBySideRenderer file={file} />
      ) : (
        <UnifiedRenderer file={file} />
      )}
    </div>
  )
}

function UnifiedRenderer({ file }: { file: FileDiff }) {
  return (
    <table className="w-full font-mono text-[12px] border-collapse">
      <tbody>
        {file.hunks.map((h, hi) => (
          <UnifiedHunk key={hi} hunk={h} />
        ))}
      </tbody>
    </table>
  )
}

function UnifiedHunk({ hunk }: { hunk: Hunk }) {
  let oldLine = hunk.oldStart
  let newLine = hunk.newStart
  return (
    <>
      <tr className="bg-[var(--wf-bg-elevated)] text-[var(--wf-text-muted)]">
        <td className="px-2 py-0.5 select-none w-12 text-right" />
        <td className="px-2 py-0.5 select-none w-12 text-right" />
        <td className="px-2 py-0.5 text-[11px]">
          {`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`}
          {hunk.header && <span className="ml-2 text-[var(--wf-text-muted)]">{hunk.header}</span>}
        </td>
      </tr>
      {hunk.lines.map((line, li) => {
        const row = renderUnifiedLine(line, oldLine, newLine, li)
        if (line.kind === DiffLine_Kind.ADD) newLine++
        else if (line.kind === DiffLine_Kind.DEL) oldLine++
        else {
          oldLine++
          newLine++
        }
        return row
      })}
    </>
  )
}

function renderUnifiedLine(line: DiffLine, oldLine: number, newLine: number, key: number) {
  const cls =
    line.kind === DiffLine_Kind.ADD
      ? 'bg-green-500/10 text-[var(--wf-text-primary)]'
      : line.kind === DiffLine_Kind.DEL
        ? 'bg-red-500/10 text-[var(--wf-text-primary)]'
        : 'text-[var(--wf-text-secondary)]'
  const prefix =
    line.kind === DiffLine_Kind.ADD ? '+' : line.kind === DiffLine_Kind.DEL ? '-' : ' '
  return (
    <tr key={key} className={cls}>
      <td className="px-2 py-0 select-none w-12 text-right text-[var(--wf-text-muted)] tabular-nums">
        {line.kind === DiffLine_Kind.ADD ? '' : oldLine}
      </td>
      <td className="px-2 py-0 select-none w-12 text-right text-[var(--wf-text-muted)] tabular-nums">
        {line.kind === DiffLine_Kind.DEL ? '' : newLine}
      </td>
      <td className="px-2 py-0 whitespace-pre">
        <span
          className={cn(
            'inline-block w-3 select-none',
            line.kind === DiffLine_Kind.ADD
              ? 'text-[var(--wf-success)]'
              : line.kind === DiffLine_Kind.DEL
                ? 'text-[var(--wf-warning)]'
                : 'text-[var(--wf-text-muted)]'
          )}
        >
          {prefix}
        </span>
        {line.text}
      </td>
    </tr>
  )
}

function SideBySideRenderer({ file }: { file: FileDiff }) {
  return (
    <div className="font-mono text-[12px]">
      {file.hunks.map((h, hi) => (
        <SideBySideHunk key={hi} hunk={h} />
      ))}
    </div>
  )
}

interface PairedRow {
  oldNum: number | null
  newNum: number | null
  oldKind: DiffLine_Kind | null
  newKind: DiffLine_Kind | null
  oldText: string
  newText: string
}

// pairLines walks one hunk's lines and pairs deletions with additions on the
// same row when possible, leaving a blank cell where one side is missing.
// Conventional rule: a run of `-` followed by a run of `+` is paired by
// position; surplus on either side stretches into orphan rows.
function pairLines(hunk: Hunk): PairedRow[] {
  const rows: PairedRow[] = []
  let oldNum = hunk.oldStart
  let newNum = hunk.newStart
  let i = 0
  while (i < hunk.lines.length) {
    const line = hunk.lines[i]
    if (line.kind === DiffLine_Kind.CONTEXT) {
      rows.push({
        oldNum,
        newNum,
        oldKind: DiffLine_Kind.CONTEXT,
        newKind: DiffLine_Kind.CONTEXT,
        oldText: line.text,
        newText: line.text
      })
      oldNum++
      newNum++
      i++
      continue
    }
    // Collect a run of deletions followed by additions.
    const dels: DiffLine[] = []
    while (i < hunk.lines.length && hunk.lines[i].kind === DiffLine_Kind.DEL) {
      dels.push(hunk.lines[i])
      i++
    }
    const adds: DiffLine[] = []
    while (i < hunk.lines.length && hunk.lines[i].kind === DiffLine_Kind.ADD) {
      adds.push(hunk.lines[i])
      i++
    }
    const max = Math.max(dels.length, adds.length)
    for (let k = 0; k < max; k++) {
      const d = dels[k]
      const a = adds[k]
      rows.push({
        oldNum: d ? oldNum : null,
        newNum: a ? newNum : null,
        oldKind: d ? DiffLine_Kind.DEL : null,
        newKind: a ? DiffLine_Kind.ADD : null,
        oldText: d?.text ?? '',
        newText: a?.text ?? ''
      })
      if (d) oldNum++
      if (a) newNum++
    }
  }
  return rows
}

function SideBySideHunk({ hunk }: { hunk: Hunk }) {
  const rows = useMemo(() => pairLines(hunk), [hunk])
  return (
    <div className="border-b border-[var(--wf-border)]">
      <div className="px-3 py-0.5 bg-[var(--wf-bg-elevated)] text-[11px] text-[var(--wf-text-muted)]">
        {`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`}
        {hunk.header && <span className="ml-2">{hunk.header}</span>}
      </div>
      <table className="w-full border-collapse">
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              <td
                className={cn(
                  'px-2 select-none w-10 text-right text-[var(--wf-text-muted)] tabular-nums',
                  r.oldKind === DiffLine_Kind.DEL && 'bg-red-500/10'
                )}
              >
                {r.oldNum ?? ''}
              </td>
              <td
                className={cn(
                  'px-2 whitespace-pre w-1/2',
                  r.oldKind === DiffLine_Kind.DEL && 'bg-red-500/10'
                )}
              >
                {r.oldText}
              </td>
              <td
                className={cn(
                  'px-2 select-none w-10 text-right text-[var(--wf-text-muted)] tabular-nums',
                  r.newKind === DiffLine_Kind.ADD && 'bg-green-500/10'
                )}
              >
                {r.newNum ?? ''}
              </td>
              <td
                className={cn(
                  'px-2 whitespace-pre w-1/2',
                  r.newKind === DiffLine_Kind.ADD && 'bg-green-500/10'
                )}
              >
                {r.newText}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function EmptyState() {
  return (
    <div className="flex-1 flex items-center justify-center px-6">
      <p className="text-sm text-[var(--wf-text-muted)] text-center">
        No changes &mdash; this task didn&apos;t touch any files.
      </p>
    </div>
  )
}

function LoadingSkeleton() {
  return (
    <div className="flex-1 flex overflow-hidden">
      <div className="w-64 shrink-0 border-r border-[var(--wf-border)] p-3 space-y-2 animate-pulse">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="h-4 rounded bg-[var(--wf-bg-elevated)]" />
        ))}
      </div>
      <div className="flex-1 p-4 space-y-2 animate-pulse">
        {Array.from({ length: 12 }).map((_, i) => (
          <div key={i} className="h-3 rounded bg-[var(--wf-bg-elevated)]" />
        ))}
      </div>
    </div>
  )
}

function ErrorState({ message }: { message: string }) {
  return (
    <div className="flex-1 flex items-center justify-center px-6">
      <p className="text-sm text-[var(--wf-warning)] text-center">
        Couldn&apos;t load diff: {message}
      </p>
    </div>
  )
}
