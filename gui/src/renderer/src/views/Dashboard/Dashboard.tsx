import { useProjectsStore } from '../../stores/projects-store'
import { ProjectCard } from './ProjectCard'
import { EmptyState } from './EmptyState'

export function Dashboard() {
  const projects = useProjectsStore((s) => s.projects)
  const loading = useProjectsStore((s) => s.loading)

  if (loading && projects.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="w-6 h-6 border-2 border-[var(--wf-fire)] border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  if (projects.length === 0) {
    return <EmptyState />
  }

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="max-w-5xl mx-auto">
        <div className="mb-6">
          <h2 className="font-heading text-xl font-semibold text-[var(--wf-text-primary)]">Dashboard</h2>
          <p className="text-sm text-[var(--wf-text-muted)] mt-1">
            Overview of all your projects and their current status.
          </p>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {projects.map((p) => (
            <ProjectCard key={p.projectId} project={p} />
          ))}
        </div>
      </div>
    </div>
  )
}
