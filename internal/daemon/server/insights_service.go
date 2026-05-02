package server

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/daemon/insights"
	pb "github.com/watchfire-io/watchfire/proto"
)

// insightsService implements the v6.0 Ember InsightsService gRPC contract.
// The handler delegates rendering to internal/daemon/insights, which keeps
// the I/O layer (loading task YAMLs from disk) and the rendering layer
// (Markdown templates + CSV writers) testable in isolation.
type insightsService struct {
	pb.UnimplementedInsightsServiceServer

	// nowFn is hoisted for tests; production calls time.Now. The "report
	// generated at" timestamp is what the canonical filename uses, so a
	// fixed clock keeps golden tests reproducible.
	nowFn func() time.Time
}

func newInsightsService() *insightsService {
	return &insightsService{nowFn: time.Now}
}

func (s *insightsService) ExportReport(_ context.Context, req *pb.ExportReportRequest) (*pb.ExportReportResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "ExportReportRequest required")
	}
	format, err := protoFormatToInsights(req.GetFormat())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	now := s.now()
	windowStart := tsToTime(req.GetWindowStart())
	windowEnd := tsToTime(req.GetWindowEnd())

	switch scope := req.GetScope().(type) {
	case *pb.ExportReportRequest_SingleTask:
		data, loadErr := insights.LoadSingleTaskData(scope.SingleTask)
		if loadErr != nil {
			return nil, status.Error(codes.NotFound, loadErr.Error())
		}
		out, renderErr := insights.ExportSingleTaskFromData(data, format, now)
		if renderErr != nil {
			return nil, status.Error(codes.Internal, renderErr.Error())
		}
		return resultToProto(out), nil

	case *pb.ExportReportRequest_ProjectId:
		data, loadErr := insights.LoadProjectData(scope.ProjectId, windowStart, windowEnd)
		if loadErr != nil {
			return nil, status.Error(codes.NotFound, loadErr.Error())
		}
		out, renderErr := insights.ExportProjectFromData(data, format, now)
		if renderErr != nil {
			return nil, status.Error(codes.Internal, renderErr.Error())
		}
		return resultToProto(out), nil

	case *pb.ExportReportRequest_Global:
		if !scope.Global {
			return nil, status.Error(codes.InvalidArgument, "global scope requires global=true")
		}
		data, loadErr := insights.LoadGlobalData(windowStart, windowEnd)
		if loadErr != nil {
			return nil, status.Error(codes.Internal, loadErr.Error())
		}
		out, renderErr := insights.ExportGlobalFromData(data, format, now)
		if renderErr != nil {
			return nil, status.Error(codes.Internal, renderErr.Error())
		}
		return resultToProto(out), nil

	default:
		return nil, status.Error(codes.InvalidArgument, "ExportReportRequest.scope must be set")
	}
}

func (s *insightsService) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func protoFormatToInsights(f pb.ExportFormat) (insights.Format, error) {
	switch f {
	case pb.ExportFormat_CSV:
		return insights.FormatCSV, nil
	case pb.ExportFormat_MARKDOWN:
		return insights.FormatMarkdown, nil
	default:
		return 0, fmt.Errorf("unknown ExportFormat: %v", f)
	}
}

// tsToTime converts a proto Timestamp pointer (which may be nil if the
// caller didn't set a window bound) to a time.Time. AsTime() returns the
// Unix epoch for an unset bound; downstream code expects the zero value
// for "no bound", so we collapse that here.
func tsToTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	t := ts.AsTime()
	if t.Unix() == 0 {
		return time.Time{}
	}
	return t
}

func resultToProto(r insights.Result) *pb.ExportReportResponse {
	return &pb.ExportReportResponse{
		Filename: r.Filename,
		Content:  r.Content,
		Mime:     r.Mime,
	}
}
