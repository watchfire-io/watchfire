package server

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	pb "github.com/watchfire-io/watchfire/proto"
)

type notificationService struct {
	pb.UnimplementedNotificationServiceServer
	bus *notify.Bus
}

// Subscribe streams notifications to GUI clients. The headless JSONL fallback
// at ~/.watchfire/logs/<project_id>/notifications.log is written by the
// emitter regardless of whether anyone is subscribed; this stream is the
// live path that drives native OS notifications in the GUI.
func (s *notificationService) Subscribe(_ *pb.SubscribeNotificationsRequest, stream pb.NotificationService_SubscribeServer) error {
	if s.bus == nil {
		// Daemon built without a bus (shouldn't happen in production, but
		// keep the stream open so the GUI doesn't burn into a reconnect loop).
		<-stream.Context().Done()
		return nil
	}
	ch, cancel := s.bus.Subscribe()
	defer cancel()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.Notification{
				Id:         n.ID,
				ProjectId:  n.ProjectID,
				TaskNumber: n.TaskNumber,
				Title:      n.Title,
				Body:       n.Body,
				EmittedAt:  timestamppb.New(n.EmittedAt),
				Kind:       notifyKindToProto(n.Kind),
			}); err != nil {
				return err
			}
		}
	}
}

func notifyKindToProto(k notify.Kind) pb.NotificationKind {
	switch k {
	case notify.KindRunComplete:
		return pb.NotificationKind_RUN_COMPLETE
	case notify.KindTaskFailed:
		return pb.NotificationKind_TASK_FAILED
	case notify.KindWeeklyDigest:
		return pb.NotificationKind_WEEKLY_DIGEST
	default:
		return pb.NotificationKind_TASK_FAILED
	}
}
