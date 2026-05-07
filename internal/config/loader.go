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

// SaveYAML saves a struct to a YAML file atomically: write to a sibling tmp
// file, fsync, then rename. POSIX rename is atomic on the same filesystem,
// so concurrent readers see either the old file or the new file — never a
// truncated/partial one. This closes a v5.x race where os.WriteFile's
// truncate-then-write window let SyncNextTaskNumber load a zero-valued
// struct and clobber project.yaml with `next_task_number: <N>` and zero
// values for every other field.
func SaveYAML(path string, v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to write temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to fsync temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close temp file %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("failed to chmod temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("failed to rename %s to %s: %w", tmpPath, path, err)
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
