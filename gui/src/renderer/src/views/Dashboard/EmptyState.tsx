import { Plus } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { useAppStore } from '../../stores/app-store'
import watchfireIcon from '../../assets/watchfire-icon.svg'

export function EmptyState() {
  const setView = useAppStore((s) => s.setView)

  return (
    <div className="flex-1 flex items-center justify-center">
      <div className="text-center max-w-sm">
        <div
          className="w-20 h-20 rounded-2xl flex items-center justify-center mx-auto mb-6"
          style={{
            background: 'linear-gradient(135deg, rgba(224,112,64,0.15) 0%, rgba(226,144,32,0.10) 100%)'
          }}
        >
          <img src={watchfireIcon} alt="" className="w-10 h-10" />
        </div>
        <h2 className="font-heading text-xl font-semibold mb-2 text-[var(--wf-text-primary)]">
          No projects yet
        </h2>
        <p className="text-sm text-[var(--wf-text-muted)] mb-6 leading-relaxed">
          Add a project folder to start orchestrating AI coding agents with Watchfire.
        </p>
        <Button onClick={() => setView('add-project')}>
          <Plus size={16} />
          Add Project
        </Button>
      </div>
    </div>
  )
}
