import { cn } from '../../lib/utils'
import type { ReactNode, HTMLAttributes } from 'react'

interface CardProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode
  hoverable?: boolean
}

export function Card({ children, hoverable, className, ...props }: CardProps) {
  return (
    <div
      className={cn(
        'bg-[var(--wf-bg-secondary)] border border-[var(--wf-border)] rounded-[var(--wf-radius-lg)] p-5 transition-all',
        hoverable && 'hover:border-[var(--wf-fire)] hover:-translate-y-0.5 cursor-pointer',
        className
      )}
      {...props}
    >
      {children}
    </div>
  )
}
