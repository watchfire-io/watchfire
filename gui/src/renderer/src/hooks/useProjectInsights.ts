// useProjectInsights — v6.0 Ember per-project rollup gRPC hook.
//
// Mirrors useGlobalInsights but scopes the request to one project. The
// daemon caches the response under `~/.watchfire/insights-cache/<id>.json`
// keyed by (window_start, window_end), so re-fetches on window flips are
// cheap.

import { useCallback, useEffect, useRef, useState } from 'react'
import { create } from '@bufbuild/protobuf'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import {
  GetProjectInsightsRequestSchema,
  type ProjectInsights
} from '../generated/watchfire_pb'
import { getInsightsClient } from '../lib/grpc-client'
import { windowToRange, type InsightsWindow } from '../lib/insights-rollup'

interface UseProjectInsightsResult {
  insights: ProjectInsights | null
  loading: boolean
  error: Error | null
  refetch: () => Promise<void>
}

export function useProjectInsights(
  projectId: string,
  window: InsightsWindow
): UseProjectInsightsResult {
  const [insights, setInsights] = useState<ProjectInsights | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const reqSeq = useRef(0)

  const fetchInsights = useCallback(async () => {
    if (!projectId) return
    const seq = ++reqSeq.current
    setLoading(true)
    setError(null)
    try {
      const req = create(GetProjectInsightsRequestSchema)
      req.projectId = projectId
      const range = windowToRange(window)
      if (range.start) req.windowStart = timestampFromDate(range.start)
      if (range.end) req.windowEnd = timestampFromDate(range.end)
      const client = getInsightsClient()
      const resp = await client.getProjectInsights(req)
      // Drop stale results — slow window=7d fetch must not overwrite a
      // newer window=30d response.
      if (reqSeq.current !== seq) return
      setInsights(resp)
    } catch (err) {
      if (reqSeq.current !== seq) return
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      if (reqSeq.current === seq) setLoading(false)
    }
  }, [projectId, window])

  useEffect(() => {
    void fetchInsights()
  }, [fetchInsights])

  return { insights, loading, error, refetch: fetchInsights }
}
