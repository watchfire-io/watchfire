package telegram

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The v10 Torch ground rule for the Telegram bridge is that it only
// ever READS from agent sessions: it must never resize the PTY (that
// would fight the TUI/GUI) and never write to it (input injection is
// the explicit /say path, task 0142 — and even that will live behind
// its own guarded seam). Watch mode observes sessions through the
// SessionSource interface, which exposes snapshots and outcomes only.
//
// This test is the guard, in the style of the MCP server's stdio-only
// source check: it parses the telegram package's own source and fails
// on any reference to Resize or SendInput, and on any import of the
// agent-manager package (whose Process would hand back the PTY).

// forbiddenSelectors are method/field names the telegram package must
// never reference — each one is a write path into a live session.
var forbiddenSelectors = map[string]string{
	"Resize":    "resizing the PTY would fight an attached TUI/GUI",
	"SendInput": "PTY writes are reserved for the explicit /say path (task 0142)",
}

// forbiddenImports are packages that would hand the bridge a live
// Process (and with it the PTY). Session observation must go through
// the read-only SessionSource seam instead. The backend subpackage
// (transcript discovery) is deliberately allowed.
var forbiddenImports = []string{
	"github.com/watchfire-io/watchfire/internal/daemon/agent",
}

func TestBridgeNeverWritesToThePTY(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, banned := range forbiddenImports {
				if path == banned {
					t.Errorf("%s: imports %s — the bridge must observe sessions through the read-only SessionSource seam, never the agent manager directly",
						fset.Position(imp.Pos()), path)
				}
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if why, banned := forbiddenSelectors[sel.Sel.Name]; banned {
				t.Errorf("%s: references %s — %s", fset.Position(sel.Pos()), sel.Sel.Name, why)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source files checked — the guard would pass vacuously")
	}
}
