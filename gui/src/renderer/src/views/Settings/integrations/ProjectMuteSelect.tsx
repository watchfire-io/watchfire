import type { Project } from '../../../generated/watchfire_pb'

interface Props {
  projects: Project[]
  value: string[]
  onChange: (next: string[]) => void
  label?: string
  description?: string
}

export function ProjectMuteSelect({
  projects,
  value,
  onChange,
  label = 'Mute for projects',
  description = 'Selected projects will not fan out notifications to this integration'
}: Props) {
  const toggle = (id: string) => {
    if (value.includes(id)) {
      onChange(value.filter((v) => v !== id))
    } else {
      onChange([...value, id])
    }
  }

  return (
    <fieldset className="space-y-1">
      <legend className="text-sm font-medium text-[var(--wf-text-secondary)] mb-0.5">{label}</legend>
      <p className="text-xs text-[var(--wf-text-muted)] mb-1">{description}</p>
      {projects.length === 0 ? (
        <p className="text-xs text-[var(--wf-text-muted)]">(no projects yet)</p>
      ) : (
        <div className="grid grid-cols-2 gap-1">
          {projects.map((p) => (
            <label key={p.projectId} className="flex items-center gap-2 cursor-pointer text-sm">
              <input
                type="checkbox"
                checked={value.includes(p.projectId)}
                onChange={() => toggle(p.projectId)}
                className="accent-fire-500"
              />
              <span className="truncate">{p.name}</span>
            </label>
          ))}
        </div>
      )}
    </fieldset>
  )
}
