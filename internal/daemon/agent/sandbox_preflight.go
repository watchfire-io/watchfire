package agent

// Sandbox preflight — the user-facing consequence of the sandbox deny list.
//
// This file is the single source of truth for which directory roots a
// project cannot live under (issue #17: a project under ~/Desktop failed
// with an "unexpected error" instead of naming the sandbox rule). It lives
// next to the sandbox profile generators on purpose: the per-platform
// deniedProjectRoots() implementations are derived from the very slices the
// profiles render (protectedUserDirs, credentialDenyDirs), so this check
// and the enforced policy cannot drift. Do NOT hardcode paths here — extend
// the shared slices instead.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DeniedRoot is one directory subtree the sandbox denies agents access to,
// making it impossible for a project inside it to function.
type DeniedRoot struct {
	Path    string // absolute path, e.g. /Users/x/Desktop
	Display string // user-facing form, e.g. "~/Desktop"
	Reason  string // short human-readable reason for the denial
}

// PathDenial is the typed, human-readable error returned when a project
// path falls inside a denied root. Its Error() text is the exact message
// every surface (CLI, gRPC, GUI wizard, TUI, agent-issue banner) shows.
type PathDenial struct {
	Root DeniedRoot // the denied root the path falls under
	Path string     // the offending project path as given
}

// Error implements error with the actionable user-facing message.
func (d *PathDenial) Error() string {
	return fmt.Sprintf(
		"This folder is inside %s, which Watchfire's sandbox blocks for agent access (%s). "+
			"Move the project (e.g. under ~/source) or choose another folder.",
		d.Root.Display, d.Root.Reason)
}

// CheckProjectPath reports whether a project located at path could function
// under the sandbox. It returns nil when the path is fine, or a *PathDenial
// naming the denied root otherwise. Symlinks are resolved best-effort so a
// link pointing into a denied root is caught too.
func CheckProjectPath(homeDir, path string) *PathDenial {
	if homeDir == "" || path == "" {
		return nil
	}
	roots := deniedProjectRoots(resolveBestEffort(cleanAbs(homeDir)))
	if len(roots) == 0 {
		return nil
	}

	// Check both the literal path and its symlink-resolved form.
	given := cleanAbs(path)
	candidates := []string{given}
	if resolved := resolveBestEffort(given); resolved != given {
		candidates = append(candidates, resolved)
	}

	for _, root := range roots {
		rootPaths := []string{root.Path}
		if resolved := resolveBestEffort(root.Path); resolved != root.Path {
			rootPaths = append(rootPaths, resolved)
		}
		for _, p := range candidates {
			for _, r := range rootPaths {
				if isWithin(p, r) {
					return &PathDenial{Root: root, Path: path}
				}
			}
		}
	}
	return nil
}

// cleanAbs returns the cleaned absolute form of p (best-effort — on Abs
// failure the cleaned relative path is returned).
func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// resolveBestEffort resolves symlinks in p, returning p unchanged when
// resolution fails (e.g. the path does not exist yet).
func resolveBestEffort(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// isWithin reports whether path equals root or is nested under it. The
// separator-suffixed prefix check means sibling names that merely contain
// the root name (e.g. ~/DesktopApps vs ~/Desktop) never match.
func isWithin(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
