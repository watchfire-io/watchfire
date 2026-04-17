package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	pb "github.com/watchfire-io/watchfire/proto"
)

type logService struct {
	pb.UnimplementedLogServiceServer
	projectMgr *project.Manager
}

func (s *logService) ListLogs(_ context.Context, req *pb.ListLogsRequest) (*pb.LogList, error) {
	logs, err := config.ListLogs(req.ProjectId)
	if err != nil {
		return nil, fmt.Errorf("failed to list logs: %w", err)
	}

	list := &pb.LogList{Logs: make([]*pb.LogEntry, 0, len(logs))}
	for _, l := range logs {
		list.Logs = append(list.Logs, &pb.LogEntry{
			LogId:         l.LogID,
			ProjectId:     l.ProjectID,
			TaskNumber:    int32(l.TaskNumber),
			SessionNumber: int32(l.SessionNumber),
			Agent:         l.Agent,
			Mode:          l.Mode,
			StartedAt:     l.StartedAt,
			EndedAt:       l.EndedAt,
			Status:        l.Status,
		})
	}
	return list, nil
}

func (s *logService) GetLog(_ context.Context, req *pb.GetLogRequest) (*pb.LogContent, error) {
	entry, content, err := config.ReadLog(req.ProjectId, req.LogId)
	if err != nil {
		return nil, fmt.Errorf("failed to read log: %w", err)
	}

	return &pb.LogContent{
		Entry: &pb.LogEntry{
			LogId:         entry.LogID,
			ProjectId:     entry.ProjectID,
			TaskNumber:    int32(entry.TaskNumber),
			SessionNumber: int32(entry.SessionNumber),
			Agent:         entry.Agent,
			Mode:          entry.Mode,
			StartedAt:     entry.StartedAt,
			EndedAt:       entry.EndedAt,
			Status:        entry.Status,
		},
		Content: strings.ToValidUTF8(content, "\uFFFD"),
	}, nil
}

func (s *logService) DeleteLog(_ context.Context, req *pb.DeleteLogRequest) (*emptypb.Empty, error) {
	if strings.TrimSpace(req.ProjectId) == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id is required")
	}
	if strings.TrimSpace(req.LogId) == "" {
		return nil, status.Error(codes.InvalidArgument, "log_id is required")
	}

	if s.projectMgr != nil {
		if _, err := s.projectMgr.GetProject(req.ProjectId); err != nil {
			return nil, status.Errorf(codes.NotFound, "project not found: %s", req.ProjectId)
		}
	}

	if err := config.DeleteLog(req.ProjectId, req.LogId); err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsNotExist(errors.Unwrap(err)) {
			return nil, status.Errorf(codes.NotFound, "log not found: %s", req.LogId)
		}
		return nil, status.Errorf(codes.Internal, "failed to delete log: %v", err)
	}

	return &emptypb.Empty{}, nil
}
