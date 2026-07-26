package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v9.0 Firestorm's central safety claim is that the MCP server is local-only
// BY CONSTRUCTION, not by configuration: its sole transport is the stdio pipe
// the MCP client hands it, so there is no port to firewall and no address to
// misconfigure. That claim is only as good as the code, and it would be
// silently lost the day someone adds a "just for debugging" HTTP listener.
//
// These tests are the guard. They read the package's own source (plus the
// cobra command that drives it) and fail on any construct that could accept
// an inbound connection. The e2e test checks the same property from the
// outside, by inspecting the running process's sockets; this one runs on
// every `go test ./...` and needs no daemon.

// listenerCalls are the function calls that create a listening socket.
var listenerCalls = map[string][]string{
	"net":       {"Listen", "ListenTCP", "ListenUDP", "ListenUnix", "ListenPacket", "ListenIP"},
	"http":      {"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS"},
	"tls":       {"Listen", "NewListener"},
	"grpc":      {"NewServer"},
	"quic":      {"Listen", "ListenAddr"},
	"websocket": {"Handler"},
}

// networkTransports are MCP transports that speak over the network rather
// than over the pipe. Constructing one here would make the server reachable.
var networkTransports = []string{
	"StreamableHTTPHandler",
	"StreamableClientTransport",
	"SSEHandler",
	"SSEServerTransport",
	"NewStreamableServerTransport",
}

// sourceFiles returns the non-test Go files that make up the MCP server: this
// package, its install helpers, and the cobra command that invokes Serve.
func sourceFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{".", "install", filepath.Join("..", "..", "cmd", "watchfire")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			// cmd/watchfire holds the whole CLI; only its MCP command is
			// in scope here.
			if dir != "." && dir != "install" && name != "mcp.go" {
				continue
			}
			files = append(files, filepath.Join(dir, name))
		}
	}
	if len(files) == 0 {
		t.Fatal("no source files found — the guard would pass vacuously")
	}
	return files
}

func TestNoListeningSocketInServePath(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range sourceFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			for _, banned := range listenerCalls[pkg.Name] {
				if sel.Sel.Name == banned {
					t.Errorf("%s: calls %s.%s — the MCP server must never open a listening socket "+
						"(stdio is its only transport; see ARCHITECTURE.md \"Local-only, by construction\")",
						fset.Position(call.Pos()), pkg.Name, banned)
				}
			}
			return true
		})
	}
}

func TestNoNetworkMcpTransport(t *testing.T) {
	for _, path := range sourceFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, name := range networkTransports {
			if strings.Contains(string(raw), name) {
				t.Errorf("%s: references %s — v9.0 serves MCP over stdio only; "+
					"HTTP/SSE transports are explicitly out of scope (ARCHITECTURE.md \"Excluded\")",
					path, name)
			}
		}
	}
}

// TestServeUsesStdioTransport pins the positive half of the claim: Serve
// hands mcp.Server.Run a transport built from os.Stdin/os.Stdout and nothing
// else. Without this, the two negative tests above would still pass if the
// transport were swapped for something that dialled out.
func TestServeUsesStdioTransport(t *testing.T) {
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	src := string(raw)
	for _, want := range []string{
		"Reader: &eofRecordingReader{r: os.Stdin",
		"Writer: nopWriteCloser{os.Stdout}",
		"m.Run(ctx, transport)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("server.go no longer wires the stdio transport as expected: missing %q", want)
		}
	}
}
