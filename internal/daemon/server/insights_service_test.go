package server

import (
	"context"
	"strings"
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

// TestInsightsServiceArgValidation covers the three error gates the handler
// is responsible for: nil request, missing scope oneof, and an unset
// global=false. The downstream load + render leg has its own tests in the
// insights package — here we just check the wire-level contract.
func TestInsightsServiceArgValidation(t *testing.T) {
	svc := newInsightsService()
	ctx := context.Background()

	if _, err := svc.ExportReport(ctx, nil); err == nil {
		t.Errorf("nil request: want error, got nil")
	}
	if _, err := svc.ExportReport(ctx, &pb.ExportReportRequest{Format: pb.ExportFormat_MARKDOWN}); err == nil {
		t.Errorf("missing scope: want error, got nil")
	}
	if _, err := svc.ExportReport(ctx, &pb.ExportReportRequest{
		Format: pb.ExportFormat_MARKDOWN,
		Scope:  &pb.ExportReportRequest_Global{Global: false},
	}); err == nil {
		t.Errorf("global=false: want error, got nil")
	}
}

// TestInsightsServiceUnknownProject — the handler should bubble up a
// NotFound when the named project isn't in the projects index. We can't
// easily mock the file system in unit tests, but the error path is reached
// because no projects index exists in test mode.
func TestInsightsServiceUnknownProject(t *testing.T) {
	svc := newInsightsService()
	_, err := svc.ExportReport(context.Background(), &pb.ExportReportRequest{
		Format: pb.ExportFormat_MARKDOWN,
		Scope:  &pb.ExportReportRequest_ProjectId{ProjectId: "definitely-not-a-project"},
	})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}
