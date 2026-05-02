// useGlobalInsights — v6.0 Ember dashboard rollup gRPC hook.
//
// Calls `InsightsService.GetGlobalInsights` whenever the selected window
// changes and exposes the response + loading + error so the rollup card
// can render an empty / partial / full state. The daemon caches the
// response under `~/.watchfire/insights-cache/_global.json`, so re-fetches
// on window flips are cheap.

import { useCallback, useEffect, useRef, useState } from 'react'
import { create } from '@bufbuild/protobuf'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import {
  GetGlobalInsightsRequestSchema,
  type GlobalInsights
} from '../generated/watchfire_pb'
import { getInsightsClient } from '../lib/grpc-client'
import { windowToRange, type InsightsWindow } from '../lib/insights-rollup'

interface UseGlobalInsightsResult {
  insights: GlobalInsights | null
  loading: boolean
  error: Error | null
  refetch: () => Promise<void>
}

export function useGlobalInsights(window: InsightsWindow): UseGlobalInsightsResult {
  const [insights, setInsights] = useState<GlobalInsights | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const reqSeq = useRef(0)

  const fetchInsights = useCallback(async () => {
    const seq = ++reqSeq.current
    setLoading(true)
    setError(null)
    try {
      const req = create(GetGlobalInsightsRequestSchema)
      const range = windowToRange(window)
      if (range.start) req.windowStart = timestampFromDate(range.start)
      if (range.end) req.windowEnd = timestampFromDate(range.end)
      const client = getInsightsClient()
      const resp = await client.getGlobalInsights(req)
      // Drop stale results — a slow window=7d fetch must not overwrite a
      // newer window=30d response.
      if (reqSeq.current !== seq) return
      setInsights(resp)
    } catch (err) {
      if (reqSeq.current !== seq) return
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      if (reqSeq.current === seq) setLoading(false)
    }
  }, [window])

  useEffect(() => {
    void fetchInsights()
  }, [fetchInsights])

  return { insights, loading, error, refetch: fetchInsights }
}
