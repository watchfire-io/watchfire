package config

import (
	"github.com/watchfire-io/watchfire/internal/models"
)

// LoadSettings loads the global settings from ~/.watchfire/settings.yaml.
// If the file doesn't exist, returns default settings.
func LoadSettings() (*models.Settings, error) {
	path, err := GlobalSettingsFile()
	if err != nil {
		return nil, err
	}
	return LoadYAMLOrDefault(path, models.NewSettings)
}

// SaveSettings saves the global settings to ~/.watchfire/settings.yaml.
func SaveSettings(settings *models.Settings) error {
	path, err := GlobalSettingsFile()
	if err != nil {
		return err
	}
	return SaveYAML(path, settings)
}
