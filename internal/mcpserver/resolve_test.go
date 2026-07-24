package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

func testProjects() []*pb.Project {
	return []*pb.Project{
		{ProjectId: "id-alpha", Name: "alpha"},
		{ProjectId: "id-beta", Name: "beta"},
		{ProjectId: "id-beta2", Name: "Beta"},
	}
}

func TestResolveProjectIDByID(t *testing.T) {
	got, err := resolveProjectID("id-alpha", "", testProjects())
	if err != nil {
		t.Fatalf("resolveProjectID: %v", err)
	}
	if got != "id-alpha" {
		t.Errorf("got %q, want id-alpha", got)
	}
}

func TestResolveProjectIDByName(t *testing.T) {
	got, err := resolveProjectID("alpha", "", testProjects())
	if err != nil {
		t.Fatalf("resolveProjectID: %v", err)
	}
	if got != "id-alpha" {
		t.Errorf("got %q, want id-alpha", got)
	}
}

func TestResolveProjectIDDefault(t *testing.T) {
	got, err := resolveProjectID("", "id-default", testProjects())
	if err != nil {
		t.Fatalf("resolveProjectID: %v", err)
	}
	if got != "id-default" {
		t.Errorf("got %q, want id-default", got)
	}
}

func TestResolveProjectIDNoDefault(t *testing.T) {
	_, err := resolveProjectID("", "", testProjects())
	if err == nil {
		t.Fatal("expected error for empty arg without default project")
	}
	if !strings.Contains(err.Error(), "alpha (id-alpha)") {
		t.Errorf("error should list valid projects, got: %v", err)
	}
}

func TestResolveProjectIDAmbiguous(t *testing.T) {
	_, err := resolveProjectID("beta", "", testProjects())
	if err == nil {
		t.Fatal("expected error for ambiguous name")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous") ||
		!strings.Contains(msg, "id-beta") || !strings.Contains(msg, "id-beta2") {
		t.Errorf("ambiguity error should list candidate ids, got: %v", err)
	}
}

func TestResolveProjectIDUnknown(t *testing.T) {
	_, err := resolveProjectID("nope", "", testProjects())
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
	if !strings.Contains(err.Error(), `"nope"`) ||
		!strings.Contains(err.Error(), "beta (id-beta)") {
		t.Errorf("unknown-project error should name the arg and list valid projects, got: %v", err)
	}
}

func writeProjectYAML(t *testing.T, dir, projectID string) {
	t.Helper()
	wfDir := filepath.Join(dir, ".watchfire")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "version: 1\nproject_id: " + projectID + "\nname: demo\nstatus: active\n"
	if err := os.WriteFile(filepath.Join(wfDir, "project.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindProjectDirWalkUp(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "proj")
	nested := filepath.Join(projectDir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectYAML(t, projectDir, "id-walkup")

	if got := findProjectDir(nested); got != projectDir {
		t.Errorf("findProjectDir(%q) = %q, want %q", nested, got, projectDir)
	}
	if got := findProjectDir(projectDir); got != projectDir {
		t.Errorf("findProjectDir(%q) = %q, want %q", projectDir, got, projectDir)
	}
	if got := findProjectDir(root); got != "" {
		t.Errorf("findProjectDir(%q) = %q, want empty", root, got)
	}
}

func TestDetectDefaultProjectFromCwd(t *testing.T) {
	// Isolate the global index (~/.watchfire) written by the
	// auto-register self-heal path.
	t.Setenv("HOME", t.TempDir())

	projectDir := filepath.Join(t.TempDir(), "proj")
	nested := filepath.Join(projectDir, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectYAML(t, projectDir, "id-cwd-default")

	t.Chdir(nested)
	got, err := detectDefaultProject()
	if err != nil {
		t.Fatalf("detectDefaultProject: %v", err)
	}
	if got != "id-cwd-default" {
		t.Errorf("got %q, want id-cwd-default", got)
	}
}

func TestDetectDefaultProjectOutsideProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	got, err := detectDefaultProject()
	if err != nil {
		t.Fatalf("detectDefaultProject: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty outside a project", got)
	}
}
