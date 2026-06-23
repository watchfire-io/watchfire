import { Flame } from 'lucide-react'
import { cn } from '../lib/utils'

// The 3-phase wildfire loop, in cycle order. Mirrors the daemon's
// AgentStatus.wildfire_phase values ("execute" | "refine" | "generate")
// and the TUI status surface (internal/tui/header.go / statusbar.go).
const PHASES = ['execute', 'refine', 'generate'] as const

const PHASE_LABEL: Record<string, string> = {
  execute: 'Execute',
  refine: 'Refine',
  generate: 'Generate'
}

interface Props {
  // AgentStatus.wildfire_phase: one of PHASES while looping, "" when the
  // loop is between phases / idle.
  phase: string
  // Dense single-chip form ("Wildfire · Execute") for space-constrained
  // surfaces like the mission-control dashboard cards. Default false renders
  // the full Execute → Refine → Generate stepper.
  compact?: boolean
  className?: string
}

/**
 * Compact live indicator for the wildfire loop. Renders the three phases as a
 * stepper (Execute → Refine → Generate) with the current phase highlighted and
 * the flame pulsing while a phase is active. Falls back to a quiet "Idle"
 * treatment when no phase is reported.
 */
export function WildfirePhaseBadge({ phase, compact = false, className }: Props) {
  const active = (PHASES as readonly string[]).includes(phase)

  if (compact) {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium',
          'bg-fire-500/15 border border-fire-500/30 text-fire-400',
          className
        )}
        title={active ? `Wildfire phase: ${PHASE_LABEL[phase]}` : 'Wildfire running (idle between phases)'}
      >
        <Flame size={10} className={cn('text-fire-400 shrink-0', active && 'animate-pulse')} />
        <span className="truncate">Wildfire · {active ? PHASE_LABEL[phase] : 'Idle'}</span>
      </span>
    )
  }

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium',
        'bg-fire-500/15 border border-fire-500/30',
        className
      )}
      title={active ? `Wildfire phase: ${PHASE_LABEL[phase]}` : 'Wildfire running (idle between phases)'}
    >
      <Flame size={11} className={cn('text-fire-400', active && 'animate-pulse')} />
      {active ? (
        <span className="inline-flex items-center gap-1">
          {PHASES.map((p, i) => (
            <span key={p} className="inline-flex items-center gap-1">
              <span className={cn(p === phase ? 'text-fire-400 font-semibold' : 'text-[var(--wf-text-muted)]')}>
                {PHASE_LABEL[p]}
              </span>
              {i < PHASES.length - 1 && <span className="text-[var(--wf-text-muted)] opacity-50">→</span>}
            </span>
          ))}
        </span>
      ) : (
        <span className="text-fire-400">Wildfire · Idle</span>
      )}
    </span>
  )
}
