interface Events {
  taskFailed: boolean
  runComplete: boolean
  weeklyDigest: boolean
}

interface Props {
  value: Events
  onChange: (next: Events) => void
}

const ROWS: { key: keyof Events; label: string; description: string }[] = [
  {
    key: 'taskFailed',
    label: 'TASK_FAILED',
    description: 'Fan out when a task ends with success: false'
  },
  {
    key: 'runComplete',
    label: 'RUN_COMPLETE',
    description: 'Fan out when an autonomous run drains its queue'
  },
  {
    key: 'weeklyDigest',
    label: 'WEEKLY_DIGEST',
    description: 'Fan out the v6.0 Ember Markdown digest'
  }
]

export function EventCheckboxes({ value, onChange }: Props) {
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium text-[var(--wf-text-secondary)] mb-1">
        Enabled events
      </legend>
      {ROWS.map((row) => (
        <label key={row.key} className="flex items-start gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={value[row.key]}
            onChange={(e) => onChange({ ...value, [row.key]: e.target.checked })}
            className="mt-1 accent-fire-500"
          />
          <div className="flex flex-col">
            <span className="text-sm font-medium text-[var(--wf-text-primary)]">{row.label}</span>
            <span className="text-xs text-[var(--wf-text-muted)]">{row.description}</span>
          </div>
        </label>
      ))}
    </fieldset>
  )
}
