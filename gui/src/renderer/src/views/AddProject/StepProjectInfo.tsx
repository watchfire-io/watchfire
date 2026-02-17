import { useEffect, useRef } from 'react'
import { FolderOpen } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import type { WizardData } from './AddProjectWizard'

interface Props {
  data: WizardData
  onChange: (partial: Partial<WizardData>) => void
  onFolderSelected?: (path: string) => void
}

export function StepProjectInfo({ data, onChange, onFolderSelected }: Props) {
  const opened = useRef(false)

  const selectFolder = async () => {
    const path = await window.watchfire.openFolderDialog()
    if (path) {
      const name = path.split('/').pop() || path
      onChange({ path, name: data.name || name })
      onFolderSelected?.(path)
    }
  }

  // Auto-open folder dialog on first mount if no path selected
  useEffect(() => {
    if (!data.path && !opened.current) {
      opened.current = true
      selectFolder()
    }
  }, [])

  return (
    <div className="max-w-md space-y-5">
      <div>
        <label className="block text-sm font-medium text-[var(--wf-text-secondary)] mb-1.5">
          Project Folder
        </label>
        <div className="flex items-center gap-2">
          <div className="flex-1 px-3 py-2 rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-sm truncate">
            {data.path || <span className="text-[var(--wf-text-muted)]">No folder selected</span>}
          </div>
          <Button variant="secondary" onClick={selectFolder}>
            <FolderOpen size={14} />
            Browse
          </Button>
        </div>
      </div>

      <Input
        label="Project Name"
        value={data.name}
        onChange={(e) => onChange({ name: e.target.value })}
        placeholder="My Project"
      />
    </div>
  )
}
