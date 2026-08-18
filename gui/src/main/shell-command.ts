// Build the command used to spawn the integrated terminal's shell
// (issue #32).
//
// macOS/Linux: the shell is spawned as a LOGIN shell (`-l`) so the
// profile-level startup files run — /etc/zprofile → path_helper → /etc/paths
// on macOS, /etc/profile + ~/.profile / ~/.zprofile elsewhere — and the
// terminal's PATH matches a native terminal tab. This complements
// login-shell.ts: that helper *captures* a login env once per app launch for
// the spawn environment, while the `-l` flag makes the terminal's own shell
// process re-run the profile chain itself, so PATH edits made after app
// launch (and shells other than $SHELL picked via the Terminal-shell
// setting) resolve correctly too.
//
// Windows has no login-shell concept — the pre-#32 spawn (no flag) is kept
// verbatim.
//
// Mirrored in shell-command.test.mjs (node --test, no TS toolchain) — keep
// the two in sync.

export interface ShellCommand {
  file: string
  args: string[]
}

// Resolve which shell binary to spawn and with what flags. Precedence:
// the validated `defaults.terminal_shell` setting, then $SHELL, then a
// per-platform default (/bin/zsh on macOS — the OS default since Catalina —
// /bin/bash elsewhere). All POSIX shells in practical use (zsh, bash, fish,
// dash, nu, pwsh) accept `-l`.
export function resolveShellCommand(
  configuredShell: string | null,
  envShell: string | undefined,
  plat: NodeJS.Platform
): ShellCommand {
  if (plat === 'win32') {
    return { file: configuredShell || envShell || '/bin/zsh', args: [] }
  }
  const fallback = plat === 'darwin' ? '/bin/zsh' : '/bin/bash'
  return { file: configuredShell || envShell || fallback, args: ['-l'] }
}
