// Resolve the user's login-shell environment so processes spawned by the
// Electron main (the in-app terminal PTY, the daemon binary, future helper
// processes) inherit the same PATH and tool-related env vars the user gets
// in their native terminal.
//
// macOS / Linux GUI apps inherit a minimal `PATH=/usr/bin:/bin:/usr/sbin:/sbin`
// and don't read shell rc files (`~/.zshrc`, `~/.bashrc`), so user-installed
// tooling like pnpm, volta, fnm, bun, cargo, rustup, etc. is invisible to a
// process forked from the GUI. The v2.0.0 Spark fix patched the daemon spawn
// path; the in-app terminal (issue #32) regressed to the same broken state
// because `pty-manager.ts` forked the user's shell with a raw
// `{ ...process.env }`. This helper is the single place both spawn paths now
// run through.
//
// Caching: the login-shell exec is non-trivial (it loads the user's full
// rc file) — running it on every PTY spawn would add 100s-of-ms per shell
// open. We cache the result for the lifetime of the Electron process; the
// user has to relaunch the app to pick up `~/.zshrc` edits, which matches
// how every other GUI terminal app on macOS behaves.

import { execFile } from 'child_process'
import { homedir, platform } from 'os'
import { join } from 'path'

// Env vars we explicitly pluck from the login-shell output. PATH is the
// load-bearing one (most user tools live on it); the others are dev-tool
// roots that some CLIs check directly without consulting PATH first
// (notably nvm's auto-shim, Volta's per-project pinning, and pnpm's own
// `pnpm env use` flow).
const RELEVANT_ENV_VARS = [
  'PATH',
  'NVM_DIR',
  'VOLTA_HOME',
  'PNPM_HOME',
  'BUN_INSTALL',
  'DENO_INSTALL',
  'CARGO_HOME',
  'GOPATH',
  'JAVA_HOME',
  'LANG',
  'LC_ALL'
]

// Hardcoded fallback paths used when login-shell detection times out or the
// shell exits non-zero. These cover the most common user-installed tool
// locations across macOS and Linux distros — they're a strict superset of
// what the v2.0.0 Spark fix ships in the daemon-side helper.
function fallbackPaths(): string[] {
  const home = homedir()
  return [
    join(home, '.local', 'bin'),
    join(home, '.npm-global', 'bin'),
    '/opt/homebrew/bin',
    '/usr/local/bin',
    join(home, '.cargo', 'bin'),
    join(home, '.bun', 'bin')
  ]
}

// Build a PATH string that prepends any fallback dir not already present in
// `currentPath`. Same dir is never duplicated even if the input PATH already
// contains it — order is preserved so user customisation wins over fallbacks.
export function mergeFallbackPaths(currentPath: string, fallbacks: string[] = fallbackPaths()): string {
  const sep = platform() === 'win32' ? ';' : ':'
  const seen = new Set(currentPath ? currentPath.split(sep) : [])
  const additions: string[] = []
  for (const p of fallbacks) {
    if (!seen.has(p)) {
      additions.push(p)
      seen.add(p)
    }
  }
  if (additions.length === 0) return currentPath
  return currentPath ? `${additions.join(sep)}${sep}${currentPath}` : additions.join(sep)
}

// Parse the output of `env` (or `printenv`) into a key/value map. Lines
// without `=` are dropped (some shells emit a trailing banner / colour
// reset); values containing `=` are preserved verbatim.
export function parseEnvOutput(out: string): Record<string, string> {
  const env: Record<string, string> = {}
  for (const line of out.split('\n')) {
    const eq = line.indexOf('=')
    if (eq <= 0) continue
    const key = line.slice(0, eq)
    const val = line.slice(eq + 1)
    env[key] = val
  }
  return env
}

// Pluck only the env vars we know the consumer cares about, plus PATH-merge
// the hardcoded fallbacks so a user with a partial rc file still gets the
// common tool dirs.
export function selectRelevantEnv(parsed: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const k of RELEVANT_ENV_VARS) {
    if (parsed[k] !== undefined) out[k] = parsed[k]
  }
  out.PATH = mergeFallbackPaths(out.PATH || process.env.PATH || '')
  return out
}

// execFile-based runner factored out so the test can mock it without pulling
// in real `child_process`. Resolves with the stdout string on success;
// rejects on non-zero exit or timeout.
export type ExecRunner = (
  cmd: string,
  args: string[],
  opts: { timeout: number }
) => Promise<string>

const defaultRunner: ExecRunner = (cmd, args, opts) =>
  new Promise((resolve, reject) => {
    execFile(cmd, args, { timeout: opts.timeout, encoding: 'utf-8' }, (err, stdout) => {
      if (err) {
        reject(err)
        return
      }
      resolve(stdout)
    })
  })

// resolveLoginShellEnv is the lower-level builder — exported for the unit
// test so it can pass a stub runner. The cached `loginShellEnv` below is
// what production code uses.
export async function resolveLoginShellEnv(
  options: { runner?: ExecRunner; timeoutMs?: number } = {}
): Promise<Record<string, string>> {
  const runner = options.runner || defaultRunner
  const timeoutMs = options.timeoutMs ?? 3000

  // Windows: skip the login-shell dance entirely. cmd.exe / pwsh don't have
  // the same minimal-env problem, and the rc-file model is different enough
  // that re-using the macOS/Linux logic does more harm than good.
  if (platform() === 'win32') {
    return processEnvCopy()
  }

  const shell = process.env.SHELL || '/bin/zsh'
  // bash needs `-l -c env` (no -i, otherwise it can hang waiting on a
  // controlling tty); zsh tolerates `-l -i -c env` but `-l -c env` is also
  // sufficient and avoids the interactive flag pulling in oh-my-zsh's
  // greeting / completion that can be slow. We match the semantics the
  // v2.0.0 Spark daemon-side helper used.
  const args = ['-l', '-c', 'env']

  try {
    const stdout = await runner(shell, args, { timeout: timeoutMs })
    const parsed = parseEnvOutput(stdout)
    if (!parsed.PATH) {
      // Empty / malformed output — treat as failure and fall through to the
      // fallback path below.
      throw new Error('login shell produced no PATH')
    }
    return { ...processEnvCopy(), ...selectRelevantEnv(parsed) }
  } catch {
    // Timeout, non-zero exit, or unparseable output: degrade gracefully.
    // We still want pnpm / volta / etc. to be visible if they're installed
    // in the conventional locations, so PATH-merge the hardcoded fallbacks
    // onto whatever Electron's process.env happens to have.
    const merged = processEnvCopy()
    merged.PATH = mergeFallbackPaths(merged.PATH || '')
    return merged
  }
}

// process.env's typescript signature is `{ [k: string]: string | undefined }`,
// which doesn't fit our `Record<string, string>` contract — Node never
// actually returns `undefined` for an enumerated key, so we drop the
// undefined branch with a single typed copy here rather than asserting at
// every call site.
function processEnvCopy(): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(process.env)) {
    if (v !== undefined) out[k] = v
  }
  return out
}

let cached: Promise<Record<string, string>> | null = null

// loginShellEnv resolves the user's login-shell env once per Electron
// process and returns the cached result on every subsequent call. Callers
// must `await` the return value — the first call pays the full login-shell
// exec cost (typically 50-300 ms), subsequent calls resolve synchronously
// from the resolved promise.
export function loginShellEnv(): Promise<Record<string, string>> {
  if (cached === null) {
    cached = resolveLoginShellEnv()
  }
  return cached
}

// Test hook: clear the cached promise so a fresh call re-runs the exec.
// Not used in production code paths.
export function _resetLoginShellEnvCache(): void {
  cached = null
}
