package models

import "time"

// AgentConfig holds configuration for a specific coding agent.
type AgentConfig struct {
	Path string `yaml:"path"` // nil = lookup in PATH, or absolute path
}

// DefaultsConfig holds default settings for new projects.
type DefaultsConfig struct {
	AutoMerge        bool   `yaml:"auto_merge"`
	AutoDeleteBranch bool   `yaml:"auto_delete_branch"`
	AutoStartTasks   bool   `yaml:"auto_start_tasks"`
	DefaultBranch    string `yaml:"default_branch"`
	DefaultSandbox   string `yaml:"default_sandbox"`
	DefaultAgent     string `yaml:"default_agent"`
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
			DefaultBranch:    "main",
			DefaultSandbox:   "sandbox-exec",
			DefaultAgent:     "claude-code",
		},
		Updates: UpdatesConfig{
			CheckOnStartup: true,
			CheckFrequency: "every_launch",
			AutoDownload:   false,
			LastChecked:    nil,
		},
		Appearance: AppearanceConfig{
			Theme: "system",
		},
	}
}
