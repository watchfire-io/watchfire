// Package server implements the gRPC server for the daemon.
package server

import (
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
)

// Server is the daemon's gRPC server.
type Server struct {
	grpcServer     *grpc.Server
	listener       net.Listener
	port           int
	projectManager *project.Manager
	taskManager    *task.Manager
}

// New creates a new server listening on the specified port.
// Pass port 0 for dynamic allocation.
func New(port int) (*Server, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Get actual port if dynamically allocated
	actualPort := listener.Addr().(*net.TCPAddr).Port

	grpcServer := grpc.NewServer()

	// Create managers
	projectMgr := project.NewManager()
	taskMgr := task.NewManager()

	srv := &Server{
		grpcServer:     grpcServer,
		listener:       listener,
		port:           actualPort,
		projectManager: projectMgr,
		taskManager:    taskMgr,
	}

	// Register services
	RegisterProjectServiceServer(grpcServer, &projectService{manager: projectMgr})
	RegisterTaskServiceServer(grpcServer, &taskService{manager: taskMgr})
	RegisterDaemonServiceServer(grpcServer, &daemonService{server: srv})

	return srv, nil
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

// Serve starts serving requests. This blocks until Stop is called.
func (s *Server) Serve() error {
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the server.
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}
