package telegram

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The v10 Torch ground rule for the Telegram bridge is that it never
// resizes the PTY (that would fight the TUI/GUI) and never writes to
// it — with a single exception: the explicit /say verb (task 0142),
// whose injection lives in exactly one function so it can be
// allowlisted precisely. Watch mode observes sessions through the
// SessionSource interface, which exposes snapshots and outcomes only.
//
// This test is the guard, in the style of the MCP server's stdio-only
// source check: it parses the telegram package's own source and fails
// on any reference to Resize, on any SendInput reference outside the
// one sanctioned /say call site, and on any import of the
// agent-manager package (whose Process would hand back the PTY).

// forbiddenSelectors are method/field names the telegram package must
// never reference — each one is a write path into a live session.
var forbiddenSelectors = map[string]string{
	"Resize": "resizing the PTY would fight an attached TUI/GUI",
}

// The single sanctioned PTY write: Bridge.injectSay in runcontrol.go —
// the /say path (task 0142). Exactly one SendInput reference must
// exist in the package, and it must be there.
const (
	sayFile = "runcontrol.go"
	sayFunc = "injectSay"
)

// forbiddenImports are packages that would hand the bridge a live
// Process (and with it the PTY). Session observation must go through
// the read-only SessionSource seam, and run control / input injection
// through the RunController seam. The backend subpackage (transcript
// discovery) is deliberately allowed.
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
	sayCallSites := 0
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

		// Walk per top-level declaration so SendInput references can be
		// attributed to their enclosing function.
		for _, decl := range file.Decls {
			funcName := ""
			if fd, ok := decl.(*ast.FuncDecl); ok {
				funcName = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if why, banned := forbiddenSelectors[sel.Sel.Name]; banned {
					t.Errorf("%s: references %s — %s", fset.Position(sel.Pos()), sel.Sel.Name, why)
				}
				if sel.Sel.Name == "SendInput" {
					if name == sayFile && funcName == sayFunc {
						sayCallSites++
					} else {
						t.Errorf("%s: references SendInput — PTY writes are reserved for the single /say path (%s in %s)",
							fset.Position(sel.Pos()), sayFunc, sayFile)
					}
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no source files checked — the guard would pass vacuously")
	}
	if sayCallSites != 1 {
		t.Errorf("expected exactly 1 sanctioned SendInput call site (%s in %s), found %d — the /say path must stay in one guarded function",
			sayFunc, sayFile, sayCallSites)
	}
}
