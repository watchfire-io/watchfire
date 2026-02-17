import { useState } from 'react'
import { MessageSquare, GitBranch, ScrollText } from 'lucide-react'
import { cn } from '../../../lib/utils'
import { ChatTab } from './ChatTab'
import { BranchesTab } from './BranchesTab'
import { LogsTab } from './LogsTab'

type RightTab = 'chat' | 'branches' | 'logs'

const TABS: { key: RightTab; icon: typeof MessageSquare; label: string }[] = [
  { key: 'chat', icon: MessageSquare, label: 'Chat' },
  { key: 'branches', icon: GitBranch, label: 'Branches' },
  { key: 'logs', icon: ScrollText, label: 'Logs' }
]

interface Props {
  projectId: string
}

export function RightPanel({ projectId }: Props) {
  const [tab, setTab] = useState<RightTab>('chat')

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-1 px-3 py-1 border-b border-[var(--wf-border)]">
        {TABS.map((t) => {
          const Icon = t.icon
          return (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              className={cn(
                'flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium rounded-[var(--wf-radius-md)] transition-colors',
                tab === t.key
                  ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)]'
                  : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-secondary)]'
              )}
            >
              <Icon size={13} />
              {t.label}
            </button>
          )
        })}
      </div>
      <div className="flex-1 overflow-hidden">
        {tab === 'chat' && <ChatTab projectId={projectId} />}
        {tab === 'branches' && <BranchesTab projectId={projectId} />}
        {tab === 'logs' && <LogsTab projectId={projectId} />}
      </div>
    </div>
  )
}
