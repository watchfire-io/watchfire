package relay

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"text/template"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

//go:embed templates/discord_task_failed.json.tmpl
var discordTaskFailedTmpl string

//go:embed templates/discord_run_complete.json.tmpl
var discordRunCompleteTmpl string

//go:embed templates/discord_weekly_digest.json.tmpl
var discordWeeklyDigestTmpl string

// discordEmbedDescriptionLimit is the defensive cap applied before
// posting to Discord. Discord's hard limit is 4096; we trim at 4000 and
// log a WARN so the user finds out a template overflow happened without
// the request being rejected by Discord.
const discordEmbedDescriptionLimit = 4000

// discordEmbedDescriptionEllipsis is appended after truncation to make
// the trim visible in the channel.
const discordEmbedDescriptionEllipsis = "…"

// DiscordAdapter is the v7.0 Relay adapter that turns canonical
// notifications into Discord webhook embeds. Each instance binds to a
// single endpoint (one Discord channel = one webhook URL); the
// dispatcher creates one DiscordAdapter per configured DiscordEndpoint
// and rebuilds the slice when integrations.yaml changes.
type DiscordAdapter struct {
	endpoint   models.DiscordEndpoint
	httpClient *http.Client
	logger     *log.Logger

	taskFailedTmpl   *template.Template
	runCompleteTmpl  *template.Template
	weeklyDigestTmpl *template.Template
}

// NewDiscordAdapter builds an adapter for the given Discord endpoint.
// Templates are parsed once at construction; subsequent Send() calls
// reuse the parsed templates so the per-notification cost stays low.
func NewDiscordAdapter(endpoint models.DiscordEndpoint, client *http.Client, logger *log.Logger) (*DiscordAdapter, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if logger == nil {
		logger = log.Default()
	}
	tf, err := template.New("discord_task_failed").Funcs(TemplateFuncs()).Parse(discordTaskFailedTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse discord_task_failed template: %w", err)
	}
	rc, err := template.New("discord_run_complete").Funcs(TemplateFuncs()).Parse(discordRunCompleteTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse discord_run_complete template: %w", err)
	}
	wd, err := template.New("discord_weekly_digest").Funcs(TemplateFuncs()).Parse(discordWeeklyDigestTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse discord_weekly_digest template: %w", err)
	}
	return &DiscordAdapter{
		endpoint:         endpoint,
		httpClient:       client,
		logger:           logger,
		taskFailedTmpl:   tf,
		runCompleteTmpl:  rc,
		weeklyDigestTmpl: wd,
	}, nil
}

// ID returns the stable id from the IntegrationsConfig entry.
func (d *DiscordAdapter) ID() string { return d.endpoint.ID }

// Kind reports the adapter kind for the dispatcher's per-kind routing.
func (d *DiscordAdapter) Kind() string { return "discord" }

// Supports gates the adapter on the per-endpoint event bitmask. Returns
// false for any kind the user has unchecked; the dispatcher skips Send
// without ever opening a connection.
func (d *DiscordAdapter) Supports(kind notify.Kind) bool {
	switch kind {
	case notify.KindTaskFailed:
		return d.endpoint.EnabledEvents.TaskFailed
	case notify.KindRunComplete:
		return d.endpoint.EnabledEvents.RunComplete
	case notify.KindWeeklyDigest:
		return d.endpoint.EnabledEvents.WeeklyDigest
	}
	return false
}

// IsProjectMuted reports whether the source project sits inside the
// adapter's per-project mute list. The dispatcher checks this before
// calling Send so muted projects never reach the network.
func (d *DiscordAdapter) IsProjectMuted(projectID string) bool {
	return IsProjectMuted(d.endpoint.ProjectMuteIDs, projectID)
}

// Send renders the appropriate template, applies defensive truncation
// to embed descriptions over discordEmbedDescriptionLimit, and POSTs to
// the configured webhook URL. Discord webhooks return 204 on success;
// any 4xx / 5xx is surfaced as an error so the dispatcher's retry +
// circuit-breaker can act on it.
func (d *DiscordAdapter) Send(ctx context.Context, p Payload) error {
	if d.endpoint.URL == "" {
		return fmt.Errorf("discord adapter %q: webhook URL not resolved (keyring miss?)", d.endpoint.ID)
	}

	tmpl, err := d.templateFor(notify.Kind(p.Kind))
	if err != nil {
		return err
	}
	body, renderErr := d.render(tmpl, p)
	if renderErr != nil {
		return renderErr
	}

	body = d.truncateEmbedDescriptions(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord adapter %q: build request: %w", d.endpoint.ID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "watchfire-relay/1")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord adapter %q: POST: %w", d.endpoint.ID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord adapter %q: HTTP %d", d.endpoint.ID, resp.StatusCode)
	}
	return nil
}

func (d *DiscordAdapter) templateFor(kind notify.Kind) (*template.Template, error) {
	switch kind {
	case notify.KindTaskFailed:
		return d.taskFailedTmpl, nil
	case notify.KindRunComplete:
		return d.runCompleteTmpl, nil
	case notify.KindWeeklyDigest:
		return d.weeklyDigestTmpl, nil
	}
	return nil, fmt.Errorf("discord adapter %q: unsupported notification kind %q", d.endpoint.ID, kind)
}

func (d *DiscordAdapter) render(tmpl *template.Template, p Payload) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("discord adapter %q: render template %q: %w", d.endpoint.ID, tmpl.Name(), err)
	}
	return buf.Bytes(), nil
}

// truncateEmbedDescriptions walks the rendered JSON, trims any embed
// description longer than discordEmbedDescriptionLimit, and re-marshals.
// On parse failure (rendered body wasn't valid JSON, which should never
// happen for templates that pass golden tests) the original bytes are
// returned untouched and a WARN is logged so the upstream Send still
// has something to POST and the operator can see the broken template.
func (d *DiscordAdapter) truncateEmbedDescriptions(body []byte) []byte {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		d.logger.Printf("WARN: discord adapter %q: rendered body not valid JSON, posting unchanged: %v", d.endpoint.ID, err)
		return body
	}
	embeds, ok := doc["embeds"].([]any)
	if !ok || len(embeds) == 0 {
		return body
	}
	truncated := false
	for i, raw := range embeds {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		desc, ok := e["description"].(string)
		if !ok {
			continue
		}
		if runeCount(desc) <= discordEmbedDescriptionLimit {
			continue
		}
		e["description"] = trimRunes(desc, discordEmbedDescriptionLimit) + discordEmbedDescriptionEllipsis
		embeds[i] = e
		truncated = true
	}
	if !truncated {
		return body
	}
	d.logger.Printf("WARN: discord adapter %q: truncated embed description over %d chars", d.endpoint.ID, discordEmbedDescriptionLimit)
	doc["embeds"] = embeds
	out, err := json.Marshal(doc)
	if err != nil {
		d.logger.Printf("WARN: discord adapter %q: re-marshal after truncation failed, posting original: %v", d.endpoint.ID, err)
		return body
	}
	return out
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func trimRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	cut := 0
	for i := range s {
		if cut == n {
			return s[:i]
		}
		cut++
	}
	return s
}

// Compile-time assertion that DiscordAdapter satisfies the Adapter
// interface — the dispatcher (task 0062) iterates `[]Adapter` so this
// catches accidental signature drift at build time.
var _ Adapter = (*DiscordAdapter)(nil)
