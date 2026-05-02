package models

import (
	"regexp"
	"time"
)

// AgentConfig holds configuration for a specific coding agent.
type AgentConfig struct {
	Path string `yaml:"path"` // nil = lookup in PATH, or absolute path
}

// NotificationsEvents holds per-event toggles.
type NotificationsEvents struct {
	TaskFailed   bool `yaml:"task_failed"`
	RunComplete  bool `yaml:"run_complete"`
}

// NotificationsSounds holds sound prefs for notifications.
type NotificationsSounds struct {
	Enabled     bool    `yaml:"enabled"`
	TaskFailed  bool    `yaml:"task_failed"`
	RunComplete bool    `yaml:"run_complete"`
	Volume      float64 `yaml:"volume"`
}

// QuietHoursConfig defines a daily window where notifications are dropped.
// Start and End are HH:MM strings in local time. A window where Start > End
// (e.g. 22:00 → 08:00) wraps midnight.
type QuietHoursConfig struct {
	Enabled bool   `yaml:"enabled"`
	Start   string `yaml:"start"`
	End     string `yaml:"end"`
}

// NotificationsConfig holds the user's global notification preferences.
type NotificationsConfig struct {
	Enabled     bool                `yaml:"enabled"`
	Events      NotificationsEvents `yaml:"events"`
	Sounds      NotificationsSounds `yaml:"sounds"`
	QuietHours  QuietHoursConfig    `yaml:"quiet_hours"`
}

// DefaultNotifications returns the default notification preferences.
func DefaultNotifications() NotificationsConfig {
	return NotificationsConfig{
		Enabled: true,
		Events: NotificationsEvents{
			TaskFailed:  true,
			RunComplete: true,
		},
		Sounds: NotificationsSounds{
			Enabled:     true,
			TaskFailed:  true,
			RunComplete: true,
			Volume:      0.6,
		},
		QuietHours: QuietHoursConfig{
			Enabled: false,
			Start:   "22:00",
			End:     "08:00",
		},
	}
}

// DefaultsConfig holds default settings for new projects.
type DefaultsConfig struct {
	AutoMerge        bool                `yaml:"auto_merge"`
	AutoDeleteBranch bool                `yaml:"auto_delete_branch"`
	AutoStartTasks   bool                `yaml:"auto_start_tasks"`
	DefaultSandbox   string              `yaml:"default_sandbox"`
	DefaultAgent     string              `yaml:"default_agent"`
	Notifications    NotificationsConfig `yaml:"notifications"`
	// TerminalShell is the absolute path to the shell binary the GUI's
	// in-app terminal should spawn. Empty means "use $SHELL with login-shell
	// PATH detection" (issue #32). Persisted under defaults.terminal_shell.
	TerminalShell string `yaml:"terminal_shell"`
}

// UpdatesConfig holds settings for update checking.
type UpdatesConfig struct {
	CheckOnStartup bool       `yaml:"check_on_startup"`
	CheckFrequency string     `yaml:"check_frequency"` // "every_launch" | "daily" | "weekly"
	AutoDownload   bool       `yaml:"auto_download"`
	LastChecked    *time.Time `yaml:"last_checked,omitempty"`
}

// AppearanceConfig holds appearance settings.
type AppearanceConfig struct {
	Theme string `yaml:"theme"` // "system" | "light" | "dark"
}

// Settings represents global application settings.
// This corresponds to ~/.watchfire/settings.yaml.
type Settings struct {
	Version    int                     `yaml:"version"`
	Agents     map[string]*AgentConfig `yaml:"agents"`
	Defaults   DefaultsConfig          `yaml:"defaults"`
	Updates    UpdatesConfig           `yaml:"updates"`
	Appearance AppearanceConfig        `yaml:"appearance"`
}

// timeOfDayRe matches a HH:MM 24-hour time-of-day string.
var timeOfDayRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// IsValidTimeOfDay reports whether s is a HH:MM 24-hour string.
func IsValidTimeOfDay(s string) bool {
	return timeOfDayRe.MatchString(s)
}

// Normalize applies defaults to any zero-value notification subfields so a
// partial settings.yaml (e.g. one with no `notifications:` block at all) reads
// back as fully-populated defaults.
//
// The normalisation is keyed off "looks unset" — Volume==0 with Enabled==false
// and matching empty strings — so a deliberate user override that happens to
// equal the default sticks rather than being clobbered.
func (s *Settings) Normalize() {
	def := DefaultNotifications()
	n := &s.Defaults.Notifications

	// If the entire block looks unset (zero-value struct), apply all defaults.
	if !n.Enabled && !n.Events.TaskFailed && !n.Events.RunComplete &&
		!n.Sounds.Enabled && !n.Sounds.TaskFailed && !n.Sounds.RunComplete &&
		n.Sounds.Volume == 0 &&
		!n.QuietHours.Enabled && n.QuietHours.Start == "" && n.QuietHours.End == "" {
		s.Defaults.Notifications = def
		return
	}

	if n.QuietHours.Start == "" {
		n.QuietHours.Start = def.QuietHours.Start
	}
	if n.QuietHours.End == "" {
		n.QuietHours.End = def.QuietHours.End
	}
}

// NewSettings creates settings with default values.
func NewSettings() *Settings {
	return &Settings{
		Version: 1,
		Agents: map[string]*AgentConfig{
			"claude-code": {Path: ""}, // Empty means lookup in PATH
		},
		Defaults: DefaultsConfig{
			AutoMerge:        true,
			AutoDeleteBranch: true,
			AutoStartTasks:   true,
			DefaultSandbox:   "auto",
			DefaultAgent:     "claude-code",
			Notifications:    DefaultNotifications(),
		},
		Updates: UpdatesConfig{
			CheckOnStartup: true,
			CheckFrequency: "every_launch",
			AutoDownload:   true,
			LastChecked:    nil,
		},
		Appearance: AppearanceConfig{
			Theme: "system",
		},
	}
}
