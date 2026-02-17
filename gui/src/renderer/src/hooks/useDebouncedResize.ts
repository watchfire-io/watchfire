import { useEffect, useRef } from 'react'

/**
 * Debounced resize observer — calls the callback at most once every `delay` ms.
 */
export function useDebouncedResize(
  ref: React.RefObject<HTMLElement | null>,
  callback: (entry: ResizeObserverEntry) => void,
  delay = 100
): void {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return

    const observer = new ResizeObserver((entries) => {
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => {
        if (entries[0]) callback(entries[0])
      }, delay)
    })

    observer.observe(el)
    return () => {
      observer.disconnect()
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [ref, callback, delay])
}
