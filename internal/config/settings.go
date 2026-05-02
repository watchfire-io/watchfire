package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/watchfire-io/watchfire/internal/models"
)

// LoadSettings loads the global settings from ~/.watchfire/settings.yaml.
// If the file doesn't exist, returns default settings.
func LoadSettings() (*models.Settings, error) {
	path, err := GlobalSettingsFile()
	if err != nil {
		return nil, err
	}
	s, err := LoadYAMLOrDefault(path, models.NewSettings)
	if err != nil {
		return nil, err
	}
	s.Normalize()
	return s, nil
}

// SaveSettings saves the global settings to ~/.watchfire/settings.yaml.
func SaveSettings(settings *models.Settings) error {
	path, err := GlobalSettingsFile()
	if err != nil {
		return err
	}
	return SaveYAML(path, settings)
}

// InstallationIDFile returns the path to ~/.watchfire/installation_id.
func InstallationIDFile() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "installation_id"), nil
}

// LoadInstallationID reads the installation ID from its dedicated file.
// If the file doesn't exist, it migrates from settings.yaml or generates a new UUID.
func LoadInstallationID() (string, error) {
	path, err := InstallationIDFile()
	if err != nil {
		return "", err
	}

	// Try reading existing file
	data, err := os.ReadFile(path)
	if err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	}

	// Try migrating from settings.yaml
	id := migrateInstallationIDFromSettings()
	if id == "" {
		id = uuid.New().String()
	}

	// Save to dedicated file
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	return id, nil
}

// migrateInstallationIDFromSettings checks settings.yaml for a legacy installation_id field.
func migrateInstallationIDFromSettings() string {
	path, err := GlobalSettingsFile()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return ""
	}
	if id, ok := raw["installation_id"].(string); ok && id != "" {
		return id
	}
	return ""
}
