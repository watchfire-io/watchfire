// Unit tests for MarkdownEditor's pure toolbar transforms. Following the
// repo-wide convention (TaskStatusBadge.test.mjs, settings-search.test.mjs):
// `node --test` has no TS/JSX toolchain in the loop, so we mirror the pure
// transform logic in plain JS here and pin its semantics. A source-level smoke
// check below asserts the .tsx still exports the matching functions, so drift
// is caught in review.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const __dirname = dirname(fileURLToPath(import.meta.url))

// --- mirrored logic (kept byte-for-byte in sync with MarkdownEditor.tsx) ---

function toggleInline(text, from, to, marker) {
  const selected = text.slice(from, to)
  const before = text.slice(Math.max(0, from - marker.length), from)
  const after = text.slice(to, to + marker.length)
  if (before === marker && after === marker) {
    const next = text.slice(0, from - marker.length) + selected + text.slice(to + marker.length)
    return { text: next, from: from - marker.length, to: to - marker.length }
  }
  const next = text.slice(0, from) + marker + selected + marker + text.slice(to)
  return { text: next, from: from + marker.length, to: to + marker.length }
}

function prefixLines(text, from, to, prefix) {
  const lineStart = text.lastIndexOf('\n', Math.max(0, from - 1)) + 1
  let lineEnd = text.indexOf('\n', to)
  if (lineEnd === -1) lineEnd = text.length
  const block = text.slice(lineStart, lineEnd)
  const lines = block.split('\n')
  const prefixed = lines.map((l) => prefix + l).join('\n')
  const next = text.slice(0, lineStart) + prefixed + text.slice(lineEnd)
  return { text: next, from: from + prefix.length, to: to + prefix.length * lines.length }
}

function insertLink(text, from, to) {
  const label = text.slice(from, to) || 'text'
  const insert = `[${label}](url)`
  const next = text.slice(0, from) + insert + text.slice(to)
  const urlStart = from + label.length + 3
  return { text: next, from: urlStart, to: urlStart + 3 }
}

// --- bold / inline wrapping ---

test('bold wraps the selection in ** and shifts the selection inside the markers', () => {
  const res = toggleInline('the word here', 4, 8, '**') // select "word"
  assert.equal(res.text, 'the **word** here')
  assert.equal(res.text.slice(res.from, res.to), 'word')
})

test('bold on an empty selection inserts ** ** with the caret between them', () => {
  const res = toggleInline('ab', 1, 1, '**')
  assert.equal(res.text, 'a****b')
  assert.equal(res.from, 3)
  assert.equal(res.to, 3)
})

test('bold toggles OFF when the selection is already wrapped in **', () => {
  // text "**word**", selection covers "word" (indices 2..6)
  const res = toggleInline('**word**', 2, 6, '**')
  assert.equal(res.text, 'word')
  assert.equal(res.text.slice(res.from, res.to), 'word')
})

test('italic uses _ markers (matches the preview renderer, which renders _x_)', () => {
  const res = toggleInline('a word b', 2, 6, '_')
  assert.equal(res.text, 'a _word_ b')
  assert.equal(res.text.slice(res.from, res.to), 'word')
})

test('inline code uses backtick markers', () => {
  const res = toggleInline('run x now', 4, 5, '`')
  assert.equal(res.text, 'run `x` now')
})

// --- list / heading line prefixes ---

test('bullet list prefixes a single selected line with "- "', () => {
  const res = prefixLines('first line', 0, 5, '- ')
  assert.equal(res.text, '- first line')
})

test('bullet list prefixes every line the selection touches', () => {
  const text = 'one\ntwo\nthree'
  // selection spans from inside "one" to inside "three"
  const res = prefixLines(text, 1, 11, '- ')
  assert.equal(res.text, '- one\n- two\n- three')
})

test('heading prefixes the current line with "## "', () => {
  const res = prefixLines('Title', 0, 0, '## ')
  assert.equal(res.text, '## Title')
})

test('prefix targets only the line under the caret, not neighbours', () => {
  const text = 'alpha\nbeta\ngamma'
  // caret inside "beta" (index 7), empty selection
  const res = prefixLines(text, 7, 7, '- ')
  assert.equal(res.text, 'alpha\n- beta\ngamma')
})

// --- links ---

test('link wraps the selection as [selected](url) and selects the url placeholder', () => {
  const res = insertLink('see docs here', 4, 8) // select "docs"
  assert.equal(res.text, 'see [docs](url) here')
  assert.equal(res.text.slice(res.from, res.to), 'url')
})

test('link with an empty selection inserts a [text](url) placeholder', () => {
  const res = insertLink('', 0, 0)
  assert.equal(res.text, '[text](url)')
  assert.equal(res.text.slice(res.from, res.to), 'url')
})

// --- source smoke: the .tsx still exports the mirrored functions ---

test('MarkdownEditor.tsx exports the mirrored transforms', () => {
  const src = readFileSync(join(__dirname, 'MarkdownEditor.tsx'), 'utf-8')
  assert.match(src, /export function toggleInline/)
  assert.match(src, /export function prefixLines/)
  assert.match(src, /export function insertLink/)
  assert.match(src, /export function MarkdownEditor/)
})
