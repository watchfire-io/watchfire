package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFind_VisualWinsOverEditor verifies the documented precedence:
// $VISUAL beats $EDITOR even when both are set.
func TestFind_VisualWinsOverEditor(t *testing.T) {
	t.Setenv("VISUAL", "/usr/bin/visual-editor")
	t.Setenv("EDITOR", "/usr/bin/regular-editor")

	got := Find()
	if got != "/usr/bin/visual-editor" {
		t.Fatalf("Find() = %q, want /usr/bin/visual-editor (VISUAL must beat EDITOR)", got)
	}
}

// TestFind_EditorFallback verifies $EDITOR is consulted when $VISUAL
// is unset.
func TestFind_EditorFallback(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "/usr/bin/my-editor")

	got := Find()
	if got != "/usr/bin/my-editor" {
		t.Fatalf("Find() = %q, want /usr/bin/my-editor", got)
	}
}

// TestFind_PathFallback verifies the vim/vi PATH fallback fires when
// neither $VISUAL nor $EDITOR is set. We only assert that the result
// is non-empty and resolves to vim or vi — most CI machines have at
// least one. If neither is on PATH the test skips rather than fails.
func TestFind_PathFallback(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	got := Find()
	if got == "" {
		t.Skip("neither vim nor vi on PATH — cannot exercise fallback")
	}
	base := filepath.Base(got)
	if base != "vim" && base != "vi" {
		t.Fatalf("Find() = %q, expected fallback to vim or vi", got)
	}
}

// TestWriteTempFile_RoundTrip verifies WriteTempFile creates a
// readable file with the supplied content and a label-suffixed name
// so editors with filetype detection see the right extension.
func TestWriteTempFile_RoundTrip(t *testing.T) {
	const label = "definition.md"
	const content = "# Hello\n\nDefinition body."

	path, err := WriteTempFile(label, content)
	if err != nil {
		t.Fatalf("WriteTempFile error: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if !strings.HasSuffix(path, label) {
		t.Fatalf("path = %q, want suffix %q (label drives extension for vim filetype detection)", path, label)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tempfile: %v", err)
	}
	if string(got) != content {
		t.Fatalf("tempfile content = %q, want %q", string(got), content)
	}
}

// TestReadTempFile_RemovesFile verifies ReadTempFile reads the
// content AND deletes the file — leaving tempfiles behind across an
// editor session is the failure mode that prompted the helper.
func TestReadTempFile_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "post-edit.md")
	if err := os.WriteFile(path, []byte("edited content"), 0o600); err != nil {
		t.Fatalf("seed tempfile: %v", err)
	}

	got, err := ReadTempFile(path)
	if err != nil {
		t.Fatalf("ReadTempFile error: %v", err)
	}
	if got != "edited content" {
		t.Fatalf("content = %q, want %q", got, "edited content")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("tempfile still exists after ReadTempFile: stat err = %v", err)
	}
}

// TestReadTempFile_MissingFile verifies a missing tempfile surfaces
// as an error instead of returning empty string silently.
func TestReadTempFile_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.md")

	_, err := ReadTempFile(path)
	if err == nil {
		t.Fatal("ReadTempFile on missing file = nil error, want non-nil")
	}
}

// TestShouldSave covers the save/no-save decision. This is the helper
// the task spec calls out as the unit-test boundary — input old +
// new content, output should-save bool. Trivial logic, but pulling
// it into a named function gives the TUI Update handler and CLI
// runDefine a single shared decision point.
func TestShouldSave(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{"identical", "abc", "abc", false},
		{"empty unchanged", "", "", false},
		{"text added", "abc", "abc def", true},
		{"text removed", "abc def", "abc", true},
		{"trailing newline counts as change", "abc", "abc\n", true},
		{"empty to non-empty", "", "anything", true},
		{"non-empty to empty", "anything", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSave(tt.old, tt.new); got != tt.want {
				t.Fatalf("ShouldSave(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}

// TestEditorRoundTrip_NoEditorRun simulates the pre/post-edit halves
// of the helper end-to-end without actually launching an editor —
// matches the task's "build the command + read the result tempfile +
// decide whether to save" boundary. Inputs: old content + new
// content. Outputs: should-save bool + new content reachable through
// the same code path the CLI and TUI use.
func TestEditorRoundTrip_NoEditorRun(t *testing.T) {
	const oldContent = "before"
	const newContent = "after"

	path, err := WriteTempFile("definition.md", oldContent)
	if err != nil {
		t.Fatalf("WriteTempFile: %v", err)
	}
	if err := os.WriteFile(path, []byte(newContent), 0o600); err != nil {
		t.Fatalf("simulate editor write: %v", err)
	}

	got, err := ReadTempFile(path)
	if err != nil {
		t.Fatalf("ReadTempFile: %v", err)
	}
	if got != newContent {
		t.Fatalf("post-read content = %q, want %q", got, newContent)
	}
	if !ShouldSave(oldContent, got) {
		t.Fatalf("ShouldSave(%q, %q) = false, want true", oldContent, got)
	}
}

// TestEditorRoundTrip_NoChangesSkipsSave verifies the no-op path —
// when the user opens the editor and exits without changes, the
// helper reports should-save = false, so neither CLI nor TUI fires
// a write.
func TestEditorRoundTrip_NoChangesSkipsSave(t *testing.T) {
	const content = "definition body"

	path, err := WriteTempFile("definition.md", content)
	if err != nil {
		t.Fatalf("WriteTempFile: %v", err)
	}

	got, err := ReadTempFile(path)
	if err != nil {
		t.Fatalf("ReadTempFile: %v", err)
	}
	if got != content {
		t.Fatalf("post-read content = %q, want %q", got, content)
	}
	if ShouldSave(content, got) {
		t.Fatalf("ShouldSave on unchanged content = true, want false")
	}
}
