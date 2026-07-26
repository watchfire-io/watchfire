package mcpserver

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRpcErrStripsGrpcEnvelope — the daemon's own message is specific and
// useful; the transport framing around it is not, and it costs the reader
// (a model) a line of noise on every failure.
func TestRpcErrStripsGrpcEnvelope(t *testing.T) {
	err := rpcErr("get task 42", status.Error(codes.NotFound, "task not found: 42"))

	got := err.Error()
	if !strings.Contains(got, "task not found: 42") {
		t.Errorf("rpcErr dropped the daemon's message: %q", got)
	}
	if !strings.Contains(got, "get task 42") {
		t.Errorf("rpcErr dropped the operation: %q", got)
	}
	for _, noise := range []string{"rpc error", "code = ", "desc = "} {
		if strings.Contains(got, noise) {
			t.Errorf("rpcErr leaks the gRPC envelope (%q): %q", noise, got)
		}
	}
}

// TestRpcErrNamesADeadDaemon — a caller can fix a bad argument by retrying
// with different input, but a dead daemon needs the daemon back. The two
// must not read the same.
func TestRpcErrNamesADeadDaemon(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded, codes.Canceled} {
		got := rpcErr("list projects", status.Error(code, "connection refused")).Error()
		for _, want := range []string{"daemon is not reachable", "watchfire daemon start", "list projects"} {
			if !strings.Contains(got, want) {
				t.Errorf("code %v: error does not mention %q: %q", code, want, got)
			}
		}
	}
}

func TestRpcErrPassesThroughNonStatusErrors(t *testing.T) {
	sentinel := errors.New("boom")
	err := rpcErr("do a thing", sentinel)
	if !errors.Is(err, sentinel) {
		t.Errorf("rpcErr broke the error chain: %v", err)
	}
	if !strings.Contains(err.Error(), "do a thing") {
		t.Errorf("rpcErr dropped the operation: %v", err)
	}
	if rpcErr("do a thing", nil) != nil {
		t.Error("rpcErr(nil) should be nil")
	}
}

// TestStartupErrExplainsTheDependency — this one is returned before the MCP
// session exists, so the client only ever sees "the server process exited".
// Whatever it says has to be enough on its own.
func TestStartupErrExplainsTheDependency(t *testing.T) {
	got := startupErr(fmt.Errorf("watchfired not found")).Error()
	for _, want := range []string{
		"daemon could not be started or reached",
		"watchfired",
		"watchfire daemon status",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("startupErr does not mention %q: %q", want, got)
		}
	}
}
