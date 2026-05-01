import { useEffect } from 'react'
import {
  acquireAgentPreview,
  useAgentPreviewStore
} from '../stores/agent-preview-store'

/**
 * Subscribes to the latest non-blank PTY line for `projectId` while `enabled`.
 * Returns the current preview text (empty string when disabled or unavailable).
 *
 * Multiple components calling this hook for the same projectId share a single
 * underlying gRPC stream via `acquireAgentPreview`'s ref counting.
 */
export function useAgentPreview(projectId: string, enabled: boolean): string {
  const preview = useAgentPreviewStore((s) => s.previews[projectId])

  useEffect(() => {
    if (!enabled) return
    return acquireAgentPreview(projectId)
  }, [projectId, enabled])

  return enabled ? preview ?? '' : ''
}
