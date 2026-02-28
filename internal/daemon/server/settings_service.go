package server

import (
	"context"
	"fmt"

	"github.com/posthog/posthog-go"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/analytics"
	"github.com/watchfire-io/watchfire/internal/config"
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
		if req.Defaults.DefaultBranch != "" {
			settings.Defaults.DefaultBranch = req.Defaults.DefaultBranch
		}
		if req.Defaults.DefaultSandbox != "" {
			settings.Defaults.DefaultSandbox = req.Defaults.DefaultSandbox
		}
		if req.Defaults.DefaultAgent != "" {
			settings.Defaults.DefaultAgent = req.Defaults.DefaultAgent
		}
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

	// Merge agent configs
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
