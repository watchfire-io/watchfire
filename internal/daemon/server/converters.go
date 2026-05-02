package server

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/watchfire-io/watchfire/internal/daemon/project"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

func modelToProtoProject(pwe project.ProjectWithEntry) *pb.Project {
	p := pwe.Project
	return &pb.Project{
		ProjectId:           p.ProjectID,
		Name:                p.Name,
		Path:                pwe.Path,
		Status:              p.Status,
		Color:               p.Color,
		DefaultAgent:        p.DefaultAgent,
		Sandbox:             p.Sandbox,
		AutoMerge:           p.AutoMerge,
		AutoDeleteBranch:    p.AutoDeleteBranch,
		AutoStartTasks:      p.AutoStartTasks,
		Definition:          p.Definition,
		SecretsInstructions: p.SecretsInstructions,
		CreatedAt:           timestamppb.New(p.CreatedAt),
		UpdatedAt:           timestamppb.New(p.UpdatedAt),
		NextTaskNumber:      int32(p.NextTaskNumber),
		Position:            int32(pwe.Position),
		Notifications: &pb.ProjectNotifications{
			Muted: p.Notifications.Muted,
		},
	}
}

func modelToProtoTask(t *models.Task, projectID string) *pb.Task {
	protoTask := &pb.Task{
		TaskId:             t.TaskID,
		TaskNumber:         int32(t.TaskNumber),
		ProjectId:          projectID,
		Title:              t.Title,
		Prompt:             t.Prompt,
		AcceptanceCriteria: t.AcceptanceCriteria,
		Agent:              t.Agent,
		Status:             string(t.Status),
		Position:           int32(t.Position),
		AgentSessions:      int32(t.AgentSessions),
		CreatedAt:          timestamppb.New(t.CreatedAt),
		UpdatedAt:          timestamppb.New(t.UpdatedAt),
	}

	if t.Success != nil {
		protoTask.Success = t.Success
	}
	if t.FailureReason != "" {
		protoTask.FailureReason = &t.FailureReason
	}
	if t.StartedAt != nil {
		protoTask.StartedAt = timestamppb.New(*t.StartedAt)
	}
	if t.CompletedAt != nil {
		protoTask.CompletedAt = timestamppb.New(*t.CompletedAt)
	}
	if t.DeletedAt != nil {
		protoTask.DeletedAt = timestamppb.New(*t.DeletedAt)
	}

	return protoTask
}

func modelToProtoSettings(s *models.Settings) *pb.Settings {
	agents := make(map[string]*pb.AgentConfig, len(s.Agents))
	for name, cfg := range s.Agents {
		agents[name] = &pb.AgentConfig{Path: cfg.Path}
	}
	return &pb.Settings{
		Version: int32(s.Version),
		Agents:  agents,
		Defaults: &pb.DefaultsConfig{
			AutoMerge:        s.Defaults.AutoMerge,
			AutoDeleteBranch: s.Defaults.AutoDeleteBranch,
			AutoStartTasks:   s.Defaults.AutoStartTasks,
			DefaultSandbox:   s.Defaults.DefaultSandbox,
			DefaultAgent:     s.Defaults.DefaultAgent,
			Notifications:    notificationsToProto(s.Defaults.Notifications),
		},
		Updates: &pb.UpdatesConfig{
			CheckOnStartup: s.Updates.CheckOnStartup,
			CheckFrequency: s.Updates.CheckFrequency,
			AutoDownload:   s.Updates.AutoDownload,
		},
		Appearance: &pb.AppearanceConfig{
			Theme: s.Appearance.Theme,
		},
	}
}

func notificationsToProto(n models.NotificationsConfig) *pb.NotificationsConfig {
	return &pb.NotificationsConfig{
		Enabled: n.Enabled,
		Events: &pb.NotificationsEvents{
			TaskFailed:  n.Events.TaskFailed,
			RunComplete: n.Events.RunComplete,
		},
		Sounds: &pb.NotificationsSounds{
			Enabled:     n.Sounds.Enabled,
			TaskFailed:  n.Sounds.TaskFailed,
			RunComplete: n.Sounds.RunComplete,
			Volume:      n.Sounds.Volume,
		},
		QuietHours: &pb.QuietHoursConfig{
			Enabled: n.QuietHours.Enabled,
			Start:   n.QuietHours.Start,
			End:     n.QuietHours.End,
		},
	}
}

func notificationsFromProto(p *pb.NotificationsConfig) models.NotificationsConfig {
	if p == nil {
		return models.DefaultNotifications()
	}
	out := models.NotificationsConfig{Enabled: p.Enabled}
	if p.Events != nil {
		out.Events.TaskFailed = p.Events.TaskFailed
		out.Events.RunComplete = p.Events.RunComplete
	}
	if p.Sounds != nil {
		out.Sounds.Enabled = p.Sounds.Enabled
		out.Sounds.TaskFailed = p.Sounds.TaskFailed
		out.Sounds.RunComplete = p.Sounds.RunComplete
		out.Sounds.Volume = p.Sounds.Volume
	}
	if p.QuietHours != nil {
		out.QuietHours.Enabled = p.QuietHours.Enabled
		out.QuietHours.Start = p.QuietHours.Start
		out.QuietHours.End = p.QuietHours.End
	}
	return out
}
