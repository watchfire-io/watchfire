package server

import (
	"context"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/focus"
	"github.com/watchfire-io/watchfire/internal/daemon/tray"
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

func (s *daemonService) Ping(_ context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *daemonService) Shutdown(_ context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
	}()
	return &emptypb.Empty{}, nil
}

// SubscribeFocusEvents streams tray-emitted focus events to subscribers (the
// GUI). Each event maps a click in the tray menu to a "bring this view to the
// foreground" request the GUI honours by focusing its window and routing.
func (s *daemonService) SubscribeFocusEvents(_ *pb.SubscribeFocusEventsRequest, stream pb.DaemonService_SubscribeFocusEventsServer) error {
	bus := tray.FocusBus()
	if bus == nil {
		// Headless build — no events will ever fire, but the stream stays
		// open so the GUI can attach.
		<-stream.Context().Done()
		return nil
	}
	ch, cancel := bus.Subscribe()
	defer cancel()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.FocusEvent{
				ProjectId:  ev.ProjectID,
				Target:     focusTargetToProto(ev.Target),
				TaskNumber: ev.TaskNumber,
				DigestDate: ev.DigestDate,
			}); err != nil {
				return err
			}
		}
	}
}

func focusTargetToProto(t focus.Target) pb.FocusTarget {
	switch t {
	case focus.TargetTasks:
		return pb.FocusTarget_FOCUS_TARGET_TASKS
	case focus.TargetTask:
		return pb.FocusTarget_FOCUS_TARGET_TASK
	case focus.TargetDigest:
		return pb.FocusTarget_FOCUS_TARGET_DIGEST
	default:
		return pb.FocusTarget_FOCUS_TARGET_MAIN
	}
}
