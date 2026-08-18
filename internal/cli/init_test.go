package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunInitRejectsSandboxDeniedCwd — `watchfire init` inside a
// sandbox-denied root (#17) fails fast with the actionable message, before
// any prompt is shown (the check precedes all stdin reads, so this runs
// without interactive input).
func TestRunInitRejectsSandboxDeniedCwd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("privacy-root deny list is darwin-only")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	denied := filepath.Join(home, "Desktop", "proj")
	if err := os.MkdirAll(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(denied)

	initErr := runInit(initCmd, nil)
	if initErr == nil {
		t.Fatal("watchfire init under ~/Desktop succeeded, want rejection")
	}
	if !strings.Contains(initErr.Error(), "~/Desktop") || !strings.Contains(initErr.Error(), "sandbox blocks") {
		t.Errorf("error %q must name ~/Desktop and the sandbox rule", initErr)
	}
}
