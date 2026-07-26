package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/posthog/posthog-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/analytics"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/mcpserver/install"
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
			if incoming.DigestSchedule != "" {
				if _, ok := models.ParseDigestSchedule(incoming.DigestSchedule); !ok {
					return nil, fmt.Errorf("invalid digest_schedule %q (expected MON HH:MM / DAILY HH:MM)", incoming.DigestSchedule)
				}
			} else {
				incoming.DigestSchedule = models.DefaultDigestSchedule
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

// GetMcpClientStatus reports the MCP onboarding state of every known coding
// agent harness on this machine (v9.0 Firestorm), plus the generic snippet for
// the Custom option so the CLI, TUI and GUI all render that from one source of
// truth.
//
// The daemon runs as the invoking user, which is why it can answer this at all:
// detection and configuration both live in user-level config under $HOME. It is
// a pure read — no network, no writes.
func (s *settingsService) GetMcpClientStatus(_ context.Context, _ *emptypb.Empty) (*pb.McpClientStatusList, error) {
	clients := install.Clients()
	out := make([]*pb.McpClientStatus, 0, len(clients))
	for _, c := range clients {
		st := c.Status()
		out = append(out, &pb.McpClientStatus{
			Client:      c.ID,
			DisplayName: c.DisplayName,
			Detected:    st.Detected,
			Configured:  st.Configured,
			ConfigPath:  st.ConfigPath,
			Message:     mcpStatusMessage(c.DisplayName, st),
		})
	}
	return &pb.McpClientStatusList{
		Clients:       out,
		CustomSnippet: install.CustomSnippet(),
	}, nil
}

// InstallMcpClient registers `watchfire mcp serve` with one known MCP client by
// merging a watchfire entry into that client's user-level config — the same
// best-effort, idempotent write path as `watchfire mcp install`.
//
// Install problems are NOT gRPC errors. A missing harness or an unparseable
// config comes back as a normal response with configured=false and a message
// carrying the manual snippet, so the TUI/GUI can render the fallback path
// instead of an opaque error toast. Only an unknown client key — a caller bug —
// is an error.
func (s *settingsService) InstallMcpClient(_ context.Context, req *pb.InstallMcpClientRequest) (*pb.McpClientStatus, error) {
	client, ok := install.Get(req.GetClient())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unknown MCP client %q", req.GetClient())
	}

	res := client.Install()
	st := client.Status()

	// Installers know the exact file they targeted; Status only knows where the
	// config is expected to live. Prefer the former when it is populated.
	configPath := res.ConfigPath
	if configPath == "" {
		configPath = st.ConfigPath
	}

	analytics.Track("mcp_client_installed", posthog.NewProperties().
		Set("origin", req.GetMeta().GetOrigin()).
		Set("client", client.ID).
		Set("action", string(res.Action)))

	return &pb.McpClientStatus{
		Client:      client.ID,
		DisplayName: client.DisplayName,
		Detected:    st.Detected,
		Configured:  st.Configured,
		ConfigPath:  configPath,
		Message:     res.Message(client.DisplayName),
	}, nil
}

// mcpStatusMessage renders the read-only status line for a client. Badges come
// from detected/configured; this explains what installing would actually do,
// including the "you'll get a snippet instead" case for an absent harness.
func mcpStatusMessage(displayName string, st install.Status) string {
	switch {
	case st.Configured:
		return fmt.Sprintf("Configured in %s. Restart %s if it is not picking Watchfire up.", st.ConfigPath, displayName)
	case st.Detected:
		return fmt.Sprintf("Detected. Installing adds the watchfire entry to %s.", st.ConfigPath)
	default:
		return fmt.Sprintf("%s was not found on this machine. Installing returns the snippet to add to %s by hand.", displayName, st.ConfigPath)
	}
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
