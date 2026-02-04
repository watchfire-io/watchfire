package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadYAML loads a YAML file into the provided struct.
func LoadYAML(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to parse YAML from %s: %w", path, err)
	}
	return nil
}

// SaveYAML saves a struct to a YAML file.
func SaveYAML(path string, v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}

// FileExists checks if a file exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadYAMLOrDefault loads a YAML file, or returns default if file doesn't exist.
func LoadYAMLOrDefault[T any](path string, defaultFn func() *T) (*T, error) {
	if !FileExists(path) {
		return defaultFn(), nil
	}

	var v T
	if err := LoadYAML(path, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
