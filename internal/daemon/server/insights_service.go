package server

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/diff"
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

func (s *insightsService) GetGlobalInsights(_ context.Context, req *pb.GetGlobalInsightsRequest) (*pb.GlobalInsights, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "GetGlobalInsightsRequest required")
	}
	windowStart := tsToTime(req.GetWindowStart())
	windowEnd := tsToTime(req.GetWindowEnd())

	data, err := insights.LoadGlobalInsights(windowStart, windowEnd)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return globalInsightsToProto(data), nil
}

func (s *insightsService) GetProjectInsights(_ context.Context, req *pb.GetProjectInsightsRequest) (*pb.ProjectInsights, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "GetProjectInsightsRequest required")
	}
	if req.GetProjectId() == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	windowStart := tsToTime(req.GetWindowStart())
	windowEnd := tsToTime(req.GetWindowEnd())

	data, err := insights.LoadProjectInsights(req.GetProjectId(), windowStart, windowEnd)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return projectInsightsToProto(data), nil
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

// GetTaskDiff returns the structured per-task diff for the v6.0 Ember
// inline diff viewer. Resolution path lives in `internal/daemon/diff` —
// pre-merge (branch still exists) and post-merge (branch deleted, merge
// commit found via `--grep`) are both handled there.
func (s *insightsService) GetTaskDiff(_ context.Context, req *pb.GetTaskDiffRequest) (*pb.FileDiffSet, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "GetTaskDiffRequest required")
	}
	if req.GetProjectId() == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if req.GetTaskNumber() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "task_number required")
	}

	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	entry := index.FindProject(req.GetProjectId())
	if entry == nil {
		return nil, status.Errorf(codes.NotFound, "project %s not found", req.GetProjectId())
	}

	out, err := diff.TaskDiff(entry.Path, req.GetProjectId(), int(req.GetTaskNumber()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return diffSetToProto(out), nil
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

// globalInsightsToProto converts the Go aggregator output to its proto
// counterpart. Cost data isn't yet wired (task 0056); the field-by-field
// copy stays simple so when 0056 lands the only change is upstream of
// here.
func globalInsightsToProto(g *insights.GlobalInsights) *pb.GlobalInsights {
	if g == nil {
		return &pb.GlobalInsights{}
	}
	out := &pb.GlobalInsights{
		TasksTotal:       int32(g.TasksTotal),
		TasksSucceeded:   int32(g.TasksSucceeded),
		TasksFailed:      int32(g.TasksFailed),
		TotalDurationMs:  g.TotalDurationMs,
		TotalCostUsd:     g.TotalCostUSD,
		TasksMissingCost: int32(g.TasksMissingCost),
	}
	if !g.WindowStart.IsZero() {
		out.WindowStart = timestamppb.New(g.WindowStart)
	}
	if !g.WindowEnd.IsZero() {
		out.WindowEnd = timestamppb.New(g.WindowEnd)
	}
	out.TasksByDay = make([]*pb.DayBucket, 0, len(g.TasksByDay))
	for _, b := range g.TasksByDay {
		out.TasksByDay = append(out.TasksByDay, &pb.DayBucket{
			Date:      b.Date,
			Count:     int32(b.Count),
			Succeeded: int32(b.Succeeded),
			Failed:    int32(b.Failed),
		})
	}
	out.TopProjects = make([]*pb.TopProject, 0, len(g.TopProjects))
	for _, p := range g.TopProjects {
		out.TopProjects = append(out.TopProjects, &pb.TopProject{
			ProjectId:    p.ProjectID,
			ProjectName:  p.ProjectName,
			ProjectColor: p.ProjectColor,
			Count:        int32(p.Count),
			SuccessRate:  p.SuccessRate,
		})
	}
	out.AgentBreakdown = make([]*pb.AgentBreakdown, 0, len(g.AgentBreakdown))
	for _, a := range g.AgentBreakdown {
		out.AgentBreakdown = append(out.AgentBreakdown, &pb.AgentBreakdown{
			Agent:          a.Agent,
			Count:          int32(a.Count),
			SuccessRate:    a.SuccessRate,
			AvgDurationMs:  a.AvgDurationMs,
			TotalTokensIn:  a.TotalTokensIn,
			TotalTokensOut: a.TotalTokensOut,
			TotalCostUsd:   a.TotalCostUSD,
		})
	}
	return out
}

// projectInsightsToProto converts the per-project Go aggregator output to
// its proto counterpart. Same approach as globalInsightsToProto — no fancy
// translation, just field-by-field copy plus optional Timestamp wrapping.
func projectInsightsToProto(p *insights.ProjectInsights) *pb.ProjectInsights {
	if p == nil {
		return &pb.ProjectInsights{}
	}
	out := &pb.ProjectInsights{
		ProjectId:        p.ProjectID,
		TasksTotal:       int32(p.TasksTotal),
		TasksSucceeded:   int32(p.TasksSucceeded),
		TasksFailed:      int32(p.TasksFailed),
		TotalDurationMs:  p.TotalDurationMs,
		AvgDurationMs:    p.AvgDurationMs,
		P50DurationMs:    p.P50DurationMs,
		P95DurationMs:    p.P95DurationMs,
		TotalCostUsd:     p.TotalCostUSD,
		TasksMissingCost: int32(p.TasksMissingCost),
	}
	if !p.WindowStart.IsZero() {
		out.WindowStart = timestamppb.New(p.WindowStart)
	}
	if !p.WindowEnd.IsZero() {
		out.WindowEnd = timestamppb.New(p.WindowEnd)
	}
	out.TasksByDay = make([]*pb.DayBucket, 0, len(p.TasksByDay))
	for _, b := range p.TasksByDay {
		out.TasksByDay = append(out.TasksByDay, &pb.DayBucket{
			Date:      b.Date,
			Count:     int32(b.Count),
			Succeeded: int32(b.Succeeded),
			Failed:    int32(b.Failed),
		})
	}
	out.AgentBreakdown = make([]*pb.AgentBreakdown, 0, len(p.AgentBreakdown))
	for _, a := range p.AgentBreakdown {
		out.AgentBreakdown = append(out.AgentBreakdown, &pb.AgentBreakdown{
			Agent:          a.Agent,
			Count:          int32(a.Count),
			SuccessRate:    a.SuccessRate,
			AvgDurationMs:  a.AvgDurationMs,
			TotalTokensIn:  a.TotalTokensIn,
			TotalTokensOut: a.TotalTokensOut,
			TotalCostUsd:   a.TotalCostUSD,
		})
	}
	return out
}

// diffSetToProto converts the diff package's FileDiffSet to its proto
// counterpart. Field-by-field copy; the on-the-wire enum values are kept
// in sync with the diff package's string constants.
func diffSetToProto(set *diff.FileDiffSet) *pb.FileDiffSet {
	if set == nil {
		return &pb.FileDiffSet{}
	}
	out := &pb.FileDiffSet{
		TotalAdditions: int32(set.TotalAdditions),
		TotalDeletions: int32(set.TotalDeletions),
		Truncated:      set.Truncated,
	}
	out.Files = make([]*pb.FileDiff, 0, len(set.Files))
	for _, f := range set.Files {
		fd := &pb.FileDiff{
			Path:    f.Path,
			Status:  fileStatusToProto(f.Status),
			OldPath: f.OldPath,
		}
		fd.Hunks = make([]*pb.Hunk, 0, len(f.Hunks))
		for _, h := range f.Hunks {
			hp := &pb.Hunk{
				OldStart: int32(h.OldStart),
				OldLines: int32(h.OldLines),
				NewStart: int32(h.NewStart),
				NewLines: int32(h.NewLines),
				Header:   h.Header,
			}
			hp.Lines = make([]*pb.DiffLine, 0, len(h.Lines))
			for _, l := range h.Lines {
				hp.Lines = append(hp.Lines, &pb.DiffLine{
					Kind: lineKindToProto(l.Kind),
					Text: l.Text,
				})
			}
			fd.Hunks = append(fd.Hunks, hp)
		}
		out.Files = append(out.Files, fd)
	}
	return out
}

func fileStatusToProto(s diff.FileStatus) pb.FileDiff_Status {
	switch s {
	case diff.StatusAdded:
		return pb.FileDiff_ADDED
	case diff.StatusDeleted:
		return pb.FileDiff_DELETED
	case diff.StatusRenamed:
		return pb.FileDiff_RENAMED
	default:
		return pb.FileDiff_MODIFIED
	}
}

func lineKindToProto(k diff.LineKind) pb.DiffLine_Kind {
	switch k {
	case diff.LineAdd:
		return pb.DiffLine_ADD
	case diff.LineDel:
		return pb.DiffLine_DEL
	default:
		return pb.DiffLine_CONTEXT
	}
}
