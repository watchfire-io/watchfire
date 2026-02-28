package server

import (
	"context"
	"fmt"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

type logService struct {
	pb.UnimplementedLogServiceServer
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
		Content: content,
	}, nil
}
