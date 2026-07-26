package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

// resolveProject turns an optional `project` tool argument (project id or
// name) into a project_id, falling back to the server's default project when
// the argument is empty.
func (s *server) resolveProject(ctx context.Context, arg string) (string, error) {
	list, err := s.projects.ListProjects(ctx, &emptypb.Empty{})
	if err != nil {
		return "", rpcErr("list projects to resolve the \"project\" argument", err)
	}
	return resolveProjectID(arg, s.defaultProjectID, list.Projects)
}

// resolveProjectID resolves arg against the registered projects: exact
// project id first, then project name (case-insensitive). An empty arg
// resolves to defaultID when set.
func resolveProjectID(arg, defaultID string, projects []*pb.Project) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		if defaultID != "" {
			return defaultID, nil
		}
		return "", fmt.Errorf("no \"project\" argument given and the server was not started inside a registered project directory; pass one of: %s", projectChoices(projects))
	}

	for _, p := range projects {
		if p.ProjectId == arg {
			return p.ProjectId, nil
		}
	}

	var matches []*pb.Project
	for _, p := range projects {
		if strings.EqualFold(p.Name, arg) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ProjectId, nil
	case 0:
		return "", fmt.Errorf("project %q not found — known projects: %s", arg, projectChoices(projects))
	default:
		return "", fmt.Errorf("project name %q is ambiguous; pass a project id instead: %s", arg, projectChoices(matches))
	}
}

// projectChoices renders projects as "name (id)" for error messages.
func projectChoices(projects []*pb.Project) string {
	if len(projects) == 0 {
		return "(no projects registered — run 'watchfire init' in a project directory)"
	}
	parts := make([]string, 0, len(projects))
	for _, p := range projects {
		parts = append(parts, fmt.Sprintf("%s (%s)", p.Name, p.ProjectId))
	}
	return strings.Join(parts, ", ")
}

// detectDefaultProject mirrors the CLI's project resolution for the server's
// working directory: walk up to the nearest directory containing
// .watchfire/project.yaml and self-heal its registration in the global
// index. Returns "" (no error) when the server was started outside a
// project.
func detectDefaultProject() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := findProjectDir(cwd)
	if dir == "" {
		return "", nil
	}
	project, err := config.LoadProject(dir)
	if err != nil {
		return "", err
	}
	if project == nil {
		return "", nil
	}
	// Self-heal the global index registration best-effort: a failure
	// (e.g. an unwritable ~/.watchfire) must not cost us the cwd default
	// when the project is already registered.
	if err := config.EnsureProjectRegistered(dir); err != nil {
		return project.ProjectID, err
	}
	return project.ProjectID, nil
}

// findProjectDir walks up from start to the filesystem root looking for a
// directory that contains .watchfire/project.yaml.
func findProjectDir(start string) string {
	dir := start
	for {
		if config.FileExists(config.ProjectFile(dir)) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
