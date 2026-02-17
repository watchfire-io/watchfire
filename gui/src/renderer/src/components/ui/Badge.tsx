import { cn } from '../../lib/utils'
import type { ReactNode } from 'react'

interface BadgeProps {
  children: ReactNode
  variant?: 'default' | 'fire' | 'success' | 'warning' | 'error'
  className?: string
}

const variants: Record<string, string> = {
  default: 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-secondary)]',
  fire: 'bg-fire-500/20 text-fire-400',
  success: 'bg-emerald-900/30 text-emerald-400',
  warning: 'bg-amber-900/30 text-amber-400',
  error: 'bg-red-900/30 text-red-400'
}

export function Badge({ children, variant = 'default', className }: BadgeProps) {
  return (
    <span className={cn('inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium', variants[variant], className)}>
      {children}
    </span>
  )
}
