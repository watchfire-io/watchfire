package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
)

// Adapter is the contract every outbound integration implements. Each
// adapter owns its own Send() and reports the integration kind plus an
// event-bitmask gate via Supports(). The dispatcher (task 0062) iterates
// the registered adapters per Notification, applies the per-project mute,
// and runs Send() under its retry + circuit-breaker policy. Adapters
// must be safe to call concurrently from the dispatcher goroutine.
type Adapter interface {
	ID() string
	Kind() string
	Supports(notify.Kind) bool
	Send(ctx context.Context, p Payload) error
}

// Payload is the canonical, adapter-agnostic shape rendered into each
// outbound integration. The dispatcher enriches a notify.Notification
// with project + task metadata before passing it through the adapters
// (project name + color come from the project store; failure reason +
// task title come from the task YAML). Adapters consume the Payload via
// text/template and produce their channel-specific wire format.
type Payload struct {
	Version           int       `json:"version"`
	Kind              string    `json:"kind"`
	EmittedAt         time.Time `json:"emitted_at"`
	ProjectID         string    `json:"project_id"`
	ProjectName       string    `json:"project_name"`
	ProjectColor      string    `json:"project_color,omitempty"`
	TaskNumber        int       `json:"task_number,omitempty"`
	TaskTitle         string    `json:"task_title,omitempty"`
	TaskFailureReason string    `json:"task_failure_reason,omitempty"`
	DeepLink          string    `json:"deep_link"`
	DigestDate        string    `json:"digest_date,omitempty"`
	DigestPath        string    `json:"digest_path,omitempty"`
	DigestBody        string    `json:"digest_body,omitempty"`
}

// PayloadInput carries everything BuildPayload needs to derive a Payload
// from a notification. The dispatcher (task 0062) is responsible for
// resolving project name/color and task metadata before calling.
type PayloadInput struct {
	Notification      notify.Notification
	ProjectName       string
	ProjectColor      string
	TaskTitle         string
	TaskFailureReason string
	DigestDate        string
	DigestPath        string
	DigestBody        string
}

// BuildPayload turns a PayloadInput into a canonical Payload. The deep
// link is derived from the notification's project + task fields:
// `watchfire://project/<id>/task/<n>` for task-bound notifications, or
// `watchfire://digest/<date>` for the weekly digest.
func BuildPayload(in PayloadInput) Payload {
	n := in.Notification
	deepLink := fmt.Sprintf("watchfire://project/%s/task/%04d", n.ProjectID, n.TaskNumber)
	if n.Kind == notify.KindWeeklyDigest {
		date := in.DigestDate
		if date == "" {
			date = n.EmittedAt.UTC().Format("2006-01-02")
		}
		deepLink = "watchfire://digest/" + date
	}
	return Payload{
		Version:           1,
		Kind:              string(n.Kind),
		EmittedAt:         n.EmittedAt,
		ProjectID:         n.ProjectID,
		ProjectName:       in.ProjectName,
		ProjectColor:      in.ProjectColor,
		TaskNumber:        int(n.TaskNumber),
		TaskTitle:         in.TaskTitle,
		TaskFailureReason: in.TaskFailureReason,
		DeepLink:          deepLink,
		DigestDate:        in.DigestDate,
		DigestPath:        in.DigestPath,
		DigestBody:        in.DigestBody,
	}
}

// IsProjectMuted reports whether the given project ID appears in the
// adapter's mute list. Pulled out as a helper so each adapter package
// reuses the same membership check (and so the dispatcher can short
// the loop without invoking Send).
func IsProjectMuted(muteIDs []string, projectID string) bool {
	for _, id := range muteIDs {
		if id == projectID {
			return true
		}
	}
	return false
}
