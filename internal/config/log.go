package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
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
	if mkErr := os.MkdirAll(projectLogsDir, 0o755); mkErr != nil {
		return nil, fmt.Errorf("failed to create project logs dir: %w", mkErr)
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
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	_, _ = fmt.Fprintln(w, "---")
	_, _ = fmt.Fprintf(w, "project_id: %s\n", projectID)
	_, _ = fmt.Fprintf(w, "task_number: %d\n", taskNumber)
	_, _ = fmt.Fprintf(w, "session_number: %d\n", sessionNumber)
	_, _ = fmt.Fprintf(w, "agent: %s\n", agent)
	_, _ = fmt.Fprintf(w, "mode: %s\n", mode)
	_, _ = fmt.Fprintf(w, "started_at: %s\n", entry.StartedAt)
	_, _ = fmt.Fprintf(w, "ended_at: %s\n", entry.EndedAt)
	_, _ = fmt.Fprintf(w, "status: %s\n", status)
	_, _ = fmt.Fprintln(w, "---")

	for _, line := range scrollback {
		_, _ = fmt.Fprintln(w, line)
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

	logs := make([]*models.LogEntry, 0, len(dirEntries))
	for _, e := range dirEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}

		entry, err := parseLogHeader(filepath.Join(projectLogsDir, e.Name()))
		if err != nil {
			continue
		}

		// Check if a JSONL transcript exists alongside the .log file
		jsonlName := strings.TrimSuffix(e.Name(), ".log") + ".jsonl"
		if _, statErr := os.Stat(filepath.Join(projectLogsDir, jsonlName)); statErr == nil {
			entry.HasTranscript = true
		}

		logs = append(logs, entry)
	}

	sort.Slice(logs, func(i, j int) bool {
		return logs[i].StartedAt > logs[j].StartedAt
	})

	return logs, nil
}

// ReadLog reads a specific log file and returns metadata + content.
// Prefers JSONL transcript if available, falls back to raw scrollback log.
func ReadLog(projectID, logID string) (*models.LogEntry, string, error) {
	logsDir, err := GlobalLogsDir()
	if err != nil {
		return nil, "", err
	}

	// Try JSONL transcript first
	jsonlPath := filepath.Join(logsDir, projectID, logID+".jsonl")
	logPath := filepath.Join(logsDir, projectID, logID+".log")

	// Read metadata from the .log file (always present)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, "", fmt.Errorf("log not found: %w", err)
	}

	entry, fallbackBody := parseLogContent(string(data))
	if entry == nil {
		return nil, "", fmt.Errorf("invalid log format")
	}

	// If JSONL transcript exists, format and return it. Format depends on
	// which agent produced the transcript, so dispatch via the backend
	// registry keyed by the recorded agent name.
	if _, statErr := os.Stat(jsonlPath); statErr == nil {
		entry.HasTranscript = true
		if be, ok := backend.Get(entry.Agent); ok {
			formatted, fmtErr := be.FormatTranscript(jsonlPath)
			if fmtErr == nil && formatted != "" {
				return entry, formatted, nil
			}
		}
	}

	return entry, fallbackBody, nil
}

func parseLogHeader(path string) (*models.LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

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

func parseLogContent(content string) (entry *models.LogEntry, body string) {
	lines := strings.Split(content, "\n")
	entry = &models.LogEntry{}
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

	body = strings.Join(lines[headerEnd+1:], "\n")
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
		_, _ = fmt.Sscanf(val, "%d", &entry.TaskNumber)
	case "session_number":
		_, _ = fmt.Sscanf(val, "%d", &entry.SessionNumber)
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

// CopyTranscript copies a JSONL transcript file to the watchfire logs directory.
func CopyTranscript(projectID, logID, srcPath string) error {
	logsDir, err := GlobalLogsDir()
	if err != nil {
		return err
	}

	dstPath := filepath.Join(logsDir, projectID, logID+".jsonl")

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()

	_, err = io.Copy(dst, src)
	return err
}

