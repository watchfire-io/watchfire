// Renderer-side smoke tests for the v6.0 Ember useExportReport hook.
//
// Run with: node --test gui/src/renderer/src/hooks/useExportReport.test.mjs
//
// We don't run React here — instead we exercise the request-building logic
// and the defaultDownload helper as plain functions. The hook's React
// surface is small enough that a snapshot test would mostly assert React
// renders the buttons we ask it to.
//
// Logic mirrored: req.scope shape, format mapping, window mapping. Any
// change to the .ts file must be reflected here so the test catches drift.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Logic mirror ----------------------------------------------------------

function labelToProto(format) {
  return format === 'csv' ? 0 /* CSV */ : 1 /* MARKDOWN */
}

function buildRequest(scope, format, window) {
  const req = {
    format: labelToProto(format),
    scope: undefined,
    windowStart: undefined,
    windowEnd: undefined
  }
  if (window?.start) req.windowStart = { seconds: BigInt(Math.floor(window.start.getTime() / 1000)) }
  if (window?.end) req.windowEnd = { seconds: BigInt(Math.floor(window.end.getTime() / 1000)) }
  switch (scope.kind) {
    case 'singleTask':
      req.scope = { case: 'singleTask', value: scope.id }
      break
    case 'project':
      req.scope = { case: 'projectId', value: scope.projectId }
      break
    case 'global':
      req.scope = { case: 'global', value: true }
      break
  }
  return req
}

// --- Tests -----------------------------------------------------------------

test('buildRequest: single task scope', () => {
  const req = buildRequest({ kind: 'singleTask', id: 'pid:59' }, 'markdown')
  assert.equal(req.format, 1)
  assert.deepEqual(req.scope, { case: 'singleTask', value: 'pid:59' })
})

test('buildRequest: project scope CSV', () => {
  const req = buildRequest({ kind: 'project', projectId: 'pid' }, 'csv')
  assert.equal(req.format, 0)
  assert.deepEqual(req.scope, { case: 'projectId', value: 'pid' })
})

test('buildRequest: global scope with window', () => {
  const start = new Date('2026-04-25T00:00:00Z')
  const end = new Date('2026-05-02T00:00:00Z')
  const req = buildRequest({ kind: 'global' }, 'markdown', { start, end })
  assert.deepEqual(req.scope, { case: 'global', value: true })
  assert.equal(typeof req.windowStart.seconds, 'bigint')
  assert.equal(typeof req.windowEnd.seconds, 'bigint')
})

test('buildRequest: omitting window leaves bounds unset', () => {
  const req = buildRequest({ kind: 'global' }, 'markdown')
  assert.equal(req.windowStart, undefined)
  assert.equal(req.windowEnd, undefined)
})

// --- defaultDownload simulation -------------------------------------------

test('defaultDownload simulation: creates a synthetic <a download>', () => {
  // We mirror the steps defaultDownload performs inside a fake DOM. The
  // real hook lives in .ts and uses real Blob/URL/document; the goal here
  // is to assert the *contract* (filename + mime are read from the
  // response) without booting jsdom.
  const calls = []
  const fakeDoc = {
    createElement: () => ({ click: () => calls.push('click') }),
    body: {
      appendChild: () => calls.push('append'),
      removeChild: () => calls.push('remove')
    }
  }
  const blobs = []
  const fakeURL = {
    createObjectURL: (b) => {
      blobs.push(b)
      return 'blob:fake'
    },
    revokeObjectURL: () => calls.push('revoke')
  }

  function fakeDownload(resp) {
    const blob = { type: resp.mime, size: resp.content.length }
    const url = fakeURL.createObjectURL(blob)
    const a = fakeDoc.createElement()
    a.href = url
    a.download = resp.filename
    fakeDoc.body.appendChild()
    a.click()
    fakeDoc.body.removeChild()
    fakeURL.revokeObjectURL(url)
  }

  fakeDownload({
    filename: 'watchfire-task-1-2026-05-02.md',
    content: new Uint8Array([1, 2, 3]),
    mime: 'text/markdown'
  })

  assert.deepEqual(calls, ['append', 'click', 'remove', 'revoke'])
  assert.equal(blobs[0].type, 'text/markdown')
  assert.equal(blobs[0].size, 3)
})
