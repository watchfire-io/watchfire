import type { WizardData } from './AddProjectWizard'

interface Props {
  data: WizardData
  onChange: (partial: Partial<WizardData>) => void
}

export function StepDefinition({ data, onChange }: Props) {
  return (
    <div className="max-w-2xl space-y-3">
      <div className="flex items-center justify-between">
        <label className="text-sm font-medium text-[var(--wf-text-secondary)]">
          Project Definition
        </label>
        <span className="text-xs text-[var(--wf-text-muted)]">Optional — Markdown</span>
      </div>
      <textarea
        value={data.definition}
        onChange={(e) => onChange({ definition: e.target.value })}
        placeholder="Describe your project, its architecture, coding conventions, and anything an AI coding agent should know..."
        className="w-full h-64 px-4 py-3 rounded-[var(--wf-radius-lg)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-sm font-mono leading-relaxed text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 focus:ring-1 focus:ring-fire-500/30 transition-colors resize-none"
      />
    </div>
  )
}
