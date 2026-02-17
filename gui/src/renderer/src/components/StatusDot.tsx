import { cn } from '../lib/utils'

interface StatusDotProps {
  color?: string
  pulsing?: boolean
  size?: 'sm' | 'md'
  className?: string
}

export function StatusDot({ color = '#888', pulsing, size = 'md', className }: StatusDotProps) {
  const sizeClass = size === 'sm' ? 'w-2 h-2' : 'w-2.5 h-2.5'
  return (
    <span
      className={cn(
        'inline-block rounded-full shrink-0',
        sizeClass,
        pulsing && 'animate-pulse',
        className
      )}
      style={{ backgroundColor: color }}
    />
  )
}
