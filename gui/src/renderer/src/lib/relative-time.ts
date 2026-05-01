import type { Timestamp } from '@bufbuild/protobuf/wkt'

/** Convert a protobuf Timestamp to milliseconds since epoch, or null if undefined. */
export function timestampToMs(ts: Timestamp | undefined): number | null {
  if (!ts) return null
  return Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6)
}

/**
 * Compact relative time: "<1m ago", "5m ago", "4h ago", "3d ago", "2mo ago", "1y ago".
 * Floors to the largest unit that fits.
 */
export function relativeTime(ms: number, now: number = Date.now()): string {
  const deltaSec = Math.max(0, Math.floor((now - ms) / 1000))
  if (deltaSec < 60) return '<1m ago'
  const deltaMin = Math.floor(deltaSec / 60)
  if (deltaMin < 60) return `${deltaMin}m ago`
  const deltaHour = Math.floor(deltaMin / 60)
  if (deltaHour < 24) return `${deltaHour}h ago`
  const deltaDay = Math.floor(deltaHour / 24)
  if (deltaDay < 30) return `${deltaDay}d ago`
  const deltaMonth = Math.floor(deltaDay / 30)
  if (deltaMonth < 12) return `${deltaMonth}mo ago`
  const deltaYear = Math.floor(deltaDay / 365)
  return `${deltaYear}y ago`
}
