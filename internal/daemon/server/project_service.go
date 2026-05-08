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
	"github.com/watchfire-io/watchfire/internal/daemon/agent"
	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

type projectService struct {
	pb.UnimplementedProjectServiceServer
	manager  *project.Manager
	agentMgr *agent.Manager
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
	if req.NotificationsMuted != nil {
		opts.NotificationsMuted = req.NotificationsMuted
	}
	if req.Sandbox != nil {
		opts.Sandbox = req.Sandbox
	}
	if req.Status != nil {
		opts.Status = req.Status
	}
	if req.Notifications != nil {
		// Project sends the full block — convert + override on disk.
		converted := projectNotificationsFromProto(req.Notifications)
		opts.Notifications = &converted
	}

	pwe, err := s.manager.UpdateProject(opts)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

func (s *projectService) DeleteProject(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	// Stop any running agent before unregistering
	_ = s.agentMgr.StopAgent(req.ProjectId)

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
			_, _ = fmt.Sscanf(parts[0], "%d", &info.Behind)
			_, _ = fmt.Sscanf(parts[1], "%d", &info.Ahead)
		}
	}

	return info, nil
}

// RegenerateProjectId regenerates the project's UUID + updates the global
// index entry. The local project path stays the same.
func (s *projectService) RegenerateProjectId(_ context.Context, req *pb.ProjectId) (*pb.Project, error) {
	pwe, err := s.manager.RegenerateProjectID(req.ProjectId)
	if err != nil {
		return nil, err
	}
	analytics.Track("project_id_regenerated", posthog.NewProperties().Set("origin", req.GetMeta().GetOrigin()))
	return modelToProtoProject(pwe), nil
}

// ResetTaskNumbering recomputes next_task_number from the highest existing
// task on disk + 1. With zero tasks it sets the counter to 1.
func (s *projectService) ResetTaskNumbering(_ context.Context, req *pb.ProjectId) (*pb.Project, error) {
	pwe, err := s.manager.ResetTaskNumbering(req.ProjectId)
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
}

// UnregisterProject drops the project from ~/.watchfire/projects.yaml.
// The local .watchfire/ folder is left untouched.
func (s *projectService) UnregisterProject(_ context.Context, req *pb.ProjectId) (*emptypb.Empty, error) {
	// Stop any running agent before unregistering.
	_ = s.agentMgr.StopAgent(req.ProjectId)
	if err := s.manager.UnregisterProject(req.ProjectId); err != nil {
		return nil, err
	}
	analytics.Track("project_unregistered", posthog.NewProperties().Set("origin", req.GetMeta().GetOrigin()))
	return &emptypb.Empty{}, nil
}

// SetGitHubAutoPRScope toggles the project's membership in
// `~/.watchfire/integrations.yaml` → `github.project_scopes`.
func (s *projectService) SetGitHubAutoPRScope(_ context.Context, req *pb.SetGitHubAutoPRScopeRequest) (*emptypb.Empty, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		// No integrations.yaml yet — nothing to do when disabling. When
		// enabling, materialise a fresh config so the membership lands.
		if !req.Enabled {
			return &emptypb.Empty{}, nil
		}
		cfg = models.NewIntegrationsConfig()
	}

	scopes := cfg.GitHub.ProjectScopes
	contains := false
	for _, id := range scopes {
		if id == req.ProjectId {
			contains = true
			break
		}
	}
	switch {
	case req.Enabled && !contains:
		scopes = append(scopes, req.ProjectId)
	case !req.Enabled && contains:
		out := scopes[:0]
		for _, id := range scopes {
			if id != req.ProjectId {
				out = append(out, id)
			}
		}
		scopes = out
	default:
		// no-op
		return &emptypb.Empty{}, nil
	}
	cfg.GitHub.ProjectScopes = scopes
	if err := config.SaveIntegrations(cfg); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// SetProjectIntegrationBindings persists the project's per-integration
// bindings (Slack channel + Discord guild) onto the project YAML. Empty
// strings clear the binding (= inherit global default).
func (s *projectService) SetProjectIntegrationBindings(_ context.Context, req *pb.SetProjectIntegrationBindingsRequest) (*pb.Project, error) {
	slack := req.SlackChannel
	guild := req.DiscordGuildId
	pwe, err := s.manager.UpdateProject(project.UpdateOptions{
		ProjectID:      req.ProjectId,
		SlackChannel:   &slack,
		DiscordGuildID: &guild,
	})
	if err != nil {
		return nil, err
	}
	return modelToProtoProject(pwe), nil
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
