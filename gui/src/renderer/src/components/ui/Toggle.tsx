import { cn } from '../../lib/utils'

interface ToggleProps {
  checked: boolean
  onChange: (checked: boolean) => void
  label?: string
  description?: string
  disabled?: boolean
}

export function Toggle({ checked, onChange, label, description, disabled }: ToggleProps) {
  return (
    <label className={cn('flex items-center gap-3 cursor-pointer', disabled && 'opacity-50 pointer-events-none')}>
      <button
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        disabled={disabled}
        className={cn(
          'relative w-9 h-5 rounded-full transition-colors duration-200 shrink-0',
          checked ? 'bg-fire-500' : 'bg-[var(--wf-border-light)]'
        )}
      >
        <span
          className={cn(
            'absolute top-0.5 left-0.5 w-4 h-4 bg-white rounded-full transition-transform duration-200',
            checked && 'translate-x-4'
          )}
        />
      </button>
      {(label || description) && (
        <div className="flex flex-col">
          {label && <span className="text-sm font-medium text-[var(--wf-text-primary)]">{label}</span>}
          {description && <span className="text-xs text-[var(--wf-text-muted)]">{description}</span>}
        </div>
      )}
    </label>
  )
}
