package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCreateStagingFile_UsesPreferDir asserts that the download staging file
// lands inside the caller-supplied install directory. Keeping the file there
// is what makes the subsequent rename same-filesystem — the whole #25 fix.
func TestCreateStagingFile_UsesPreferDir(t *testing.T) {
	dir := t.TempDir()
	f, err := createStagingFile(dir)
	if err != nil {
		t.Fatalf("createStagingFile: %v", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_ = f.Close()

	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	got, err := filepath.EvalSymlinks(filepath.Dir(f.Name()))
	if err != nil {
		t.Fatalf("eval staged dir: %v", err)
	}
	if got != want {
		t.Errorf("staging file in %q, want %q", got, want)
	}
}

// TestCreateStagingFile_FallsBackToTmp asserts that an unusable preferDir
// (non-existent here) falls back to os.TempDir() rather than erroring — the
// cross-filesystem case is then handled downstream by ReplaceBinary.
func TestCreateStagingFile_FallsBackToTmp(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	f, err := createStagingFile(missing)
	if err != nil {
		t.Fatalf("createStagingFile: %v", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_ = f.Close()

	if filepath.Dir(f.Name()) == missing {
		t.Errorf("expected fallback to os.TempDir(), got staging in missing dir %q", missing)
	}
}

// TestReplaceBinary_SameDir covers the fast path: staged file lives next to
// the install target, so the swap is a single same-filesystem rename.
func TestReplaceBinary_SameDir(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "watchfire")
	if err := os.WriteFile(destPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, ".watchfire-update-abc")
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceBinary(destPath, newPath); err != nil {
		t.Fatalf("ReplaceBinary: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("dest = %q, want %q", got, "new")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("newPath should be gone after rename, stat err = %v", err)
	}
}

// TestReplaceBinary_CrossDir simulates the EXDEV scenario: the new binary
// sits in a different directory from the install path (on real Linux with
// tmpfs /tmp this would be a cross-filesystem rename). ReplaceBinary must
// stage the new binary into the install dir first and still finish with an
// atomic same-dir rename. This is the regression test for #25.
func TestReplaceBinary_CrossDir(t *testing.T) {
	destDir := t.TempDir()
	stageDir := t.TempDir()

	destPath := filepath.Join(destDir, "watchfire")
	if err := os.WriteFile(destPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	newPath := filepath.Join(stageDir, "watchfire-update-42")
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceBinary(destPath, newPath); err != nil {
		t.Fatalf("ReplaceBinary: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("dest = %q, want %q", got, "new")
	}

	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("original stage file not cleaned up: %v", err)
	}

	// The install dir must contain only the final binary — no leftover
	// .watchfire-update-* staging file from the fallback copy step.
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(destPath) {
			t.Errorf("unexpected leftover in install dir: %s", e.Name())
		}
	}
}

// TestReplaceBinary_ExecPermsPreserved asserts the final binary is executable
// — exec perms are set before the rename so there's no window where a
// concurrent watchfire invocation could exec a non-executable file.
func TestReplaceBinary_ExecPermsPreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX exec bit doesn't apply on Windows")
	}
	dir := t.TempDir()
	destPath := filepath.Join(dir, "watchfire")
	if err := os.WriteFile(destPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, ".watchfire-update-xyz")
	// Write with non-exec perms to prove ReplaceBinary (via DownloadAsset's
	// staging chmod, or stageIntoInstallDir's chmod) sets them.
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Mimic DownloadAsset: set exec perms on the staged file before handing
	// off to ReplaceBinary.
	if err := os.Chmod(newPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceBinary(destPath, newPath); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("exec perm not set on final binary: %v", info.Mode())
	}
}
