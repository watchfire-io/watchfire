package cli

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/watchfire-io/watchfire/internal/config"
)

// ConnectDaemon establishes a gRPC connection to the running daemon.
// Exported so other thin clients (e.g. internal/mcpserver) can reuse the
// CLI's connection logic instead of duplicating it.
func ConnectDaemon() (*grpc.ClientConn, error) {
	info, err := config.LoadDaemonInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to load daemon info: %w", err)
	}
	if info == nil {
		return nil, fmt.Errorf("daemon not running")
	}

	addr := fmt.Sprintf("%s:%d", info.Host, info.Port)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	return conn, nil
}
