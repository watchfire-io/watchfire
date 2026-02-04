// Package server implements the gRPC server for the daemon.
package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"

	"google.golang.org/grpc"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/daemon/tray"
)

// Server is the daemon's gRPC server.
type Server struct {
	grpcServer     *grpc.Server
	listener       net.Listener
	port           int
	projectManager *project.Manager
	taskManager    *task.Manager
	agentManager   *agent.Manager
}

// New creates a new server listening on the specified port.
// Pass port 0 for dynamic allocation.
func New(port int) (*Server, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.TODO(), "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Get actual port if dynamically allocated
	actualPort := listener.Addr().(*net.TCPAddr).Port

	grpcServer := grpc.NewServer()

	// Create managers
	projectMgr := project.NewManager()
	taskMgr := task.NewManager()
	agentMgr := agent.NewManager()

	srv := &Server{
		grpcServer:     grpcServer,
		listener:       listener,
		port:           actualPort,
		projectManager: projectMgr,
		taskManager:    taskMgr,
		agentManager:   agentMgr,
	}

	// Register services
	RegisterProjectServiceServer(grpcServer, &projectService{manager: projectMgr})
	RegisterTaskServiceServer(grpcServer, &taskService{manager: taskMgr})
	RegisterDaemonServiceServer(grpcServer, &daemonService{server: srv})
	RegisterAgentServiceServer(grpcServer, &agentService{manager: agentMgr})

	return srv, nil
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

// AgentManager returns the agent manager.
func (s *Server) AgentManager() *agent.Manager {
	return s.agentManager
}

// Serve starts serving requests. This blocks until Stop is called.
func (s *Server) Serve() error {
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the server.
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}

// TrayState adapts a Server to the tray.DaemonState interface.
type TrayState struct {
	srv *Server
}

// NewTrayState creates a TrayState for the given server.
func NewTrayState(srv *Server) *TrayState {
	return &TrayState{srv: srv}
}

// Port returns the port the server is listening on.
func (t *TrayState) Port() int {
	return t.srv.Port()
}

// ProjectCount returns the number of registered projects.
func (t *TrayState) ProjectCount() int {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return 0
	}
	return len(index.Projects)
}

// ActiveAgents returns information about all currently running agents.
func (t *TrayState) ActiveAgents() []tray.AgentInfo {
	running := t.srv.agentManager.ListAgents()
	agents := make([]tray.AgentInfo, 0, len(running))
	for _, a := range running {
		agents = append(agents, tray.AgentInfo{
			ProjectName: a.ProjectName,
			Mode:        string(a.Mode),
			TaskNumber:  a.TaskNumber,
			TaskTitle:   a.TaskTitle,
		})
	}
	return agents
}

// RequestShutdown sends SIGINT to the current process to trigger a graceful shutdown.
func (t *TrayState) RequestShutdown() {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGINT)
}
