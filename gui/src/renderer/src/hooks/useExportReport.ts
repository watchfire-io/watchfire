// useExportReport — v6.0 Ember shared export entry point.
//
// Lands an `ExportReport` gRPC call on the daemon and triggers a browser
// download via `URL.createObjectURL(new Blob([content], { type: mime }))`.
// Three Insights surfaces consume this hook (per-task InspectTab, per-project
// InsightsTab, dashboard rollup card) plus the v6.0 weekly DigestModal —
// each of those surfaces sits behind one of the helper export-fns returned
// here and never has to know about the gRPC client directly.

import { useCallback, useState } from 'react'
import { create } from '@bufbuild/protobuf'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import {
  ExportFormat,
  ExportReportRequestSchema,
  type ExportReportResponse
} from '../generated/watchfire_pb'
import { getInsightsClient } from '../lib/grpc-client'

/** ExportFormatLabel — label keyed by the format the user picked. */
export type ExportFormatLabel = 'markdown' | 'csv'

function labelToProto(format: ExportFormatLabel): ExportFormat {
  return format === 'csv' ? ExportFormat.CSV : ExportFormat.MARKDOWN
}

export interface ExportWindow {
  start?: Date
  end?: Date
}

/** Optional injection seam — useExportReport({ client }) lets tests stub the
 *  gRPC client without monkey-patching the lib. Production callers omit it
 *  and the hook resolves the singleton via getInsightsClient(). */
export interface UseExportReportOptions {
  client?: { exportReport: (req: unknown) => Promise<ExportReportResponse> }
  // download is also injectable for tests so the hook can verify the Blob
  // URL machinery is invoked without actually hitting the DOM.
  download?: (resp: ExportReportResponse) => void
}

interface ExportReportApi {
  /** Export a single task. id format: `<project_id>:<task_number>`. */
  exportSingleTask: (id: string, format: ExportFormatLabel) => Promise<ExportReportResponse>
  /** Export a per-project report. */
  exportProject: (
    projectId: string,
    format: ExportFormatLabel,
    window?: ExportWindow
  ) => Promise<ExportReportResponse>
  /** Export the fleet-wide rollup. */
  exportGlobal: (format: ExportFormatLabel, window?: ExportWindow) => Promise<ExportReportResponse>
  /** True while a request is in flight; used to disable the pill. */
  loading: boolean
  /** Last error from a failed call; cleared on the next successful call. */
  error: Error | null
}

export function useExportReport(opts: UseExportReportOptions = {}): ExportReportApi {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const run = useCallback(
    async (
      buildScope: (req: ReturnType<typeof create<typeof ExportReportRequestSchema>>) => void,
      format: ExportFormatLabel,
      window?: ExportWindow
    ): Promise<ExportReportResponse> => {
      setLoading(true)
      setError(null)
      try {
        const req = create(ExportReportRequestSchema)
        req.format = labelToProto(format)
        if (window?.start) req.windowStart = timestampFromDate(window.start)
        if (window?.end) req.windowEnd = timestampFromDate(window.end)
        buildScope(req)

        const client = opts.client ?? (getInsightsClient() as unknown as UseExportReportOptions['client'])
        if (!client) {
          throw new Error('insights gRPC client unavailable')
        }
        const resp = (await client.exportReport(req)) as ExportReportResponse
        const dl = opts.download ?? defaultDownload
        dl(resp)
        return resp
      } catch (err) {
        const e = err instanceof Error ? err : new Error(String(err))
        setError(e)
        throw e
      } finally {
        setLoading(false)
      }
    },
    [opts.client, opts.download]
  )

  const exportSingleTask = useCallback(
    (id: string, format: ExportFormatLabel) =>
      run((req) => {
        req.scope = { case: 'singleTask', value: id }
      }, format),
    [run]
  )

  const exportProject = useCallback(
    (projectId: string, format: ExportFormatLabel, window?: ExportWindow) =>
      run(
        (req) => {
          req.scope = { case: 'projectId', value: projectId }
        },
        format,
        window
      ),
    [run]
  )

  const exportGlobal = useCallback(
    (format: ExportFormatLabel, window?: ExportWindow) =>
      run(
        (req) => {
          req.scope = { case: 'global', value: true }
        },
        format,
        window
      ),
    [run]
  )

  return { exportSingleTask, exportProject, exportGlobal, loading, error }
}

/** defaultDownload triggers a browser download for an ExportReportResponse.
 *  Pulled out so it can be unit-tested and so callers that already render
 *  the bytes inline (e.g. the digest modal) can swap it out. */
export function defaultDownload(resp: ExportReportResponse): void {
  // Slice into a fresh ArrayBuffer-backed view — the generated TS bindings
  // declare `content: Uint8Array<ArrayBufferLike>`, which the DOM Blob
  // constructor types reject (it requires ArrayBuffer specifically). The
  // copy is on the order of ~KB so the cost is irrelevant.
  const view = new Uint8Array(resp.content.byteLength)
  view.set(resp.content)
  const blob = new Blob([view], { type: resp.mime || 'application/octet-stream' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = resp.filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  // Revoke async — Safari occasionally drops the download if revoked
  // synchronously inside the click handler.
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
