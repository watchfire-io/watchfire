import { forwardRef, type InputHTMLAttributes } from 'react'
import { cn } from '../../lib/utils'

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ label, error, className, id, ...props }, ref) => {
    const inputId = id || label?.toLowerCase().replace(/\s+/g, '-')
    return (
      <div className="flex flex-col gap-1.5">
        {label && (
          <label htmlFor={inputId} className="text-sm font-medium text-[var(--wf-text-secondary)]">
            {label}
          </label>
        )}
        <input
          ref={ref}
          id={inputId}
          className={cn(
            'px-3 py-2 rounded-[var(--wf-radius-md)] text-sm',
            'bg-[var(--wf-bg-primary)] border border-[var(--wf-border)]',
            'text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)]',
            'focus:outline-none focus:border-fire-500 focus:ring-1 focus:ring-fire-500/30',
            'transition-colors',
            error && 'border-[var(--wf-error)]',
            className
          )}
          {...props}
        />
        {error && <p className="text-xs text-[var(--wf-error)]">{error}</p>}
      </div>
    )
  }
)

Input.displayName = 'Input'
