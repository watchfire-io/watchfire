// Minimal QR code encoder — byte mode, error-correction level M,
// versions 1–10 (up to 213 payload bytes). Hand-rolled for v10.0 Torch
// so the Telegram pairing deep link renders as a QR code with zero
// external dependencies and zero network fetches. Implements ISO/IEC
// 18004: byte-mode segment encoding, Reed–Solomon EC over GF(256)
// (poly 0x11d), block interleaving, all 8 mask patterns with penalty
// scoring, and BCH-protected format/version information.
//
// The Telegram deep link (`https://t.me/<bot>?start=<code>`) tops out
// around 60 characters, which fits in version 5-M; versions up to 10
// are supported for headroom.

export interface QrCode {
  size: number
  /** modules[y][x] === true → dark module. No quiet zone included. */
  modules: boolean[][]
}

interface VersionSpec {
  ecPerBlock: number
  /** Data codewords per RS block, shorter blocks first. */
  blocks: number[]
  /** Alignment pattern center coordinates. */
  align: number[]
}

// Error-correction level M only (2 EC bits = 0b00).
const VERSIONS: VersionSpec[] = [
  { ecPerBlock: 10, blocks: [16], align: [] },
  { ecPerBlock: 16, blocks: [28], align: [6, 18] },
  { ecPerBlock: 26, blocks: [44], align: [6, 22] },
  { ecPerBlock: 18, blocks: [32, 32], align: [6, 26] },
  { ecPerBlock: 24, blocks: [43, 43], align: [6, 30] },
  { ecPerBlock: 16, blocks: [27, 27, 27, 27], align: [6, 34] },
  { ecPerBlock: 18, blocks: [31, 31, 31, 31], align: [6, 22, 38] },
  { ecPerBlock: 22, blocks: [38, 38, 39, 39], align: [6, 24, 42] },
  { ecPerBlock: 22, blocks: [36, 36, 36, 37, 37], align: [6, 26, 46] },
  { ecPerBlock: 26, blocks: [43, 43, 43, 43, 44], align: [6, 28, 50] }
]

// --- GF(256) arithmetic -------------------------------------------------

const GF_EXP = new Uint8Array(512)
const GF_LOG = new Uint8Array(256)
{
  let x = 1
  for (let i = 0; i < 255; i++) {
    GF_EXP[i] = x
    GF_LOG[x] = i
    x <<= 1
    if (x & 0x100) x ^= 0x11d
  }
  for (let i = 255; i < 512; i++) GF_EXP[i] = GF_EXP[i - 255]
}

function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0
  return GF_EXP[GF_LOG[a] + GF_LOG[b]]
}

/** Monic RS generator polynomial ∏(x − α^i), coefficients highest power first. */
export function rsGenerator(degree: number): number[] {
  let poly = [1]
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0)
    for (let j = 0; j < poly.length; j++) {
      next[j] ^= poly[j]
      next[j + 1] ^= gfMul(poly[j], GF_EXP[i])
    }
    poly = next
  }
  return poly
}

/** Reed–Solomon remainder (the EC codewords) for one block. */
export function rsRemainder(data: number[], degree: number): number[] {
  const gen = rsGenerator(degree)
  const res = new Array<number>(degree).fill(0)
  for (const b of data) {
    const factor = b ^ (res.shift() as number)
    res.push(0)
    for (let i = 0; i < degree; i++) res[i] ^= gfMul(gen[i + 1], factor)
  }
  return res
}

// --- format / version information --------------------------------------

/** 15-bit format info for EC level M + the given mask (BCH(15,5), masked). */
export function formatBits(mask: number): number {
  const data = mask & 7 // EC level M contributes 0b00 in the top 2 bits
  let rem = data
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >> 9) & 1 ? 0x537 : 0)
  return (((data << 10) | rem) ^ 0x5412) & 0x7fff
}

/** 18-bit version info for versions ≥ 7 (BCH(18,6)). */
export function versionBits(version: number): number {
  let rem = version
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >> 11) & 1 ? 0x1f25 : 0)
  return ((version << 12) | rem) & 0x3ffff
}

// --- capacity -----------------------------------------------------------

function dataCodewordCount(vi: number): number {
  return VERSIONS[vi].blocks.reduce((a, b) => a + b, 0)
}

/** Max payload bytes for version index vi (byte mode: 4-bit mode + 8/16-bit count header). */
export function capacityBytes(vi: number): number {
  return dataCodewordCount(vi) - (vi + 1 <= 9 ? 2 : 3)
}

/** Smallest supported version (1-based) that fits `byteLen` payload bytes. */
export function chooseVersion(byteLen: number): number {
  for (let i = 0; i < VERSIONS.length; i++) {
    if (byteLen <= capacityBytes(i)) return i + 1
  }
  throw new Error(`QR payload too long: ${byteLen} bytes (max ${capacityBytes(VERSIONS.length - 1)})`)
}

// --- mask patterns + penalty --------------------------------------------

const MASKS: ((y: number, x: number) => boolean)[] = [
  (y, x) => (y + x) % 2 === 0,
  (y) => y % 2 === 0,
  (_, x) => x % 3 === 0,
  (y, x) => (y + x) % 3 === 0,
  (y, x) => (Math.floor(y / 2) + Math.floor(x / 3)) % 2 === 0,
  (y, x) => ((y * x) % 2) + ((y * x) % 3) === 0,
  (y, x) => (((y * x) % 2) + ((y * x) % 3)) % 2 === 0,
  (y, x) => (((y + x) % 2) + ((y * x) % 3)) % 2 === 0
]

export function penaltyScore(g: boolean[][]): number {
  const size = g.length
  let p = 0
  // N1: runs of ≥5 same-colored modules in rows and columns.
  for (let axis = 0; axis < 2; axis++) {
    for (let i = 0; i < size; i++) {
      let run = 1
      for (let j = 1; j <= size; j++) {
        const cur = j < size ? (axis === 0 ? g[i][j] : g[j][i]) : null
        const prev = axis === 0 ? g[i][j - 1] : g[j - 1][i]
        if (cur !== null && cur === prev) {
          run++
        } else {
          if (run >= 5) p += 3 + run - 5
          run = 1
        }
      }
    }
  }
  // N2: 2×2 blocks of a single color.
  for (let y = 0; y < size - 1; y++) {
    for (let x = 0; x < size - 1; x++) {
      const c = g[y][x]
      if (g[y][x + 1] === c && g[y + 1][x] === c && g[y + 1][x + 1] === c) p += 3
    }
  }
  // N3: finder-like 1:1:3:1:1 pattern with 4 light modules on either side.
  const pats = [
    [true, false, true, true, true, false, true, false, false, false, false],
    [false, false, false, false, true, false, true, true, true, false, true]
  ]
  for (let axis = 0; axis < 2; axis++) {
    for (let i = 0; i < size; i++) {
      for (let j = 0; j + 11 <= size; j++) {
        for (const pat of pats) {
          let match = true
          for (let k = 0; k < 11; k++) {
            const cell = axis === 0 ? g[i][j + k] : g[j + k][i]
            if (cell !== pat[k]) {
              match = false
              break
            }
          }
          if (match) p += 40
        }
      }
    }
  }
  // N4: dark-module proportion deviation from 50%, in 5% steps.
  let dark = 0
  for (const row of g) for (const cell of row) if (cell) dark++
  p += 10 * Math.floor(Math.abs((dark * 100) / (size * size) - 50) / 5)
  return p
}

// --- matrix construction ------------------------------------------------

function drawFinder(modules: boolean[][], isFunc: boolean[][], left: number, top: number): void {
  const size = modules.length
  for (let dy = -1; dy <= 7; dy++) {
    for (let dx = -1; dx <= 7; dx++) {
      const x = left + dx
      const y = top + dy
      if (x < 0 || y < 0 || x >= size || y >= size) continue
      const inCore = dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6
      const dist = Math.max(Math.abs(dx - 3), Math.abs(dy - 3))
      modules[y][x] = inCore && dist !== 2
      isFunc[y][x] = true
    }
  }
}

function drawFormat(modules: boolean[][], mask: number): void {
  const size = modules.length
  const bits = formatBits(mask)
  const bit = (i: number): boolean => ((bits >> i) & 1) === 1
  // First copy around the top-left finder.
  for (let i = 0; i <= 5; i++) modules[i][8] = bit(i)
  modules[7][8] = bit(6)
  modules[8][8] = bit(7)
  modules[8][7] = bit(8)
  for (let i = 9; i < 15; i++) modules[8][14 - i] = bit(i)
  // Second copy split across the other two finders.
  for (let i = 0; i < 8; i++) modules[8][size - 1 - i] = bit(i)
  for (let i = 8; i < 15; i++) modules[size - 15 + i][8] = bit(i)
}

function buildMatrix(version: number, spec: VersionSpec, codewords: number[]): QrCode {
  const size = 17 + version * 4
  const modules: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const isFunc: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))

  drawFinder(modules, isFunc, 0, 0)
  drawFinder(modules, isFunc, size - 7, 0)
  drawFinder(modules, isFunc, 0, size - 7)

  // Timing patterns.
  for (let i = 8; i < size - 8; i++) {
    modules[6][i] = i % 2 === 0
    isFunc[6][i] = true
    modules[i][6] = i % 2 === 0
    isFunc[i][6] = true
  }

  // Alignment patterns (skip the three finder corners).
  for (const cy of spec.align) {
    for (const cx of spec.align) {
      if ((cx <= 8 && cy <= 8) || (cx >= size - 9 && cy <= 8) || (cx <= 8 && cy >= size - 9)) continue
      for (let dy = -2; dy <= 2; dy++) {
        for (let dx = -2; dx <= 2; dx++) {
          modules[cy + dy][cx + dx] = Math.max(Math.abs(dx), Math.abs(dy)) !== 1
          isFunc[cy + dy][cx + dx] = true
        }
      }
    }
  }

  // Reserve format info areas (filled in per-mask later).
  for (let i = 0; i <= 8; i++) {
    if (i !== 6) {
      isFunc[i][8] = true
      isFunc[8][i] = true
    }
  }
  for (let i = 0; i < 8; i++) isFunc[8][size - 1 - i] = true
  for (let i = 0; i < 7; i++) isFunc[size - 1 - i][8] = true
  // Dark module.
  modules[size - 8][8] = true
  isFunc[size - 8][8] = true

  // Version information (two 6×3 blocks, versions ≥ 7).
  if (version >= 7) {
    const vBits = versionBits(version)
    for (let i = 0; i < 18; i++) {
      const dark = ((vBits >> i) & 1) === 1
      const a = Math.floor(i / 3)
      const b = size - 11 + (i % 3)
      modules[b][a] = dark
      isFunc[b][a] = true
      modules[a][b] = dark
      isFunc[a][b] = true
    }
  }

  // Zigzag data placement. Unfilled trailing data modules are the
  // spec's remainder bits, which are 0 (light).
  const isData: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const totalBits = codewords.length * 8
  let bitIdx = 0
  let upward = true
  for (let x = size - 1; x >= 1; x -= 2) {
    if (x === 6) x = 5
    for (let k = 0; k < size; k++) {
      const y = upward ? size - 1 - k : k
      for (const xx of [x, x - 1]) {
        if (isFunc[y][xx]) continue
        modules[y][xx] =
          bitIdx < totalBits && ((codewords[bitIdx >> 3] >>> (7 - (bitIdx & 7))) & 1) === 1
        isData[y][xx] = true
        bitIdx++
      }
    }
    upward = !upward
  }

  // Try all 8 masks, keep the lowest penalty.
  let best: boolean[][] | null = null
  let bestPenalty = Infinity
  for (let m = 0; m < 8; m++) {
    const candidate = modules.map((row) => row.slice())
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        if (isData[y][x] && MASKS[m](y, x)) candidate[y][x] = !candidate[y][x]
      }
    }
    drawFormat(candidate, m)
    const p = penaltyScore(candidate)
    if (p < bestPenalty) {
      bestPenalty = p
      best = candidate
    }
  }
  return { size, modules: best as boolean[][] }
}

// --- entry point --------------------------------------------------------

/** Encode UTF-8 text as a QR symbol (byte mode, EC level M). */
export function encodeQr(text: string): QrCode {
  const data = new TextEncoder().encode(text)
  const version = chooseVersion(data.length)
  const spec = VERSIONS[version - 1]
  const dataCodewords = dataCodewordCount(version - 1)

  // Bit stream: mode + count + payload + terminator + pad bytes.
  const bits: number[] = []
  const push = (val: number, len: number): void => {
    for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1)
  }
  push(0b0100, 4)
  push(data.length, version <= 9 ? 8 : 16)
  for (const b of data) push(b, 8)
  const capBits = dataCodewords * 8
  push(0, Math.min(4, capBits - bits.length))
  while (bits.length % 8 !== 0) bits.push(0)
  const padBytes = [0xec, 0x11]
  for (let i = 0; bits.length < capBits; i++) push(padBytes[i % 2], 8)

  const codewords: number[] = []
  for (let i = 0; i < bits.length; i += 8) {
    let b = 0
    for (let j = 0; j < 8; j++) b = (b << 1) | bits[i + j]
    codewords.push(b)
  }

  // Split into RS blocks, compute EC, interleave (data first, then EC).
  const blocksData: number[][] = []
  let off = 0
  for (const len of spec.blocks) {
    blocksData.push(codewords.slice(off, off + len))
    off += len
  }
  const blocksEc = blocksData.map((b) => rsRemainder(b, spec.ecPerBlock))
  const interleaved: number[] = []
  const maxData = Math.max(...spec.blocks)
  for (let i = 0; i < maxData; i++) {
    for (const b of blocksData) if (i < b.length) interleaved.push(b[i])
  }
  for (let i = 0; i < spec.ecPerBlock; i++) {
    for (const e of blocksEc) interleaved.push(e[i])
  }

  return buildMatrix(version, spec, interleaved)
}
