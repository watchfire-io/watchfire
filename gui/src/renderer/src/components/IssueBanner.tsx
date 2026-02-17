import { AlertTriangle, RefreshCw } from 'lucide-react'
import type { AgentIssue } from '../generated/watchfire_pb'

interface IssueBannerProps {
  issue: AgentIssue
  onResume?: () => void
}

export function IssueBanner({ issue, onResume }: IssueBannerProps) {
  return (
    <div className="flex items-center gap-3 px-4 py-2 bg-amber-900/30 border-b border-amber-700/40 text-amber-200 text-sm">
      <AlertTriangle size={16} className="shrink-0 text-amber-400" />
      <span className="flex-1">
        {issue.issueType === 'auth_required'
          ? 'Authentication required — agent is paused'
          : issue.issueType === 'rate_limited'
            ? `Rate limited — ${issue.message || 'waiting for reset'}`
            : issue.message || 'Agent issue detected'}
      </span>
      {onResume && (
        <button
          onClick={onResume}
          className="flex items-center gap-1 px-2 py-1 text-xs font-medium rounded bg-amber-700/50 hover:bg-amber-700/70 transition-colors"
        >
          <RefreshCw size={12} />
          Resume
        </button>
      )}
    </div>
  )
}
