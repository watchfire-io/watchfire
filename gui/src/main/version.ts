// Pure helpers for parsing and comparing CLI version strings.
//
// Extracted from cli-installer.ts so the version-match logic can be reasoned
// about (and tested) without Electron in the loop. Issue #30: on Linux the
// GUI asked to update the CLI on every launch because the strict `!==`
// comparison was tripping on trailing whitespace, ANSI escapes that leaked
// through the old CSI-only stripper, and pre-release suffixes. The helpers
// below normalise both sides before comparing.

/**
 * Strip ANSI escape sequences from a string.
 *
 * Covers the common emissions from lipgloss / termenv:
 *   - CSI sequences (colors, cursor movement): `\x1b[...m`, `\x1b[K`, etc.
 *   - OSC sequences (hyperlinks, titles): `\x1b]8;;URL\x07` (BEL or ST terminated)
 *   - Solo ESC two-byte sequences: `\x1b(B`, `\x1b>`, etc.
 *
 * The previous regex only handled CSI, which was enough for the stdout of
 * `watchfire version` in most environments but left a gap on Linux where the
 * GUI's inherited env sometimes triggered hyperlink/OSC emission.
 */
export function stripAnsi(s: string): string {
  return (
    s
      // CSI: ESC [ ... final-byte (any letter)
      .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
      // OSC: ESC ] ... (BEL | ESC \)
      .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
      // Other 2-byte ESC sequences (charset shifts, etc.)
      .replace(/\x1b[@-Z\\-_]/g, '')
  )
}

/**
 * Extract the semver-like version substring from the first line of
 * `watchfire version` output.
 *
 * The CLI prints (styled with lipgloss, possibly with ANSI):
 *     Watchfire 2.0.1 (Spark)
 *        Commit   ...
 *
 * Returns the matched version as a plain string (e.g. "2.0.1", "2.0.1-rc.1")
 * or null if the output didn't contain a recognisable version.
 */
export function parseCLIVersion(rawOutput: string): string | null {
  const clean = stripAnsi(rawOutput)
  // Allow optional leading "v", the core X.Y.Z triple, and any semver suffix
  // (pre-release / build metadata) so dev tags like "2.0.1-rc.1" still parse.
  const match = clean.match(/Watchfire\s+v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)/)
  return match ? match[1] : null
}

type SemverTuple = [number, number, number, string]

/**
 * Parse a version string into a [major, minor, patch, preRelease] tuple.
 * Trims whitespace, drops a leading "v", drops build metadata (+suffix), and
 * keeps the pre-release component separately for ordering.
 */
function parseSemver(v: string): SemverTuple | null {
  const m = v.trim().match(/^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/)
  if (!m) return null
  return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] ?? '']
}

/**
 * Compare two semver strings.
 *
 * Returns -1 if `a < b`, 0 if equal, 1 if `a > b`, or null if either side
 * couldn't be parsed as semver. Callers should treat null as "unknown —
 * don't prompt" rather than "outdated" so that non-release builds (which
 * emit "dev" or similar) don't spam the update dialog.
 *
 * Pre-release ordering follows SemVer 2.0.0 §11: a version without a
 * pre-release tag outranks one with a pre-release tag (so 2.0.1 > 2.0.1-rc.1).
 * Full semver pre-release precedence isn't needed here — we only care about
 * catching the user running a release build against a matching release GUI.
 */
export function compareSemver(a: string, b: string): number | null {
  const pa = parseSemver(a)
  const pb = parseSemver(b)
  if (!pa || !pb) return null
  for (let i = 0; i < 3; i++) {
    if (pa[i] !== pb[i]) return (pa[i] as number) < (pb[i] as number) ? -1 : 1
  }
  const prA = pa[3]
  const prB = pb[3]
  if (prA === prB) return 0
  if (prA === '') return 1
  if (prB === '') return -1
  return prA < prB ? -1 : 1
}
