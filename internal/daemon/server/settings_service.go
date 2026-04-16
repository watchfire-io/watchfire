package server

import (
	"context"
	"fmt"

	"github.com/posthog/posthog-go"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/analytics"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

type settingsService struct {
	pb.UnimplementedSettingsServiceServer
}

func (s *settingsService) GetSettings(_ context.Context, _ *emptypb.Empty) (*pb.Settings, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}
	pbSettings := modelToProtoSettings(settings)
	if installID, err := config.LoadInstallationID(); err == nil {
		pbSettings.InstallationId = installID
	}
	return pbSettings, nil
}

func (s *settingsService) UpdateSettings(_ context.Context, req *pb.UpdateSettingsRequest) (*pb.Settings, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}

	if req.Defaults != nil {
		settings.Defaults.AutoMerge = req.Defaults.AutoMerge
		settings.Defaults.AutoDeleteBranch = req.Defaults.AutoDeleteBranch
		settings.Defaults.AutoStartTasks = req.Defaults.AutoStartTasks
		if req.Defaults.DefaultSandbox != "" {
			settings.Defaults.DefaultSandbox = req.Defaults.DefaultSandbox
		}
		// Empty string means "Ask per project" — always write through so
		// the user can clear a previously-set global default.
		if req.Defaults.DefaultAgent != "" {
			if _, ok := backend.Get(req.Defaults.DefaultAgent); !ok {
				return nil, fmt.Errorf("unknown agent %q", req.Defaults.DefaultAgent)
			}
		}
		settings.Defaults.DefaultAgent = req.Defaults.DefaultAgent
	}

	if req.Updates != nil {
		settings.Updates.CheckOnStartup = req.Updates.CheckOnStartup
		if req.Updates.CheckFrequency != "" {
			settings.Updates.CheckFrequency = req.Updates.CheckFrequency
		}
		settings.Updates.AutoDownload = req.Updates.AutoDownload
	}

	if req.Appearance != nil {
		if req.Appearance.Theme != "" {
			settings.Appearance.Theme = req.Appearance.Theme
		}
	}

	// Merge agent configs. Reject unknown agents so typos in the UI or
	// stale clients don't silently pollute settings.yaml.
	for name := range req.Agents {
		if _, ok := backend.Get(name); !ok {
			return nil, fmt.Errorf("unknown agent %q", name)
		}
	}
	for name, agentCfg := range req.Agents {
		if settings.Agents == nil {
			settings.Agents = make(map[string]*models.AgentConfig)
		}
		settings.Agents[name] = &models.AgentConfig{Path: agentCfg.Path}
	}

	if err := config.SaveSettings(settings); err != nil {
		return nil, fmt.Errorf("failed to save settings: %w", err)
	}
	analytics.Track("settings_updated", posthog.NewProperties().Set("origin", req.GetMeta().GetOrigin()))
	return modelToProtoSettings(settings), nil
}

func (s *settingsService) ListAgents(_ context.Context, _ *emptypb.Empty) (*pb.AgentList, error) {
	backends := backend.List()
	agents := make([]*pb.AgentInfo, 0, len(backends))
	for _, b := range backends {
		agents = append(agents, &pb.AgentInfo{Name: b.Name(), DisplayName: b.DisplayName()})
	}
	return &pb.AgentList{Agents: agents}, nil
}
