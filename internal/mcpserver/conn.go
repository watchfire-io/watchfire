package mcpserver

import (
	"google.golang.org/grpc"

	"github.com/watchfire-io/watchfire/internal/cli"
)

// ensureDaemonConn makes sure the daemon is running (auto-starting it if
// necessary, exactly like the CLI) and returns a gRPC connection to it.
//
// Both failure modes go through startupErr: the MCP client only ever sees
// this as "the server process died during startup", so the message has to
// name the missing piece itself rather than assume anyone reads stderr.
func ensureDaemonConn() (*grpc.ClientConn, error) {
	if err := cli.EnsureDaemon(); err != nil {
		return nil, startupErr(err)
	}
	conn, err := cli.ConnectDaemon()
	if err != nil {
		return nil, startupErr(err)
	}
	return conn, nil
}
