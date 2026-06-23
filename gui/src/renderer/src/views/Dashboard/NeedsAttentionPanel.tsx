import { AlertTriangle, KeyRound, Timer, CheckCircle2, ChevronRight, ExternalLink } from 'lucide-react'
import { useNeedsAttention } from '../../hooks/useNeedsAttention'
import type { AttentionEntry, AttentionKind } from '../../lib/needs-attention'

// Per-kind icon for the entry's leading glyph.
function kindIcon(kind: AttentionKind) {
  switch (kind) {
    case 'auth_required':
      return KeyRound
    case 'rate_limited':
      return Timer
    default:
      return AlertTriangle
  }
}

function handleOpen(entry: AttentionEntry): void {
  // Open (or focus) the offending project's own window and route it to the
  // relevant surface — Tasks for failed tasks, just-focus for agent issues
  // (the IssueBanner + Resume live in the always-visible chat pane).
  void window.watchfire.focusProjectWindow(entry.projectId, entry.target, entry.taskNumber)
}

/**
 * v8 Inferno — mission control. A persistent "needs me" surface on the home
 * window aggregating auth/rate-limit agent issues and failed tasks across every
 * project, with click-through that opens/focuses the project window.
 */
export function NeedsAttentionPanel() {
  const entries = useNeedsAttention()

  if (entries.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] px-4 py-2.5 text-sm text-[var(--wf-text-muted)]">
        <CheckCircle2 size={15} className="shrink-0 text-[var(--wf-success)]" />
        <span>All clear — nothing needs your attention.</span>
      </div>
    )
  }

  return (
    <div className="rounded-[var(--wf-radius-md)] border border-[var(--wf-error)]/40 bg-[var(--wf-error)]/[0.06] overflow-hidden">
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--wf-error)]/20">
        <AlertTriangle size={15} className="shrink-0 text-[var(--wf-error)]" />
        <h3 className="text-sm font-semibold text-[var(--wf-text-primary)]">Needs attention</h3>
        <span className="px-1.5 py-0.5 rounded-full bg-[var(--wf-error)]/15 text-[var(--wf-error)] text-[11px] font-semibold leading-none tabular-nums">
          {entries.length}
        </span>
      </div>
      <ul className="divide-y divide-[var(--wf-border)]/60">
        {entries.map((entry) => {
          const Icon = kindIcon(entry.kind)
          return (
            <li key={entry.id}>
              <button
                type="button"
                onClick={() => handleOpen(entry)}
                title={`Open ${entry.projectName} →`}
                className="group w-full flex items-center gap-3 px-4 py-2 text-left hover:bg-[var(--wf-bg-elevated)] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fire-500/50"
              >
                <Icon size={14} className="shrink-0 text-[var(--wf-error)]" />
                <span className="shrink-0 text-sm font-medium text-[var(--wf-text-primary)] truncate max-w-[40%]">
                  {entry.projectName}
                </span>
                <span className="shrink-0 px-1.5 py-0.5 rounded-[var(--wf-radius-sm)] bg-[var(--wf-bg-elevated)] text-[var(--wf-text-secondary)] text-[10px] font-semibold leading-none uppercase tracking-wide">
                  {entry.label}
                </span>
                {entry.taskNumber !== undefined && (
                  <span className="shrink-0 text-[11px] text-[var(--wf-text-muted)] tabular-nums">
                    #{entry.taskNumber}
                  </span>
                )}
                <span className="flex-1 min-w-0 text-xs text-[var(--wf-text-muted)] truncate">
                  {entry.detail}
                </span>
                <ExternalLink
                  size={13}
                  className="shrink-0 text-[var(--wf-text-muted)] opacity-0 group-hover:opacity-100 transition-opacity"
                />
                <ChevronRight size={14} className="shrink-0 text-[var(--wf-text-muted)] group-hover:text-[var(--wf-text-secondary)]" />
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
