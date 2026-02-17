/** Format a timestamp for display */
export function formatDate(ts: { seconds: bigint } | undefined): string {
  if (!ts) return ''
  const date = new Date(Number(ts.seconds) * 1000)
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  })
}

/** Format a task number with zero-padding */
export function formatTaskNumber(num: number): string {
  return `#${String(num).padStart(4, '0')}`
}

/** Status display helpers */
export function statusLabel(status: string): string {
  switch (status) {
    case 'draft': return 'Todo'
    case 'ready': return 'In Dev'
    case 'done': return 'Done'
    default: return status
  }
}

export function statusColor(status: string): string {
  switch (status) {
    case 'draft': return 'text-[var(--wf-text-muted)]'
    case 'ready': return 'text-[var(--wf-warning)]'
    case 'done': return 'text-[var(--wf-success)]'
    default: return 'text-[var(--wf-text-secondary)]'
  }
}

/** Merge class names */
export function cn(...classes: (string | false | undefined | null)[]): string {
  return classes.filter(Boolean).join(' ')
}
