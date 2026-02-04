package config

import (
	"os"
	"syscall"

	"github.com/watchfire-io/watchfire/internal/models"
)

// LoadDaemonInfo loads the daemon connection info from ~/.watchfire/daemon.yaml.
// Returns nil if the file doesn't exist.
func LoadDaemonInfo() (*models.DaemonInfo, error) {
	path, err := GlobalDaemonFile()
	if err != nil {
		return nil, err
	}

	if !FileExists(path) {
		return nil, nil
	}

	var info models.DaemonInfo
	if err := LoadYAML(path, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SaveDaemonInfo saves the daemon connection info to ~/.watchfire/daemon.yaml.
func SaveDaemonInfo(info *models.DaemonInfo) error {
	if err := EnsureGlobalDir(); err != nil {
		return err
	}

	path, err := GlobalDaemonFile()
	if err != nil {
		return err
	}
	return SaveYAML(path, info)
}

// RemoveDaemonInfo removes the daemon.yaml file.
func RemoveDaemonInfo() error {
	path, err := GlobalDaemonFile()
	if err != nil {
		return err
	}

	if !FileExists(path) {
		return nil
	}
	return os.Remove(path)
}

// IsDaemonRunning checks if the daemon process is still running.
// Returns true if daemon.yaml exists and the PID is alive.
func IsDaemonRunning() (bool, *models.DaemonInfo, error) {
	info, err := LoadDaemonInfo()
	if err != nil {
		return false, nil, err
	}
	if info == nil {
		return false, nil, nil
	}

	// Check if process is alive using kill -0
	process, err := os.FindProcess(info.PID)
	if err != nil {
		// On Unix, FindProcess always succeeds
		return false, info, nil
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		// Process doesn't exist, clean up stale file
		_ = RemoveDaemonInfo()
		return false, info, nil
	}

	return true, info, nil
}
