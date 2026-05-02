package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

		// Custom shell path for the GUI's in-app terminal (issue #32). Empty
		// means "use $SHELL via login-shell autodetection". A non-empty value
		// must point at an executable file the user can actually run; we
		// validate with X_OK semantics here so a typo is rejected at save
		// time rather than producing a silently-broken terminal later.
		if req.Defaults.TerminalShell != "" {
			if err := validateExecutablePath(req.Defaults.TerminalShell); err != nil {
				return nil, fmt.Errorf("invalid terminal_shell %q: %w", req.Defaults.TerminalShell, err)
			}
		}
		settings.Defaults.TerminalShell = req.Defaults.TerminalShell

		if req.Defaults.Notifications != nil {
			incoming := notificationsFromProto(req.Defaults.Notifications)
			// Roll back malformed quiet-hours strings to the defaults rather than
			// persist garbage that would silently swallow notifications later. The
			// caller is expected to surface this to the user; we return an error
			// so the GUI/TUI can flash a settings-save error toast / status-bar
			// message.
			if incoming.QuietHours.Start != "" && !models.IsValidTimeOfDay(incoming.QuietHours.Start) {
				return nil, fmt.Errorf("invalid quiet_hours.start %q (expected HH:MM)", incoming.QuietHours.Start)
			}
			if incoming.QuietHours.End != "" && !models.IsValidTimeOfDay(incoming.QuietHours.End) {
				return nil, fmt.Errorf("invalid quiet_hours.end %q (expected HH:MM)", incoming.QuietHours.End)
			}
			if incoming.Sounds.Volume < 0 {
				incoming.Sounds.Volume = 0
			}
			if incoming.Sounds.Volume > 1 {
				incoming.Sounds.Volume = 1
			}
			settings.Defaults.Notifications = incoming
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

// ListAgents always returns every registered backend. Availability is reported
// as a per-agent `available` hint computed from ResolveExecutable with the
// user's current settings, but a missing binary NEVER removes an agent from
// the list. This is a deliberate architectural choice: filtering by binary
// availability at list time is what caused issue #29 (user installs Codex,
// it doesn't appear in the picker until the daemon restarts, or at all if
// Fedora's install path isn't in the resolver's fallback list). Pickers must
// surface every backend immediately so a freshly-installed CLI is selectable;
// the `available` hint lets the UI render a "(not installed)" badge rather
// than silently hide the option.
//
// Settings failures (and ResolveExecutable errors) are non-fatal here: we
// still enumerate the registry with available=false rather than break the
// picker because settings.yaml is unreadable.
func (s *settingsService) ListAgents(_ context.Context, _ *emptypb.Empty) (*pb.AgentList, error) {
	settings, _ := config.LoadSettings()
	backends := backend.List()
	agents := make([]*pb.AgentInfo, 0, len(backends))
	for _, b := range backends {
		_, resolveErr := b.ResolveExecutable(settings)
		agents = append(agents, &pb.AgentInfo{
			Name:        b.Name(),
			DisplayName: b.DisplayName(),
			Available:   resolveErr == nil,
		})
	}
	return &pb.AgentList{Agents: agents}, nil
}

// validateExecutablePath returns nil iff path points at a regular file the
// caller can execute. Mirrors the GUI's pre-save fs.access(path, X_OK) check
// so a malformed terminal_shell setting is rejected by both surfaces with the
// same semantics.
func validateExecutablePath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	// At least one execute bit set. We can't run access(2) directly without
	// CGo, but on POSIX a regular file with any X bit is generally executable
	// for someone — the kernel makes the final call when the user spawns it.
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("not executable")
	}
	return nil
}
