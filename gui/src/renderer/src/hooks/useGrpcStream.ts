import { useEffect, useRef } from 'react'

/**
 * Generic hook for gRPC server-streaming RPCs.
 * Provides automatic cleanup via AbortController.
 */
export function useGrpcStream<T>(
  streamFn: (signal: AbortSignal) => AsyncIterable<T>,
  onMessage: (msg: T) => void,
  deps: unknown[] = []
): void {
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    abortRef.current?.abort()
    const abort = new AbortController()
    abortRef.current = abort

    ;(async () => {
      try {
        for await (const msg of streamFn(abort.signal)) {
          onMessage(msg)
        }
      } catch (err: unknown) {
        if (err instanceof Error && err.name !== 'AbortError') {
          console.error('Stream error:', err)
        }
      }
    })()

    return () => {
      abort.abort()
    }
  }, deps)
}
