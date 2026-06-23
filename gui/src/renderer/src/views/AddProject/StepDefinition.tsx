import type { WizardData } from './AddProjectWizard'
import { MarkdownEditor } from '../../components/ui/MarkdownEditor'

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
      <MarkdownEditor
        value={data.definition}
        onChange={(definition) => onChange({ definition })}
        minHeight={256}
        ariaLabel="Project definition"
        placeholder="Describe your project, its architecture, coding conventions, and anything an AI coding agent should know..."
      />
    </div>
  )
}
