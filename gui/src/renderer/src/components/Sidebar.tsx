import { LayoutDashboard, Plus, Settings, PanelLeftClose, PanelLeft, Wifi, WifiOff } from 'lucide-react'
import { useAppStore } from '../stores/app-store'
import { useProjectsStore } from '../stores/projects-store'
import { StatusDot } from './StatusDot'
import { cn } from '../lib/utils'
import watchfireIcon from '../assets/watchfire-icon.svg'

export function Sidebar() {
  const view = useAppStore((s) => s.view)
  const selectedProjectId = useAppStore((s) => s.selectedProjectId)
  const collapsed = useAppStore((s) => s.sidebarCollapsed)
  const connected = useAppStore((s) => s.connected)
  const setView = useAppStore((s) => s.setView)
  const selectProject = useAppStore((s) => s.selectProject)
  const toggleSidebar = useAppStore((s) => s.toggleSidebar)

  const projects = useProjectsStore((s) => s.projects)
  const agentStatuses = useProjectsStore((s) => s.agentStatuses)

  return (
    <aside
      className={cn(
        'flex flex-col h-full bg-[var(--wf-bg-secondary)] border-r border-[var(--wf-border)] transition-all duration-200',
        collapsed ? 'w-14' : 'w-48'
      )}
    >
      {/* Banner / drag area */}
      <div className="titlebar-drag shrink-0">
        {/* Space for macOS traffic lights */}
        <div className="h-10" />
        {!collapsed ? (
          <div className="flex items-center gap-2 px-4 pb-3 titlebar-no-drag">
            <img src={watchfireIcon} alt="Watchfire" className="w-6 h-6 shrink-0" />
            <span className="font-heading font-semibold text-sm tracking-tight text-[var(--wf-text-primary)]">
              watchfire
            </span>
          </div>
        ) : (
          <div className="flex justify-center pb-3 titlebar-no-drag">
            <img src={watchfireIcon} alt="Watchfire" className="w-6 h-6" />
          </div>
        )}
      </div>

      {/* Nav items */}
      <nav className="flex-1 flex flex-col gap-0.5 px-2 py-1 overflow-y-auto">
        <SidebarItem
          icon={<LayoutDashboard size={16} />}
          label="Dashboard"
          active={view === 'dashboard'}
          collapsed={collapsed}
          onClick={() => setView('dashboard')}
        />

        {/* Project list */}
        {!collapsed && projects.length > 0 && (
          <div className="mt-3 mb-1 px-2">
            <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--wf-text-muted)]">
              Projects
            </span>
          </div>
        )}

        {projects.map((p) => {
          const agentStatus = agentStatuses[p.projectId]
          const isRunning = agentStatus?.isRunning
          return (
            <SidebarItem
              key={p.projectId}
              icon={<StatusDot color={p.color || '#e07040'} pulsing={isRunning} size="sm" />}
              label={p.name}
              active={view === 'project' && selectedProjectId === p.projectId}
              collapsed={collapsed}
              onClick={() => selectProject(p.projectId)}
            />
          )
        })}

        <SidebarItem
          icon={<Plus size={16} />}
          label="Add Project"
          active={view === 'add-project'}
          collapsed={collapsed}
          onClick={() => setView('add-project')}
        />
      </nav>

      {/* Footer */}
      <div className="flex flex-col gap-0.5 px-2 py-2 border-t border-[var(--wf-border)]">
        <SidebarItem
          icon={<Settings size={16} />}
          label="Settings"
          active={view === 'settings'}
          collapsed={collapsed}
          onClick={() => setView('settings')}
        />

        <div className="flex items-center justify-between px-2 py-1">
          {!collapsed && (
            <span className="flex items-center gap-1.5 text-[11px] text-[var(--wf-text-muted)]">
              {connected ? (
                <>
                  <Wifi size={11} className="text-[var(--wf-success)] shrink-0" />
                  Connected
                </>
              ) : (
                <>
                  <WifiOff size={11} className="text-[var(--wf-error)] shrink-0" />
                  Disconnected
                </>
              )}
            </span>
          )}
          <button
            onClick={toggleSidebar}
            className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
          >
            {collapsed ? <PanelLeft size={14} /> : <PanelLeftClose size={14} />}
          </button>
        </div>
      </div>
    </aside>
  )
}

interface SidebarItemProps {
  icon: React.ReactNode
  label: string
  active?: boolean
  collapsed?: boolean
  onClick: () => void
}

function SidebarItem({ icon, label, active, collapsed, onClick }: SidebarItemProps) {
  return (
    <button
      onClick={onClick}
      title={collapsed ? label : undefined}
      className={cn(
        'flex items-center gap-2 px-2.5 py-1.5 rounded-[var(--wf-radius-md)] text-sm transition-colors w-full text-left',
        active
          ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-fire)]'
          : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)]',
        collapsed && 'justify-center px-0'
      )}
    >
      <span className="shrink-0 flex items-center justify-center w-4 h-4">{icon}</span>
      {!collapsed && <span className="truncate text-sm">{label}</span>}
    </button>
  )
}
