package mcpserver

import (
	"google.golang.org/grpc"

	"github.com/watchfire-io/watchfire/internal/cli"
)

// ensureDaemonConn makes sure the daemon is running (auto-starting it if
// necessary, exactly like the CLI) and returns a gRPC connection to it.
func ensureDaemonConn() (*grpc.ClientConn, error) {
	if err := cli.EnsureDaemon(); err != nil {
		return nil, err
	}
	return cli.ConnectDaemon()
}
