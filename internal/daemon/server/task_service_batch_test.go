package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

func setupBatchTestProject(t *testing.T, projectID string) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	projectPath := t.TempDir()
	if err := config.EnsureProjectDir(projectPath); err != nil {
		t.Fatalf("EnsureProjectDir: %v", err)
	}
	proj := models.NewProject(projectID, "Batch Test", projectPath)
	if err := config.SaveProject(projectPath, proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := config.RegisterProject(projectID, proj.Name, projectPath); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	return projectPath
}

// TestCreateTasksBatchRPC drives the v10 Torch quick-add RPC end to end:
// bullets become tasks in input order through the validated create path,
// with colon/quote titles surviving the YAML round-trip.
func TestCreateTasksBatchRPC(t *testing.T) {
	projectID := "proj-batch-1"
	projectPath := setupBatchTestProject(t, projectID)

	svc := &taskService{manager: task.NewManager()}
	list, err := svc.CreateTasksBatch(context.Background(), &pb.CreateTasksBatchRequest{
		ProjectId: projectID,
		Text:      "- First: with a colon\n- Second \"quoted\" one\n  AC: it works\n- Third",
		Status:    "ready",
	})
	if err != nil {
		t.Fatalf("CreateTasksBatch: %v", err)
	}
	if len(list.Tasks) != 3 {
		t.Fatalf("created %d tasks, want 3", len(list.Tasks))
	}

	wantTitles := []string{"First: with a colon", `Second "quoted" one`, "Third"}
	for i, tk := range list.Tasks {
		if tk.Title != wantTitles[i] {
			t.Errorf("task %d title = %q, want %q", i, tk.Title, wantTitles[i])
		}
		if tk.Status != "ready" {
			t.Errorf("task %d status = %q, want ready", i, tk.Status)
		}
		if tk.TaskNumber != int32(i+1) {
			t.Errorf("task %d number = %d, want %d", i, tk.TaskNumber, i+1)
		}
		// Round-trip: the file the daemon wrote must load back cleanly.
		loaded, err := config.LoadTask(projectPath, int(tk.TaskNumber))
		if err != nil || loaded == nil {
			t.Fatalf("LoadTask(%d): %v", tk.TaskNumber, err)
		}
		if loaded.Title != wantTitles[i] {
			t.Errorf("task %d title after reload = %q, want %q", i, loaded.Title, wantTitles[i])
		}
	}
	if list.Tasks[1].AcceptanceCriteria != "it works" {
		t.Errorf("AC = %q, want %q", list.Tasks[1].AcceptanceCriteria, "it works")
	}
}

func TestCreateTasksBatchRPCRejectsEmptyAndBadStatus(t *testing.T) {
	projectID := "proj-batch-2"
	setupBatchTestProject(t, projectID)

	svc := &taskService{manager: task.NewManager()}

	_, err := svc.CreateTasksBatch(context.Background(), &pb.CreateTasksBatchRequest{
		ProjectId: projectID,
		Text:      "   \n\n",
		Status:    "ready",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty text: got %v, want InvalidArgument", err)
	}

	_, err = svc.CreateTasksBatch(context.Background(), &pb.CreateTasksBatchRequest{
		ProjectId: projectID,
		Text:      "- fine",
		Status:    "done",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("status done: got %v, want InvalidArgument", err)
	}
}
