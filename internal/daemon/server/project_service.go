package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/posthog/posthog-go"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/analytics"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	pb "github.com/watchfire-io/watchfire/proto"
)

type projectService struct {
	pb.UnimplementedProjectServiceServer
	manager *project.Manager
}

func (s *projectService) ListProjects(_ context.Context, _ *emptypb.Empty) (*pb.ProjectList, error) {
	results, err := s.manager.ListProjects()
	if err != nil {
		return nil, err
	}

	list := &pb.ProjectList{Projects: make([]*pb.Project, 0, len(results))}
	for _, pwe := range results {
		list.Projects = append(list.Projects, modelToProtoProject(pwe))
	}
	return list, nil
}

func (s *projectService) GetProject(_ context.Context, req *pb.ProjectId) (*pb.Project, error) {
	pwe, err := s.manager.GetProject(req.ProjectId)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

func (s *projectService) CreateProject(_ context.Context, req *pb.CreateProjectRequest) (*pb.Project, error) {
	pwe, err := s.manager.CreateProject(project.CreateOptions{
		Path:             req.Path,
		Name:             req.Name,
		Definition:       req.Definition,
		DefaultBranch:    req.DefaultBranch,
		AutoMerge:        req.AutoMerge,
		AutoDeleteBranch: req.AutoDeleteBranch,
		AutoStartTasks:   req.AutoStartTasks,
	})
	if err != nil {
		return nil, err
	}
	analytics.Track("project_created", posthog.NewProperties().Set("origin", req.GetMeta().GetOrigin()))
	return modelToProtoProject(pwe), nil
}

func (s *projectService) UpdateProject(_ context.Context, req *pb.UpdateProjectRequest) (*pb.Project, error) {
	opts := project.UpdateOptions{ProjectID: req.ProjectId}
	if req.Name != nil {
		opts.Name = req.Name
	}
	if req.Color != nil {
		opts.Color = req.Color
	}
	if req.DefaultBranch != nil {
		opts.DefaultBranch = req.DefaultBranch
	}
	if req.DefaultAgent != nil {
		opts.DefaultAgent = req.DefaultAgent
	}
	if req.AutoMerge != nil {
		opts.AutoMerge = req.AutoMerge
	}
	if req.AutoDeleteBranch != nil {
		opts.AutoDeleteBranch = req.AutoDeleteBranch
	}
	if req.AutoStartTasks != nil {
		opts.AutoStartTasks = req.AutoStartTasks
	}
	if req.Definition != nil {
		opts.Definition = req.Definition
	}
	if req.SecretsInstructions != nil {
		opts.SecretsInstructions = req.SecretsInstructions
	}

	pwe, err := s.manager.UpdateProject(opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

func (s *projectService) DeleteProject(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	err := s.manager.DeleteProject(req.ProjectId)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *projectService) ReorderProjects(_ context.Context, req *pb.ReorderProjectsRequest) (*pb.ProjectList, error) {
	results, err := s.manager.ReorderProjects(req.ProjectIds)
	if err != nil {
		return nil, err
	}
	list := &pb.ProjectList{Projects: make([]*pb.Project, 0, len(results))}
	for _, pwe := range results {
		list.Projects = append(list.Projects, modelToProtoProject(pwe))
	}
	return list, nil
}

func (s *projectService) GetGitInfo(_ context.Context, req *pb.ProjectId) (*pb.GitInfo, error) {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return nil, err
	}
	entry := index.FindProject(req.ProjectId)
	if entry == nil {
		return nil, fmt.Errorf("project not found: %s", req.ProjectId)
	}

	info := &pb.GitInfo{}

	// Current branch
	if out, err := runGit(entry.Path, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.CurrentBranch = out
	}

	// Remote URL
	if out, err := runGit(entry.Path, "remote", "get-url", "origin"); err == nil {
		// Strip protocol prefix for display
		remote := out
		remote = strings.TrimPrefix(remote, "https://")
		remote = strings.TrimPrefix(remote, "http://")
		remote = strings.TrimPrefix(remote, "git@")
		remote = strings.TrimSuffix(remote, ".git")
		remote = strings.Replace(remote, ":", "/", 1)
		info.RemoteUrl = remote
	}

	// Dirty status
	if out, err := runGit(entry.Path, "status", "--porcelain"); err == nil {
		if out != "" {
			lines := strings.Split(out, "\n")
			count := 0
			for _, l := range lines {
				if strings.TrimSpace(l) != "" {
					count++
				}
			}
			info.IsDirty = true
			info.UncommittedCount = int32(count)
		}
	}

	// Ahead/behind
	if out, err := runGit(entry.Path, "rev-list", "--left-right", "--count", "@{u}...HEAD"); err == nil {
		parts := strings.Fields(out)
		if len(parts) == 2 {
			fmt.Sscanf(parts[0], "%d", &info.Behind)
			fmt.Sscanf(parts[1], "%d", &info.Ahead)
		}
	}

	return info, nil
}

// runGit runs a git command in the given directory and returns trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
