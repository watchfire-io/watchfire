package server

import (
	"context"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

type daemonService struct {
	pb.UnimplementedDaemonServiceServer
	server *Server
}

func (s *daemonService) GetStatus(_ context.Context, _ *emptypb.Empty) (*pb.DaemonStatus, error) {
	info, err := config.LoadDaemonInfo()
	if err != nil {
		return nil, err
	}

	agents := s.server.agentManager.ListAgents()
	activeProjects := make([]string, 0, len(agents))
	for _, a := range agents {
		activeProjects = append(activeProjects, a.ProjectID)
	}

	updateAvailable, updateVersion, updateURL := s.server.GetUpdateState()

	return &pb.DaemonStatus{
		Host:            info.Host,
		Port:            int32(info.Port),
		Pid:             int32(info.PID),
		StartedAt:       timestamppb.New(info.StartedAt),
		ActiveAgents:    int32(len(agents)),
		ActiveProjects:  activeProjects,
		UpdateAvailable: updateAvailable,
		UpdateVersion:   updateVersion,
		UpdateUrl:       updateURL,
	}, nil
}

func (s *daemonService) Shutdown(_ context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
	}()
	return &emptypb.Empty{}, nil
}
