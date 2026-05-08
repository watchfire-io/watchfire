// Package editor exposes the shared $EDITOR shellout primitives used by
// the CLI's `watchfire define` command and the TUI's Definition tab.
// The two surfaces drive the editor differently — the CLI runs it as
// a foreground child, the TUI wraps it in tea.ExecProcess so Bubble
// Tea suspends cleanly — but both pick the editor binary, allocate
// the tempfile, and decide whether to save through this package, so
// the precedence rules and cleanup behaviour stay consistent.
//
// Editor selection precedence: $VISUAL → $EDITOR → vim → vi.
//
// The package lives under internal/editor (not internal/cli) because
// internal/cli already imports internal/tui (root.go calls tui.Run);
// putting the helpers here lets both packages consume them without
// introducing an import cycle.
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Find returns the path to the user's preferred external editor using
// the precedence $VISUAL → $EDITOR → vim → vi. Returns "" if no
// editor is configured and neither vim nor vi is on PATH.
func Find() string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	for _, name := range []string{"vim", "vi"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// WriteTempFile creates a tempfile under os.TempDir() seeded with
// content. label is mixed into the filename so editors with filetype
// detection (vim, etc.) pick the right syntax — e.g. label
// "definition.md" yields a file ending in `.md`. Returns the absolute
// path; the caller is responsible for cleanup (typically via
// ReadTempFile, which removes the file as part of the read).
func WriteTempFile(label, content string) (string, error) {
	tmpFile := filepath.Join(os.TempDir(), "watchfire-"+label)
	if err := os.WriteFile(tmpFile, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	return tmpFile, nil
}

// ReadTempFile reads the post-editor content at path and removes the
// file. The remove is best-effort and runs even if the read fails, so
// the tempfile never lingers across an editor error.
func ReadTempFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return "", fmt.Errorf("failed to read edited content: %w", err)
	}
	return string(data), nil
}

// ShouldSave reports whether the post-editor content differs from the
// original — the CLI flow saves only when this returns true and the
// TUI flow dispatches an UpdateProject RPC only when this returns
// true. Pulled out as a named function so the save/no-save decision
// has a single explicit code path that's trivial to unit-test.
func ShouldSave(oldContent, newContent string) bool {
	return oldContent != newContent
}
