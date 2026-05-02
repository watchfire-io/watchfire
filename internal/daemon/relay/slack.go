package relay

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"text/template"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

//go:embed templates/slack_task_failed.json.tmpl
var slackTaskFailedTmpl string

//go:embed templates/slack_run_complete.json.tmpl
var slackRunCompleteTmpl string

//go:embed templates/slack_weekly_digest.json.tmpl
var slackWeeklyDigestTmpl string

// SlackAdapter renders v7.0 Relay notifications as Block Kit messages
// and POSTs them to a Slack incoming-webhook URL. One adapter binds to
// one endpoint (one webhook URL = one Slack channel); the dispatcher
// builds one SlackAdapter per configured `models.SlackEndpoint` and
// rebuilds the slice when integrations.yaml changes.
//
// Authentication is by URL secrecy alone — Slack incoming webhooks have
// no signing scheme, so the URL itself lives in the OS keyring and is
// resolved at load time. A missing keyring entry surfaces as an explicit
// Send error instead of a silent no-op so the user finds out the channel
// is broken on the first attempt.
type SlackAdapter struct {
	endpoint   models.SlackEndpoint
	httpClient *http.Client
	logger     *log.Logger

	taskFailedTmpl   *template.Template
	runCompleteTmpl  *template.Template
	weeklyDigestTmpl *template.Template
}

// NewSlackAdapter parses the three embedded Block Kit templates once
// and returns a ready-to-use adapter. The HTTP client and logger fall
// back to sane defaults so production callers can pass nil.
func NewSlackAdapter(endpoint models.SlackEndpoint, client *http.Client, logger *log.Logger) (*SlackAdapter, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if logger == nil {
		logger = log.Default()
	}
	tf, err := template.New("slack_task_failed").Funcs(TemplateFuncs()).Parse(slackTaskFailedTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse slack_task_failed template: %w", err)
	}
	rc, err := template.New("slack_run_complete").Funcs(TemplateFuncs()).Parse(slackRunCompleteTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse slack_run_complete template: %w", err)
	}
	wd, err := template.New("slack_weekly_digest").Funcs(TemplateFuncs()).Parse(slackWeeklyDigestTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse slack_weekly_digest template: %w", err)
	}
	return &SlackAdapter{
		endpoint:         endpoint,
		httpClient:       client,
		logger:           logger,
		taskFailedTmpl:   tf,
		runCompleteTmpl:  rc,
		weeklyDigestTmpl: wd,
	}, nil
}

// ID returns the stable id from the IntegrationsConfig entry.
func (s *SlackAdapter) ID() string { return s.endpoint.ID }

// Kind reports the adapter kind for the dispatcher's per-kind routing.
func (s *SlackAdapter) Kind() string { return "slack" }

// Supports gates the adapter on the per-endpoint event bitmask. The
// dispatcher skips Send entirely (no connection opened) when this
// returns false.
func (s *SlackAdapter) Supports(kind notify.Kind) bool {
	switch kind {
	case notify.KindTaskFailed:
		return s.endpoint.EnabledEvents.TaskFailed
	case notify.KindRunComplete:
		return s.endpoint.EnabledEvents.RunComplete
	case notify.KindWeeklyDigest:
		return s.endpoint.EnabledEvents.WeeklyDigest
	}
	return false
}

// IsProjectMuted reports whether the source project sits inside the
// adapter's per-project mute list. The dispatcher checks this before
// calling Send so muted projects never reach the network.
func (s *SlackAdapter) IsProjectMuted(projectID string) bool {
	return IsProjectMuted(s.endpoint.ProjectMuteIDs, projectID)
}

// Send renders the appropriate template and POSTs the resulting Block
// Kit JSON. Slack incoming webhooks return 200 OK on success; any
// non-2xx is surfaced as an error so the dispatcher's retry +
// circuit-breaker can act on it.
func (s *SlackAdapter) Send(ctx context.Context, p Payload) error {
	if s.endpoint.URL == "" {
		return fmt.Errorf("slack adapter %q: webhook URL not resolved (keyring miss?)", s.endpoint.ID)
	}

	tmpl, err := s.templateFor(notify.Kind(p.Kind))
	if err != nil {
		return err
	}
	body, err := s.render(tmpl, p)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack adapter %q: build request: %w", s.endpoint.ID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "watchfire-relay/1")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack adapter %q: POST: %w", s.endpoint.ID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack adapter %q: HTTP %d", s.endpoint.ID, resp.StatusCode)
	}
	return nil
}

func (s *SlackAdapter) templateFor(kind notify.Kind) (*template.Template, error) {
	switch kind {
	case notify.KindTaskFailed:
		return s.taskFailedTmpl, nil
	case notify.KindRunComplete:
		return s.runCompleteTmpl, nil
	case notify.KindWeeklyDigest:
		return s.weeklyDigestTmpl, nil
	}
	return nil, fmt.Errorf("slack adapter %q: unsupported notification kind %q", s.endpoint.ID, kind)
}

func (s *SlackAdapter) render(tmpl *template.Template, p Payload) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("slack adapter %q: render template %q: %w", s.endpoint.ID, tmpl.Name(), err)
	}
	return buf.Bytes(), nil
}

// Compile-time assertion that SlackAdapter satisfies the Adapter
// interface — the dispatcher iterates `[]Adapter` so this catches
// accidental signature drift at build time.
var _ Adapter = (*SlackAdapter)(nil)
