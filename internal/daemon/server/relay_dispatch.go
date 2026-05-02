package server

import (
	"net/http"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/daemon/relay"
	"github.com/watchfire-io/watchfire/internal/models"
)

// relayHTTPClient is a single shared client across every adapter the
// dispatcher builds. 10s timeout matches the per-adapter constructors;
// keeping one client means connection reuse stays effective when many
// adapters POST to the same host (e.g. several Slack channels).
var relayHTTPClient = &http.Client{Timeout: 10 * time.Second}

// buildRelayAdapters reads the current `~/.watchfire/integrations.yaml`
// and returns one relay.Adapter per configured endpoint. Run is the
// dispatcher's AdapterFactory: it is called from NewDispatcher and
// again on every Reload (triggered by the watcher's
// EventIntegrationsChanged event).
//
// Webhook endpoints carry an HMAC secret resolved from the keyring
// here; Slack and Discord URLs were already loaded by
// `config.LoadIntegrations` (their URL is itself the secret). A miss
// in either case is logged and the adapter is built with an empty
// secret — Send will then refuse loudly rather than send unsigned /
// to a broken URL.
func buildRelayAdapters() ([]relay.Adapter, error) {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return nil, err
	}

	out := make([]relay.Adapter, 0,
		len(cfg.Webhooks)+len(cfg.Slack)+len(cfg.Discord))

	for _, ep := range cfg.Webhooks {
		secret := lookupWebhookSecret(ep)
		out = append(out, relay.NewWebhookAdapter(ep, secret, relayHTTPClient, nil))
	}
	for _, ep := range cfg.Slack {
		a, err := relay.NewSlackAdapter(ep, relayHTTPClient, nil)
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	for _, ep := range cfg.Discord {
		a, err := relay.NewDiscordAdapter(ep, relayHTTPClient, nil)
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func lookupWebhookSecret(ep models.WebhookEndpoint) []byte {
	if ep.SecretRef == "" {
		return nil
	}
	v, ok := config.LookupIntegrationSecret(ep.SecretRef)
	if !ok {
		return nil
	}
	return []byte(v)
}

// resolveNotificationPayload converts a notify.Notification into a
// fully-populated relay.Payload. Project name + color come from the
// projects index; task title + failure reason come from the on-disk
// task YAML. WEEKLY_DIGEST notifications carry their digest path /
// body in the notification's Body field, so we copy that across.
func resolveNotificationPayload(n notify.Notification) (relay.Payload, error) {
	in := relay.PayloadInput{Notification: n}

	// Resolve project metadata via the projects index.
	if n.ProjectID != "" {
		if index, err := config.LoadProjectsIndex(); err == nil {
			if entry := index.FindProject(n.ProjectID); entry != nil {
				in.ProjectName = entry.Name
				if proj, perr := config.LoadProject(entry.Path); perr == nil && proj != nil {
					in.ProjectColor = proj.Color
				}
				// Resolve task metadata for task-bound kinds.
				if n.Kind != notify.KindWeeklyDigest && n.TaskNumber > 0 {
					if t, terr := config.LoadTask(entry.Path, int(n.TaskNumber)); terr == nil && t != nil {
						in.TaskTitle = t.Title
						in.TaskFailureReason = t.FailureReason
					}
				}
			}
		}
	}

	if n.Kind == notify.KindWeeklyDigest {
		// The digest notification carries the rendered preview in
		// `Body` and the file path in `Title` for now (see digest.go);
		// fall back to the emit timestamp's date if no explicit date
		// was resolvable.
		in.DigestDate = n.EmittedAt.UTC().Format("2006-01-02")
		in.DigestBody = n.Body
	}

	return relay.BuildPayload(in), nil
}
