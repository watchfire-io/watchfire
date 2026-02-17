import { Input } from '../../components/ui/Input'
import { Toggle } from '../../components/ui/Toggle'
import type { WizardData } from './AddProjectWizard'

interface Props {
  data: WizardData
  onChange: (partial: Partial<WizardData>) => void
}

export function StepGitConfig({ data, onChange }: Props) {
  return (
    <div className="max-w-md space-y-5">
      <Input
        label="Default Branch"
        value={data.defaultBranch}
        onChange={(e) => onChange({ defaultBranch: e.target.value })}
        placeholder="main"
      />

      <div className="space-y-4 pt-2">
        <Toggle
          checked={data.autoMerge}
          onChange={(v) => onChange({ autoMerge: v })}
          label="Auto-merge"
          description="Merge task branches when done"
        />
        <Toggle
          checked={data.autoDeleteBranch}
          onChange={(v) => onChange({ autoDeleteBranch: v })}
          label="Auto-delete branches"
          description="Remove worktree branches after merge"
        />
        <Toggle
          checked={data.autoStartTasks}
          onChange={(v) => onChange({ autoStartTasks: v })}
          label="Auto-start tasks"
          description="Chain to the next ready task automatically"
        />
      </div>
    </div>
  )
}
