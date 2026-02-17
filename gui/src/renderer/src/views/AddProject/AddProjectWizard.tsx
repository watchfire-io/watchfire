import { useState, useCallback } from 'react'
import { ArrowLeft, ArrowRight, Check } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { useAppStore } from '../../stores/app-store'
import { useProjectsStore } from '../../stores/projects-store'
import { getProjectClient } from '../../lib/grpc-client'
import { useToast } from '../../components/ui/Toast'
import { StepProjectInfo } from './StepProjectInfo'
import { StepGitConfig } from './StepGitConfig'
import { StepDefinition } from './StepDefinition'

export interface WizardData {
  path: string
  name: string
  defaultBranch: string
  autoMerge: boolean
  autoDeleteBranch: boolean
  autoStartTasks: boolean
  definition: string
}

const STEPS = ['Project', 'Git Config', 'Definition']

export function AddProjectWizard() {
  const [step, setStep] = useState(0)
  const [creating, setCreating] = useState(false)
  const setView = useAppStore((s) => s.setView)
  const selectProject = useAppStore((s) => s.selectProject)
  const fetchProjects = useProjectsStore((s) => s.fetchProjects)
  const { toast } = useToast()

  const [data, setData] = useState<WizardData>({
    path: '',
    name: '',
    defaultBranch: 'main',
    autoMerge: true,
    autoDeleteBranch: true,
    autoStartTasks: true,
    definition: ''
  })

  const update = (partial: Partial<WizardData>) => setData((d) => ({ ...d, ...partial }))
  const canNext = step === 0 ? data.path !== '' : true

  const importExistingProject = useCallback(async (path: string) => {
    setCreating(true)
    try {
      const client = getProjectClient()
      const project = await client.createProject({
        path,
        name: '',
        defaultBranch: '',
        autoMerge: false,
        autoDeleteBranch: false,
        autoStartTasks: false,
        definition: ''
      })
      await fetchProjects()
      toast('Project imported', 'success')
      selectProject(project.projectId)
    } catch (err) {
      toast(String(err), 'error')
      setCreating(false)
    }
  }, [fetchProjects, selectProject, toast])

  const handleFolderSelected = useCallback(async (path: string) => {
    const exists = await window.watchfire.checkProjectExists(path)
    if (exists) {
      importExistingProject(path)
    }
  }, [importExistingProject])

  const handleCreate = async () => {
    setCreating(true)
    try {
      const client = getProjectClient()
      const project = await client.createProject({
        path: data.path,
        name: data.name,
        defaultBranch: data.defaultBranch,
        autoMerge: data.autoMerge,
        autoDeleteBranch: data.autoDeleteBranch,
        autoStartTasks: data.autoStartTasks,
        definition: data.definition
      })
      await fetchProjects()
      toast('Project created', 'success')
      selectProject(project.projectId)
    } catch (err) {
      toast(String(err), 'error')
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <div className="px-6 py-4 border-b border-[var(--wf-border)]">
        <div className="flex items-center gap-2 mb-3">
          <button
            onClick={() => setView('dashboard')}
            className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
          >
            <ArrowLeft size={18} />
          </button>
          <h2 className="font-heading text-lg font-semibold">Add Project</h2>
        </div>
        {/* Step indicator */}
        <div className="flex items-center gap-2">
          {STEPS.map((label, i) => (
            <div key={label} className="flex items-center gap-2">
              <div
                className={`flex items-center justify-center w-6 h-6 rounded-full text-xs font-medium transition-colors ${
                  i <= step
                    ? 'bg-fire-500 text-white'
                    : 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-muted)]'
                }`}
              >
                {i < step ? <Check size={12} /> : i + 1}
              </div>
              <span className={`text-xs ${i <= step ? 'text-[var(--wf-text-primary)]' : 'text-[var(--wf-text-muted)]'}`}>
                {label}
              </span>
              {i < STEPS.length - 1 && <div className="w-8 h-px bg-[var(--wf-border)]" />}
            </div>
          ))}
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {step === 0 && <StepProjectInfo data={data} onChange={update} onFolderSelected={handleFolderSelected} />}
        {step === 1 && <StepGitConfig data={data} onChange={update} />}
        {step === 2 && <StepDefinition data={data} onChange={update} />}
      </div>

      <div className="flex items-center justify-between px-6 py-4 border-t border-[var(--wf-border)]">
        <Button variant="ghost" onClick={() => step > 0 ? setStep(step - 1) : setView('dashboard')}>
          <ArrowLeft size={14} />
          {step > 0 ? 'Back' : 'Cancel'}
        </Button>
        {step < STEPS.length - 1 ? (
          <Button onClick={() => setStep(step + 1)} disabled={!canNext}>
            Next
            <ArrowRight size={14} />
          </Button>
        ) : (
          <Button onClick={handleCreate} disabled={creating || !data.path}>
            {creating ? 'Creating...' : 'Create Project'}
          </Button>
        )}
      </div>
    </div>
  )
}
