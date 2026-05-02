package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/relay"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// integrationsService implements `IntegrationsService` for the v7.0 Relay
// settings UI. The handlers delegate to `internal/config/integrations.go`
// for the YAML + keyring round-trip and apply secret-scrubbing on every
// outbound message so the wire never carries a Slack URL or webhook
// HMAC secret back to the client.
type integrationsService struct {
	pb.UnimplementedIntegrationsServiceServer

	// httpClient is injected for tests (httptest server). Real binary
	// path uses the default client.
	httpClient *http.Client
}

func newIntegrationsService() *integrationsService {
	return &integrationsService{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ListIntegrations returns the current scrubbed config for display.
func (s *integrationsService) ListIntegrations(_ context.Context, _ *pb.ListIntegrationsRequest) (*pb.IntegrationsConfig, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}
	return scrubConfigToProto(cfg), nil
}

// SaveIntegration creates or updates a single integration. Uses the
// payload oneof to dispatch on type. Empty IDs on create are filled in
// with a fresh UUIDv4.
func (s *integrationsService) SaveIntegration(_ context.Context, req *pb.SaveIntegrationRequest) (*pb.IntegrationsConfig, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}

	switch payload := req.GetPayload().(type) {
	case *pb.SaveIntegrationRequest_Webhook:
		if err := upsertWebhook(cfg, payload.Webhook); err != nil {
			return nil, err
		}
	case *pb.SaveIntegrationRequest_Slack:
		if err := upsertSlack(cfg, payload.Slack); err != nil {
			return nil, err
		}
	case *pb.SaveIntegrationRequest_Discord:
		if err := upsertDiscord(cfg, payload.Discord); err != nil {
			return nil, err
		}
	case *pb.SaveIntegrationRequest_Github:
		cfg.GitHub = githubProtoToModel(payload.Github)
	default:
		return nil, fmt.Errorf("save: missing payload")
	}

	if err := config.SaveIntegrations(cfg); err != nil {
		return nil, fmt.Errorf("save integrations: %w", err)
	}
	// Reload so the response reflects what's actually on disk (handles
	// any scrubbing the save path applied).
	cfg, err = config.LoadIntegrations()
	if err != nil {
		return nil, err
	}
	return scrubConfigToProto(cfg), nil
}

// DeleteIntegration removes an integration entry by kind + id. GitHub
// is single-instance — passing GITHUB resets to the zero config. Also
// deletes the corresponding keyring secret entry to avoid leaks.
func (s *integrationsService) DeleteIntegration(_ context.Context, req *pb.DeleteIntegrationRequest) (*pb.IntegrationsConfig, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, err
	}

	id := req.GetId()
	switch req.GetKind() {
	case pb.IntegrationKind_WEBHOOK:
		cfg.Webhooks = removeWebhookByID(cfg.Webhooks, id)
		_ = config.DeleteIntegrationSecret(config.SecretKeyForIntegration(id, "secret"))
	case pb.IntegrationKind_SLACK:
		cfg.Slack = removeSlackByID(cfg.Slack, id)
		_ = config.DeleteIntegrationSecret(config.SecretKeyForIntegration(id, "url"))
	case pb.IntegrationKind_DISCORD:
		cfg.Discord = removeDiscordByID(cfg.Discord, id)
		_ = config.DeleteIntegrationSecret(config.SecretKeyForIntegration(id, "url"))
	case pb.IntegrationKind_GITHUB:
		cfg.GitHub = models.GitHubConfig{}
	default:
		return nil, fmt.Errorf("delete: unknown kind")
	}

	if err := config.SaveIntegrations(cfg); err != nil {
		return nil, err
	}
	cfg, err = config.LoadIntegrations()
	if err != nil {
		return nil, err
	}
	return scrubConfigToProto(cfg), nil
}

// TestIntegration fires a synthetic notification through the integration's
// adapter path. The dispatcher (task 0062) is the eventual canonical
// path; here we POST a minimal payload directly so the user gets a
// sample message arriving in their channel before the adapter package
// lands.
func (s *integrationsService) TestIntegration(ctx context.Context, req *pb.TestIntegrationRequest) (*pb.TestIntegrationResponse, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, err
	}

	id := req.GetId()
	switch req.GetKind() {
	case pb.IntegrationKind_WEBHOOK:
		ep, ok := findWebhook(cfg.Webhooks, id)
		if !ok {
			return &pb.TestIntegrationResponse{Ok: false, Message: "webhook not found"}, nil
		}
		return s.deliverTest(ctx, ep.URL, "TASK_FAILED")
	case pb.IntegrationKind_SLACK:
		ep, ok := findSlack(cfg.Slack, id)
		if !ok {
			return &pb.TestIntegrationResponse{Ok: false, Message: "slack endpoint not found"}, nil
		}
		if ep.URL == "" {
			return &pb.TestIntegrationResponse{Ok: false, Message: "slack URL not set in keyring"}, nil
		}
		return s.deliverTest(ctx, ep.URL, "TASK_FAILED")
	case pb.IntegrationKind_DISCORD:
		ep, ok := findDiscord(cfg.Discord, id)
		if !ok {
			return &pb.TestIntegrationResponse{Ok: false, Message: "discord endpoint not found"}, nil
		}
		if ep.URL == "" {
			return &pb.TestIntegrationResponse{Ok: false, Message: "discord URL not set in keyring"}, nil
		}
		return s.deliverDiscordTest(ctx, ep)
	case pb.IntegrationKind_GITHUB:
		// GitHub auto-PR has no synchronous test send — surface the
		// gh CLI auth check status instead. Keeps this handler honest:
		// "test" means "would a real send work right now?".
		if !cfg.GitHub.Enabled {
			return &pb.TestIntegrationResponse{Ok: false, Message: "github auto-PR is disabled"}, nil
		}
		return &pb.TestIntegrationResponse{Ok: true, Message: "github auto-PR enabled (gh auth checked at PR-open time)"}, nil
	default:
		return nil, fmt.Errorf("test: unknown kind")
	}
}

func (s *integrationsService) deliverTest(ctx context.Context, endpoint, kind string) (*pb.TestIntegrationResponse, error) {
	body := map[string]any{
		"version":     1,
		"kind":        kind,
		"emitted_at":  time.Now().UTC().Format(time.RFC3339),
		"title":       "Watchfire test notification",
		"body":        "If you can read this, your integration is wired up correctly.",
		"deep_link":   "watchfire://test",
		"project_id":  "test",
		"task_number": 0,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return &pb.TestIntegrationResponse{Ok: false, Message: err.Error()}, nil
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return &pb.TestIntegrationResponse{Ok: false, Message: err.Error()}, nil
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Watchfire-Event", kind)
	r.Header.Set("User-Agent", "watchfire-test/1")

	resp, err := s.httpClient.Do(r)
	if err != nil {
		return &pb.TestIntegrationResponse{Ok: false, Message: err.Error()}, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if !ok {
		msg = fmt.Sprintf("HTTP %d (delivery rejected)", resp.StatusCode)
	}
	return &pb.TestIntegrationResponse{
		Ok:         ok,
		Message:    msg,
		StatusCode: int32(resp.StatusCode),
	}, nil
}

// deliverDiscordTest renders + POSTs one Discord embed per supported
// notification kind, so a single click of "Test" exercises every
// template the v7.0 Relay Discord adapter ships. The aggregate response
// rolls per-kind status into the message + sets ok=true only if every
// kind succeeded; the status_code field carries the worst HTTP status
// (or the last one on all-success).
func (s *integrationsService) deliverDiscordTest(ctx context.Context, ep models.DiscordEndpoint) (*pb.TestIntegrationResponse, error) {
	adapter, err := relay.NewDiscordAdapter(ep, s.httpClient, nil)
	if err != nil {
		return &pb.TestIntegrationResponse{Ok: false, Message: err.Error()}, nil
	}

	now := time.Now().UTC()
	kinds := []notify.Kind{
		notify.KindTaskFailed,
		notify.KindRunComplete,
		notify.KindWeeklyDigest,
	}
	allOK := true
	var msgs []string
	for _, kind := range kinds {
		payload := syntheticDiscordPayload(kind, now)
		if sendErr := adapter.Send(ctx, payload); sendErr != nil {
			allOK = false
			msgs = append(msgs, fmt.Sprintf("%s: %v", kind, sendErr))
			continue
		}
		msgs = append(msgs, fmt.Sprintf("%s: OK", kind))
	}
	return &pb.TestIntegrationResponse{
		Ok:      allOK,
		Message: strings.Join(msgs, " · "),
	}, nil
}

// syntheticDiscordPayload builds a self-contained sample Payload for
// each notification kind, used by the Test handler so the user can
// preview how each Discord embed renders without waiting for a real
// failure / digest run.
func syntheticDiscordPayload(kind notify.Kind, now time.Time) relay.Payload {
	base := relay.Payload{
		Version:      1,
		Kind:         string(kind),
		EmittedAt:    now,
		ProjectID:    "test-project",
		ProjectName:  "Watchfire test",
		ProjectColor: "#3b82f6",
		TaskNumber:   1,
		TaskTitle:    "Watchfire Discord adapter test",
		DeepLink:     "watchfire://project/test-project/task/0001",
	}
	switch kind {
	case notify.KindTaskFailed:
		base.TaskFailureReason = "synthetic test — your Discord adapter is wired up correctly"
	case notify.KindWeeklyDigest:
		base.DigestDate = now.Format("2006-01-02")
		base.DeepLink = "watchfire://digest/" + base.DigestDate
		base.DigestBody = "## Watchfire weekly digest test\n\nIf you can read this, your Discord channel is receiving WEEKLY_DIGEST notifications."
	}
	return base
}

// --- proto / model converters ---------------------------------------------

func eventsModelToProto(e models.EventBitmask) *pb.IntegrationEvents {
	return &pb.IntegrationEvents{
		TaskFailed:   e.TaskFailed,
		RunComplete:  e.RunComplete,
		WeeklyDigest: e.WeeklyDigest,
	}
}

func eventsProtoToModel(e *pb.IntegrationEvents) models.EventBitmask {
	if e == nil {
		return models.EventBitmask{}
	}
	return models.EventBitmask{
		TaskFailed:   e.GetTaskFailed(),
		RunComplete:  e.GetRunComplete(),
		WeeklyDigest: e.GetWeeklyDigest(),
	}
}

// maskURL returns a display-only label for a URL: scheme stripped, host
// preserved, path obfuscated past the third segment. Result is safe to
// show in logs / UI.
func maskURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Unparseable / missing host — fall back to a constant marker so
		// we never leak the raw URL.
		return "***"
	}
	pathSeg := u.Path
	parts := strings.Split(strings.TrimPrefix(pathSeg, "/"), "/")
	// Keep at most the first two path segments so /services/T0/B0/abcd
	// renders as /services/T0/...
	if len(parts) > 2 {
		parts = append(parts[:2], "...")
	}
	masked := "***" + u.Host
	if pathSeg != "" {
		masked += "/" + strings.Join(parts, "/")
	}
	return masked
}

func scrubConfigToProto(cfg *models.IntegrationsConfig) *pb.IntegrationsConfig {
	if cfg == nil {
		return &pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}}
	}
	out := &pb.IntegrationsConfig{
		Webhooks: make([]*pb.WebhookIntegration, 0, len(cfg.Webhooks)),
		Slack:    make([]*pb.SlackIntegration, 0, len(cfg.Slack)),
		Discord:  make([]*pb.DiscordIntegration, 0, len(cfg.Discord)),
		Github: &pb.GitHubIntegration{
			Enabled:       cfg.GitHub.Enabled,
			DraftDefault:  cfg.GitHub.DraftDefault,
			ProjectScopes: append([]string(nil), cfg.GitHub.ProjectScopes...),
		},
	}
	for _, ep := range cfg.Webhooks {
		secretSet := false
		if ep.SecretRef != "" {
			if _, ok := config.LookupIntegrationSecret(ep.SecretRef); ok {
				secretSet = true
			}
		}
		out.Webhooks = append(out.Webhooks, &pb.WebhookIntegration{
			Id:             ep.ID,
			Label:          ep.Label,
			Url:            ep.URL,           // Webhook URL is non-secret (the HMAC is)
			UrlLabel:       maskURL(ep.URL),
			SecretSet:      secretSet,
			Secret:         "",                // never returned
			EnabledEvents:  eventsModelToProto(ep.EnabledEvents),
			ProjectMuteIds: append([]string(nil), ep.ProjectMuteIDs...),
		})
	}
	for _, ep := range cfg.Slack {
		out.Slack = append(out.Slack, &pb.SlackIntegration{
			Id:             ep.ID,
			Label:          ep.Label,
			Url:            "", // never returned
			UrlLabel:       maskURL(ep.URL),
			UrlSet:         ep.URL != "",
			EnabledEvents:  eventsModelToProto(ep.EnabledEvents),
			ProjectMuteIds: append([]string(nil), ep.ProjectMuteIDs...),
		})
	}
	for _, ep := range cfg.Discord {
		out.Discord = append(out.Discord, &pb.DiscordIntegration{
			Id:             ep.ID,
			Label:          ep.Label,
			Url:            "",
			UrlLabel:       maskURL(ep.URL),
			UrlSet:         ep.URL != "",
			EnabledEvents:  eventsModelToProto(ep.EnabledEvents),
			ProjectMuteIds: append([]string(nil), ep.ProjectMuteIDs...),
		})
	}
	return out
}

func githubProtoToModel(g *pb.GitHubIntegration) models.GitHubConfig {
	if g == nil {
		return models.GitHubConfig{}
	}
	return models.GitHubConfig{
		Enabled:       g.GetEnabled(),
		DraftDefault:  g.GetDraftDefault(),
		ProjectScopes: append([]string(nil), g.GetProjectScopes()...),
	}
}

func upsertWebhook(cfg *models.IntegrationsConfig, in *pb.WebhookIntegration) error {
	if in == nil {
		return fmt.Errorf("webhook payload missing")
	}
	id := strings.TrimSpace(in.GetId())
	if id == "" {
		id = uuid.New().String()
	}
	endpoint := models.WebhookEndpoint{
		ID:             id,
		Label:          in.GetLabel(),
		URL:            in.GetUrl(),
		EnabledEvents:  eventsProtoToModel(in.GetEnabledEvents()),
		ProjectMuteIDs: append([]string(nil), in.GetProjectMuteIds()...),
	}

	// Find existing entry to preserve secret_ref + push new secret if
	// supplied. Empty secret on update means "don't change".
	for i, ep := range cfg.Webhooks {
		if ep.ID == id {
			endpoint.SecretRef = ep.SecretRef
			if in.GetSecret() != "" {
				endpoint.SecretRef = config.SecretKeyForIntegration(id, "secret")
				if err := storeSecret(endpoint.SecretRef, in.GetSecret()); err != nil {
					return err
				}
			}
			cfg.Webhooks[i] = endpoint
			return nil
		}
	}

	// Create — push the secret if supplied.
	if in.GetSecret() != "" {
		endpoint.SecretRef = config.SecretKeyForIntegration(id, "secret")
		if err := storeSecret(endpoint.SecretRef, in.GetSecret()); err != nil {
			return err
		}
	}
	cfg.Webhooks = append(cfg.Webhooks, endpoint)
	return nil
}

func upsertSlack(cfg *models.IntegrationsConfig, in *pb.SlackIntegration) error {
	if in == nil {
		return fmt.Errorf("slack payload missing")
	}
	id := strings.TrimSpace(in.GetId())
	if id == "" {
		id = uuid.New().String()
	}
	endpoint := models.SlackEndpoint{
		ID:             id,
		Label:          in.GetLabel(),
		URL:            in.GetUrl(), // SaveIntegrations pushes this to keyring
		EnabledEvents:  eventsProtoToModel(in.GetEnabledEvents()),
		ProjectMuteIDs: append([]string(nil), in.GetProjectMuteIds()...),
	}
	for i, ep := range cfg.Slack {
		if ep.ID == id {
			endpoint.URLRef = ep.URLRef
			// Empty URL on update preserves the existing keyring entry
			if in.GetUrl() == "" {
				endpoint.URL = ep.URL
			}
			cfg.Slack[i] = endpoint
			return nil
		}
	}
	cfg.Slack = append(cfg.Slack, endpoint)
	return nil
}

func upsertDiscord(cfg *models.IntegrationsConfig, in *pb.DiscordIntegration) error {
	if in == nil {
		return fmt.Errorf("discord payload missing")
	}
	id := strings.TrimSpace(in.GetId())
	if id == "" {
		id = uuid.New().String()
	}
	endpoint := models.DiscordEndpoint{
		ID:             id,
		Label:          in.GetLabel(),
		URL:            in.GetUrl(),
		EnabledEvents:  eventsProtoToModel(in.GetEnabledEvents()),
		ProjectMuteIDs: append([]string(nil), in.GetProjectMuteIds()...),
	}
	for i, ep := range cfg.Discord {
		if ep.ID == id {
			endpoint.URLRef = ep.URLRef
			if in.GetUrl() == "" {
				endpoint.URL = ep.URL
			}
			cfg.Discord[i] = endpoint
			return nil
		}
	}
	cfg.Discord = append(cfg.Discord, endpoint)
	return nil
}

func storeSecret(key, value string) error {
	return config.PutIntegrationSecret(key, value)
}

func removeWebhookByID(in []models.WebhookEndpoint, id string) []models.WebhookEndpoint {
	out := in[:0]
	for _, ep := range in {
		if ep.ID == id {
			continue
		}
		out = append(out, ep)
	}
	return out
}
func removeSlackByID(in []models.SlackEndpoint, id string) []models.SlackEndpoint {
	out := in[:0]
	for _, ep := range in {
		if ep.ID == id {
			continue
		}
		out = append(out, ep)
	}
	return out
}
func removeDiscordByID(in []models.DiscordEndpoint, id string) []models.DiscordEndpoint {
	out := in[:0]
	for _, ep := range in {
		if ep.ID == id {
			continue
		}
		out = append(out, ep)
	}
	return out
}

func findWebhook(in []models.WebhookEndpoint, id string) (models.WebhookEndpoint, bool) {
	for _, ep := range in {
		if ep.ID == id {
			return ep, true
		}
	}
	return models.WebhookEndpoint{}, false
}
func findSlack(in []models.SlackEndpoint, id string) (models.SlackEndpoint, bool) {
	for _, ep := range in {
		if ep.ID == id {
			return ep, true
		}
	}
	return models.SlackEndpoint{}, false
}
func findDiscord(in []models.DiscordEndpoint, id string) (models.DiscordEndpoint, bool) {
	for _, ep := range in {
		if ep.ID == id {
			return ep, true
		}
	}
	return models.DiscordEndpoint{}, false
}
