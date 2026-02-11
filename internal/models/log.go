package models

// LogEntry represents metadata for a single agent session log.
type LogEntry struct {
	LogID         string `yaml:"log_id"`
	ProjectID     string `yaml:"project_id"`
	TaskNumber    int    `yaml:"task_number"`
	SessionNumber int    `yaml:"session_number"`
	Agent         string `yaml:"agent"`
	Mode          string `yaml:"mode"`
	StartedAt     string `yaml:"started_at"`
	EndedAt       string `yaml:"ended_at"`
	Status        string `yaml:"status"`
}
