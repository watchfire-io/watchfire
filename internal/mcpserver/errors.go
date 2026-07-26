package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Tool errors are read by a language model, not a human tailing a log, so
// they must say what failed AND what to do next. Two rules apply everywhere
// in this package:
//
//   - Never leak a raw gRPC string. "rpc error: code = NotFound desc = task
//     not found: 42" is noise; "failed to get task 42: task not found: 42"
//     is the same information without the envelope.
//   - A dead daemon is a different failure from a bad argument. The caller
//     can fix a bad argument by retrying with different input; a dead daemon
//     needs the daemon back. Say which one happened.

// daemonUnreachableHint is appended whenever the daemon connection itself
// broke mid-session. The MCP server auto-starts the daemon at startup, so
// reaching this state means it died afterwards.
const daemonUnreachableHint = "the Watchfire daemon is not reachable — it was running when this MCP session started, so it has since stopped or crashed. Restart it with `watchfire daemon start` (or check `watchfire daemon status`), then retry"

// rpcErr converts a failed daemon RPC into an actionable tool error.
// what describes the attempted operation in the imperative ("get task 42").
func rpcErr(what string, err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("failed to %s: %w", what, err)
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return fmt.Errorf("failed to %s: %s (%s)", what, daemonUnreachableHint, st.Message())
	default:
		// The daemon's own message is already specific ("task not found:
		// 42", "no ready tasks found for start-all mode") — pass it
		// through without the gRPC envelope.
		return fmt.Errorf("failed to %s: %s", what, st.Message())
	}
}

// startupErr wraps a failure to bring up the daemon connection before the
// MCP session even begins. It is returned from Serve, so the MCP client sees
// it as the server process failing to start — the message has to explain
// what the user must install or run.
func startupErr(err error) error {
	return fmt.Errorf("the Watchfire daemon could not be started or reached: %w.\n"+
		"The MCP server is a thin client — it needs a local `watchfired`. Make sure Watchfire is "+
		"installed (both `watchfire` and `watchfired` on PATH, e.g. via the Watchfire app or "+
		"`make install`), then verify with `watchfire daemon status`", err)
}

// isCleanShutdown reports whether a server-run error is the ordinary end of a
// stdio session rather than a fault: the MCP client closed the pipe (EOF) or
// the process context was cancelled (SIGINT/SIGTERM). Neither should exit
// nonzero — clients treat that as a crashed server.
func isCleanShutdown(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}
