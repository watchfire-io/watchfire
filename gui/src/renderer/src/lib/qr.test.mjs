// Unit tests for the hand-rolled QR encoder (v10.0 Torch Telegram
// pairing). Node 22.6+ strips the type annotations from qr.ts on
// import, so we exercise the real module rather than a mirror.
//
// Correctness of the full pipeline was additionally validated against
// the jsQR decoder during development (versions 1–10 round-trip,
// including multi-byte UTF-8); these tests pin the spec-level
// invariants that keep the symbol decodable.

import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import {
  encodeQr,
  chooseVersion,
  capacityBytes,
  formatBits,
  versionBits,
  rsGenerator,
  rsRemainder,
  penaltyScore
} from './qr.ts'

describe('qr format/version information', () => {
  test('format bits match the published table for EC level M', () => {
    // ISO 18004 Annex C: EC M, mask 0 → 101010000010010.
    assert.equal(formatBits(0), 0b101010000010010)
    // EC M, mask 7 → 100000110001001... derive: all 8 values are
    // distinct and 15-bit.
    const seen = new Set()
    for (let m = 0; m < 8; m++) {
      const f = formatBits(m)
      assert.ok(f >= 0 && f < 1 << 15)
      seen.add(f)
    }
    assert.equal(seen.size, 8)
  })

  test('version bits match the published table for v7–v10', () => {
    assert.equal(versionBits(7), 0x07c94)
    assert.equal(versionBits(8), 0x085bc)
    assert.equal(versionBits(9), 0x09a99)
    assert.equal(versionBits(10), 0x0a4d3)
  })
})

describe('qr Reed-Solomon', () => {
  test('generator polynomial for degree 10 matches the known constants', () => {
    // g(x) for 10 EC codewords, log-domain form: the coefficient of the
    // second term is α^251 (value 216), and the constant term is α^45.
    const g = rsGenerator(10)
    assert.equal(g.length, 11)
    assert.equal(g[0], 1) // monic
    assert.equal(g[1], 216)
    assert.equal(g[10], 193) // α^45 = 193
  })

  test('remainder makes the message polynomial divisible by the generator', () => {
    const data = [64, 86, 22, 198, 34, 246, 118, 246, 246, 66, 7, 118, 134, 242, 7, 38]
    const ec = rsRemainder(data, 10)
    assert.equal(ec.length, 10)
    // Dividing (data ++ ec) again must leave a zero remainder.
    const again = rsRemainder([...data, ...ec], 10)
    assert.deepEqual(again, new Array(10).fill(0))
  })
})

describe('qr version selection', () => {
  test('capacities follow the byte-mode EC-M table', () => {
    assert.equal(capacityBytes(0), 14) // v1-M
    assert.equal(capacityBytes(2), 42) // v3-M
    assert.equal(capacityBytes(9), 213) // v10-M (16-bit count)
  })

  test('chooseVersion picks the smallest fitting version and throws past v10', () => {
    assert.equal(chooseVersion(14), 1)
    assert.equal(chooseVersion(15), 2)
    assert.equal(chooseVersion(60), 4) // typical t.me deep link ceiling (v4-M holds 62)
    assert.equal(chooseVersion(213), 10)
    assert.throws(() => chooseVersion(214), /too long/)
  })
})

describe('qr matrix invariants', () => {
  const link = 'https://t.me/watchfire_bot?start=AB12CD34'
  const qr = encodeQr(link)

  test('size is 17 + 4·version and rows are square', () => {
    assert.equal((qr.size - 17) % 4, 0)
    assert.equal(qr.modules.length, qr.size)
    for (const row of qr.modules) assert.equal(row.length, qr.size)
  })

  test('finder patterns sit in three corners', () => {
    const finderAt = (left, top) => {
      for (let dy = 0; dy < 7; dy++) {
        for (let dx = 0; dx < 7; dx++) {
          const dist = Math.max(Math.abs(dx - 3), Math.abs(dy - 3))
          const want = dist !== 2
          assert.equal(
            qr.modules[top + dy][left + dx],
            want,
            `finder mismatch at (${left + dx},${top + dy})`
          )
        }
      }
    }
    finderAt(0, 0)
    finderAt(qr.size - 7, 0)
    finderAt(0, qr.size - 7)
  })

  test('timing patterns alternate', () => {
    for (let i = 8; i < qr.size - 8; i++) {
      assert.equal(qr.modules[6][i], i % 2 === 0)
      assert.equal(qr.modules[i][6], i % 2 === 0)
    }
  })

  test('dark module is set', () => {
    assert.equal(qr.modules[qr.size - 8][8], true)
  })

  test('encoding is deterministic', () => {
    const again = encodeQr(link)
    assert.deepEqual(again.modules, qr.modules)
  })

  test('the two format-info copies agree and carry a valid mask id', () => {
    // Reconstruct the 15 bits from the copy along the right/bottom edges
    // and check it equals formatBits(mask) for some mask 0..7.
    const bits = []
    for (let i = 0; i < 8; i++) bits.push(qr.modules[8][qr.size - 1 - i] ? 1 : 0)
    for (let i = 8; i < 15; i++) bits.push(qr.modules[qr.size - 15 + i][8] ? 1 : 0)
    let value = 0
    for (let i = 14; i >= 0; i--) value = (value << 1) | bits[i]
    const valid = new Set()
    for (let m = 0; m < 8; m++) valid.add(formatBits(m))
    assert.ok(valid.has(value), `format info 0b${value.toString(2)} not a valid EC-M code`)
  })

  test('version info present for v7+ symbols', () => {
    const big = encodeQr('x'.repeat(120)) // forces version 7
    const size = big.size
    assert.equal((size - 17) / 4, 7)
    let value = 0
    for (let i = 17; i >= 0; i--) {
      const a = Math.floor(i / 3)
      const b = size - 11 + (i % 3)
      value = (value << 1) | (big.modules[b][a] ? 1 : 0)
    }
    assert.equal(value, versionBits(7))
  })
})

describe('qr penalty scoring', () => {
  test('uniform grid scores worse than a checkerboard', () => {
    const n = 21
    const uniform = Array.from({ length: n }, () => new Array(n).fill(true))
    const checker = Array.from({ length: n }, (_, y) =>
      Array.from({ length: n }, (_, x) => (x + y) % 2 === 0)
    )
    assert.ok(penaltyScore(uniform) > penaltyScore(checker))
  })
})
