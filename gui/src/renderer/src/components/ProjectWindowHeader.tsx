import { Settings, ArrowLeft, PanelsTopLeft } from 'lucide-react'
import { useAppStore } from '../stores/app-store'
import { useProjectsStore } from '../stores/projects-store'

// The slim chrome at the top of a project-scoped window (v8 "Inferno" Feature
// 1). A project window has no multi-project sidebar, so this bar doubles as the
// macOS title-bar drag region (with room for the traffic lights) and surfaces
// the project name plus the two cross-window affordances:
//   - "Open another project" → asks the main process to bring up the home /
//     dashboard window so the user can pick a different project.
//   - a Settings toggle so Cmd+, (and the gear) stays reachable in-window.
export function ProjectWindowHeader({ inSettings }: { inSettings: boolean }) {
  const projectId = useAppStore((s) =>
    s.windowScope.kind === 'project' ? s.windowScope.projectId : null
  )
  const setView = useAppStore((s) => s.setView)
  const project = useProjectsStore((s) =>
    s.projects.find((p) => p.projectId === projectId)
  )
  const name = project?.name ?? 'Watchfire'

  return (
    <div className="titlebar-drag flex items-center justify-between h-10 shrink-0 pl-20 pr-3 border-b border-[var(--wf-border)]">
      <span className="font-heading text-sm font-semibold truncate text-[var(--wf-text-primary)]">
        {name}
      </span>
      <div className="titlebar-no-drag flex items-center gap-1">
        <button
          onClick={() => window.watchfire.openHomeWindow()}
          title="Open another project"
          className="flex items-center gap-1.5 px-2 py-1 text-xs rounded-[var(--wf-radius-md)] text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)] transition-colors"
        >
          <PanelsTopLeft size={14} />
          Open another project
        </button>
        <button
          onClick={() => setView(inSettings ? 'project' : 'settings')}
          title={inSettings ? 'Back to project' : 'Settings'}
          className="p-1.5 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
        >
          {inSettings ? <ArrowLeft size={16} /> : <Settings size={16} />}
        </button>
      </div>
    </div>
  )
}
