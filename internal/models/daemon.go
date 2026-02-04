package models

import "time"

// DaemonInfo represents the daemon connection information.
// This corresponds to ~/.watchfire/daemon.yaml.
type DaemonInfo struct {
	Version   int       `yaml:"version"`
	Host      string    `yaml:"host"`
	Port      int       `yaml:"port"`
	PID       int       `yaml:"pid"`
	StartedAt time.Time `yaml:"started_at"`
}

// NewDaemonInfo creates a new daemon info with current values.
func NewDaemonInfo(host string, port, pid int) *DaemonInfo {
	return &DaemonInfo{
		Version:   1,
		Host:      host,
		Port:      port,
		PID:       pid,
		StartedAt: time.Now().UTC(),
	}
}
