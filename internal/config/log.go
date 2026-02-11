package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// WriteLog writes a session log to disk with YAML header + scrollback content.
func WriteLog(projectID string, taskNumber, sessionNumber int, agent, mode, status string, startedAt time.Time, scrollback []string) (*models.LogEntry, error) {
	if err := EnsureGlobalLogsDir(); err != nil {
		return nil, fmt.Errorf("failed to ensure logs dir: %w", err)
	}

	logsDir, err := GlobalLogsDir()
	if err != nil {
		return nil, err
	}

	projectLogsDir := filepath.Join(logsDir, projectID)
	if err := os.MkdirAll(projectLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create project logs dir: %w", err)
	}

	endedAt := time.Now().UTC()
	timestamp := startedAt.Format("2006-01-02T15-04-05")

	var logID string
	if taskNumber > 0 {
		logID = fmt.Sprintf("%04d-%d-%s", taskNumber, sessionNumber, timestamp)
	} else {
		logID = fmt.Sprintf("%s-%d-%s", mode, sessionNumber, timestamp)
	}

	entry := &models.LogEntry{
		LogID:         logID,
		ProjectID:     projectID,
		TaskNumber:    taskNumber,
		SessionNumber: sessionNumber,
		Agent:         agent,
		Mode:          mode,
		StartedAt:     startedAt.UTC().Format(time.RFC3339),
		EndedAt:       endedAt.Format(time.RFC3339),
		Status:        status,
	}

	filePath := filepath.Join(projectLogsDir, logID+".log")
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "---")
	fmt.Fprintf(w, "project_id: %s\n", projectID)
	fmt.Fprintf(w, "task_number: %d\n", taskNumber)
	fmt.Fprintf(w, "session_number: %d\n", sessionNumber)
	fmt.Fprintf(w, "agent: %s\n", agent)
	fmt.Fprintf(w, "mode: %s\n", mode)
	fmt.Fprintf(w, "started_at: %s\n", entry.StartedAt)
	fmt.Fprintf(w, "ended_at: %s\n", entry.EndedAt)
	fmt.Fprintf(w, "status: %s\n", status)
	fmt.Fprintln(w, "---")

	for _, line := range scrollback {
		fmt.Fprintln(w, line)
	}

	return entry, w.Flush()
}

// ListLogs reads all log files for a project and returns their metadata (newest first).
func ListLogs(projectID string) ([]*models.LogEntry, error) {
	logsDir, err := GlobalLogsDir()
	if err != nil {
		return nil, err
	}

	projectLogsDir := filepath.Join(logsDir, projectID)
	dirEntries, err := os.ReadDir(projectLogsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var logs []*models.LogEntry
	for _, e := range dirEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}

		entry, err := parseLogHeader(filepath.Join(projectLogsDir, e.Name()))
		if err != nil {
			continue
		}
		logs = append(logs, entry)
	}

	sort.Slice(logs, func(i, j int) bool {
		return logs[i].StartedAt > logs[j].StartedAt
	})

	return logs, nil
}

// ReadLog reads a specific log file and returns metadata + content.
func ReadLog(projectID, logID string) (*models.LogEntry, string, error) {
	logsDir, err := GlobalLogsDir()
	if err != nil {
		return nil, "", err
	}

	filePath := filepath.Join(logsDir, projectID, logID+".log")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("log not found: %w", err)
	}

	entry, body := parseLogContent(string(data))
	if entry == nil {
		return nil, "", fmt.Errorf("invalid log format")
	}

	return entry, body, nil
}

func parseLogHeader(path string) (*models.LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	entry := &models.LogEntry{}
	inHeader := false

	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			if !inHeader {
				inHeader = true
				continue
			}
			break
		}
		if inHeader {
			parseLogHeaderLine(entry, line)
		}
	}

	if entry.LogID == "" {
		entry.LogID = strings.TrimSuffix(filepath.Base(path), ".log")
	}

	return entry, nil
}

func parseLogContent(content string) (*models.LogEntry, string) {
	lines := strings.Split(content, "\n")
	entry := &models.LogEntry{}
	headerEnd := -1
	inHeader := false

	for i, line := range lines {
		if line == "---" {
			if !inHeader {
				inHeader = true
				continue
			}
			headerEnd = i
			break
		}
		if inHeader {
			parseLogHeaderLine(entry, line)
		}
	}

	if headerEnd < 0 {
		return nil, ""
	}

	body := strings.Join(lines[headerEnd+1:], "\n")
	return entry, body
}

func parseLogHeaderLine(entry *models.LogEntry, line string) {
	parts := strings.SplitN(line, ": ", 2)
	if len(parts) != 2 {
		return
	}
	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])

	switch key {
	case "project_id":
		entry.ProjectID = val
	case "task_number":
		fmt.Sscanf(val, "%d", &entry.TaskNumber)
	case "session_number":
		fmt.Sscanf(val, "%d", &entry.SessionNumber)
	case "agent":
		entry.Agent = val
	case "mode":
		entry.Mode = val
	case "started_at":
		entry.StartedAt = val
	case "ended_at":
		entry.EndedAt = val
	case "status":
		entry.Status = val
	}
}
