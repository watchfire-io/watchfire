package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
)

// Kind enumerates the notification kinds the daemon emits.
// String values mirror the proto enum names so the JSONL fallback file is
// portable across surfaces (GUI gRPC stream, tray menu reader).
type Kind string

const (
	KindTaskFailed  Kind = "TASK_FAILED"
	KindRunComplete Kind = "RUN_COMPLETE"
)

// Notification is a single notification event fanned out over the Bus.
type Notification struct {
	ID         string    `json:"id"`
	Kind       Kind      `json:"kind"`
	ProjectID  string    `json:"project_id"`
	TaskNumber int32     `json:"task_number"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	EmittedAt  time.Time `json:"emitted_at"`
}

// Bus fans Notification events out to in-process subscribers (gRPC streams,
// tests). Modeled on the watcher's channel-fan-out pattern: subscribers get a
// buffered channel; slow consumers drop events silently rather than blocking
// the emitter.
type Bus struct {
	mu          sync.Mutex
	subscribers map[chan Notification]struct{}
}

// NewBus creates an empty notification bus.
func NewBus() *Bus {
	return &Bus{subscribers: make(map[chan Notification]struct{})}
}

// Subscribe returns a buffered channel that receives every emitted
// Notification. The returned cancel function unsubscribes and closes the
// channel; callers must invoke it to avoid leaking goroutines / channels.
func (b *Bus) Subscribe() (<-chan Notification, func()) {
	if b == nil {
		ch := make(chan Notification)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan Notification, 16)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Emit fans the notification out to every current subscriber. A non-blocking
// send is used so a stalled subscriber cannot wedge the emitter; dropped
// events for slow consumers are deliberate (the headless fallback log is the
// durable record).
func (b *Bus) Emit(n Notification) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		select {
		case ch <- n:
		default:
		}
	}
}

// MakeID derives a stable notification ID from kind + project_id + task_number
// + emitted_at. A duplicate emission for the same (kind, project, task,
// second) tick collapses to the same id, which lets downstream consumers
// dedupe deterministically without coordinating with the emitter.
func MakeID(kind Kind, projectID string, taskNumber int32, emittedAt time.Time) string {
	payload := fmt.Sprintf("%s|%s|%d|%d", kind, projectID, taskNumber, emittedAt.Unix())
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:8])
}

// AppendLogLine appends a single Notification record to the project's
// `~/.watchfire/logs/<project_id>/notifications.log` (one JSON object per
// line, append-only). This is the headless-fallback path consumed by the
// system tray (`internal/daemon/tray/notifications.go`) when no GUI client
// is subscribed to the live bus.
func AppendLogLine(n Notification) error {
	if n.ProjectID == "" {
		return fmt.Errorf("notification has no project_id")
	}
	if err := config.EnsureGlobalLogsDir(); err != nil {
		return err
	}
	logsDir, err := config.GlobalLogsDir()
	if err != nil {
		return err
	}
	projectLogsDir := filepath.Join(logsDir, n.ProjectID)
	if err := os.MkdirAll(projectLogsDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(projectLogsDir, "notifications.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}
