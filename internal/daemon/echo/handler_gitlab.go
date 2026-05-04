package echo

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
)

// gitlabHeaderEvent / gitlabHeaderToken / gitlabHeaderUUID are the three
// headers GitLab attaches to every webhook delivery.
//
//   - X-Gitlab-Event       — human-readable event kind ("Merge Request Hook")
//   - X-Gitlab-Token       — opaque shared secret, compared constant-time
//   - X-Gitlab-Event-UUID  — per-delivery identifier, used for idempotency
//
// GitLab does not sign the body (no HMAC) — the security boundary is the
// shared secret in `X-Gitlab-Token`. We compare constant-time so a probe
// loop cannot recover the token from timing side-channels, and we only
// touch the body after the token check passes.
//
// https://docs.gitlab.com/ee/user/project/integrations/webhook_events.html
const (
	gitlabHeaderEvent = "X-Gitlab-Event"
	gitlabHeaderToken = "X-Gitlab-Token"
	gitlabHeaderUUID  = "X-Gitlab-Event-UUID"
)

// gitlabEventMR is the value of `X-Gitlab-Event` for the merge-request
// hook kind. Any other value is 200-acked + ignored so the daemon
// doesn't drop the delivery on the upstream side (GitLab redelivers
// unhandled 4xx, which would spin until the project owner fixes the
// hook config).
const gitlabEventMR = "Merge Request Hook"

// GitLabHandlerConfig wires the per-request state the GitLab webhook
// handler needs. The shape mirrors the v8.0 Discord / Slack handlers:
//
//   - ResolveSharedToken — keyring fetch for the per-host shared secret.
//     Run per request so a Settings-side rotation takes effect without a
//     daemon restart.
//   - Idempotency        — LRU+TTL cache keyed on the per-delivery UUID
//     header so a redelivery does not double-flush the matching task.
//   - FlushTask          — dispatch hook into the projects index +
//     `task.MarkDone` lifecycle. Concrete implementation lives in the
//     daemon's server package; tests inject a closure asserting on the
//     extracted branch + repo URL.
//   - RefundOnReplay     — per-IP rate-limit refund hook the parent
//     Server wires when the limiter is enabled. nil = no-op.
//   - Logger             — instrumentation. Defaults to log.Default().
type GitLabHandlerConfig struct {
	ResolveSharedToken func() (string, error)
	Idempotency        *Cache
	FlushTask          TaskFlusher
	RefundOnReplay     func(r *http.Request)
	// RecordDelivery is the per-provider freshness hook the parent
	// Server uses to populate `LastDelivery("gitlab")` for the inbound
	// status RPC. Called once per verified delivery whose handler ran
	// to completion (verified signature + flush dispatch). nil = no-op.
	RecordDelivery func()
	Logger         *log.Logger
}

// NewGitLabHandler returns the http.Handler that lives at
// `POST /echo/gitlab/webhook`. The handler is purely a transport
// adapter: it verifies the shared token, deduplicates by UUID, parses
// the merge-request body, and dispatches into the shared `TaskFlusher`.
func NewGitLabHandler(cfg GitLabHandlerConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Idempotency == nil {
		cfg.Idempotency = NewCache(0, 0)
	}
	return &gitlabHandler{cfg: cfg}
}

type gitlabHandler struct{ cfg GitLabHandlerConfig }

// gitlabMRPayload is the subset of the merge-request hook body
// Watchfire reads. GitLab's full schema is much richer; we only need
// the action string, the source branch, and the project's web_url for
// repo matching.
//
// https://docs.gitlab.com/ee/user/project/integrations/webhook_events.html#merge-request-events
type gitlabMRPayload struct {
	ObjectKind       string                 `json:"object_kind"`
	ObjectAttributes gitlabMRAttributes     `json:"object_attributes"`
	Project          gitlabProjectReference `json:"project"`
}

type gitlabMRAttributes struct {
	Action       string `json:"action"`        // "open" / "merge" / "close" / "reopen" / …
	State        string `json:"state"`         // "merged" / "closed" / "opened"
	SourceBranch string `json:"source_branch"` // head branch — `watchfire/<n>` for our PRs
	TargetBranch string `json:"target_branch"`
}

type gitlabProjectReference struct {
	WebURL            string `json:"web_url"`
	GitHTTPURL        string `json:"git_http_url"`
	GitSSHURL         string `json:"git_ssh_url"`
	PathWithNamespace string `json:"path_with_namespace"`
}

func (h *gitlabHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if IsTooLarge(err) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			h.cfg.Logger.Printf("WARN: echo: gitlab webhook payload too large (>%d bytes)", MaxBodyBytes)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	expected, err := h.cfg.ResolveSharedToken()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: gitlab shared token not configured: %v", err)
		return
	}

	got := r.Header.Get(gitlabHeaderToken)
	if got == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: gitlab webhook missing %s header", gitlabHeaderToken)
		return
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: gitlab webhook token mismatch")
		return
	}

	event := r.Header.Get(gitlabHeaderEvent)
	if event != gitlabEventMR {
		// Polite 200 — GitLab redelivers 4xx and we don't want a flood
		// of spurious project-event hooks pinning the inbound surface.
		writeJSONOK(w, map[string]any{"status": "ignored", "reason": fmt.Sprintf("event %q not handled", event)})
		h.cfg.Logger.Printf("INFO: echo: gitlab webhook event %q ignored", event)
		return
	}

	// Idempotency check uses GitLab's per-delivery UUID. When the header
	// is missing (older self-hosted installs) the cache is bypassed —
	// the worst case is a duplicate flush, which is itself idempotent
	// (`TaskFlushAlreadyDone` outcome on the second hit).
	deliveryID := r.Header.Get(gitlabHeaderUUID)
	if deliveryID != "" && h.cfg.Idempotency.Seen(deliveryID) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		writeJSONOK(w, map[string]any{"status": "replay", "delivery": deliveryID})
		h.cfg.Logger.Printf("INFO: echo: gitlab webhook delivery %s replayed", deliveryID)
		return
	}

	var payload gitlabMRPayload
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		http.Error(w, "malformed payload", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: gitlab webhook body malformed: %v", jsonErr)
		return
	}

	// Only the close/merge transitions affect a Watchfire task. Open /
	// reopen / approval events are 200-acked + ignored. We accept both
	// `action: merge` and `state: merged` so different GitLab versions
	// (the action field was added in 12.x) produce the same dispatch.
	merged := payload.ObjectAttributes.Action == "merge" || payload.ObjectAttributes.State == "merged"
	closedWithoutMerge := !merged && (payload.ObjectAttributes.Action == "close" || payload.ObjectAttributes.State == "closed")
	if !merged && !closedWithoutMerge {
		writeJSONOK(w, map[string]any{"status": "ignored", "action": payload.ObjectAttributes.Action})
		h.cfg.Logger.Printf("INFO: echo: gitlab MR action %q (state %q) ignored", payload.ObjectAttributes.Action, payload.ObjectAttributes.State)
		return
	}

	repoURL := preferRepoURL(payload.Project.WebURL, payload.Project.GitHTTPURL, payload.Project.GitSSHURL)
	if repoURL == "" {
		http.Error(w, "missing repository URL", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: gitlab MR payload missing repository URL")
		return
	}

	if h.cfg.FlushTask == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: gitlab handler FlushTask not wired")
		return
	}

	flushReq := TaskFlushRequest{
		RepoURL:      repoURL,
		SourceBranch: payload.ObjectAttributes.SourceBranch,
		Merged:       merged,
	}
	if closedWithoutMerge {
		flushReq.FailureReason = "GitLab MR closed without merge"
	}

	res, flushErr := h.cfg.FlushTask(r.Context(), flushReq)
	if flushErr != nil {
		http.Error(w, "flush failed", http.StatusInternalServerError)
		h.cfg.Logger.Printf("ERROR: echo: gitlab flush task: %v", flushErr)
		return
	}

	if h.cfg.RecordDelivery != nil {
		h.cfg.RecordDelivery()
	}
	writeJSONOK(w, taskFlushResponseBody(res))
	h.cfg.Logger.Printf(
		"INFO: echo: gitlab MR %s on %s branch=%s outcome=%s task=%d",
		payload.ObjectAttributes.Action, repoURL, payload.ObjectAttributes.SourceBranch, res.Outcome, res.TaskNumber,
	)
}

// preferRepoURL picks the most useful repo URL out of GitLab's three
// candidate fields. web_url is the canonical https URL the user pastes
// into their browser; git_http_url and git_ssh_url are fallbacks for
// projects whose public URL is not browser-routable.
func preferRepoURL(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

// writeJSONOK writes a 200 with an `application/json` content type and
// the supplied body. Used by the GitLab + Bitbucket handlers for their
// success / ignored ack responses (the only shapes the upstream
// providers parse are status codes; the body is for debug only).
func writeJSONOK(w http.ResponseWriter, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		// Should never happen — `body` is always a map[string]any built
		// in this file. Fall through to an empty 200 rather than panic.
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf)
}

// taskFlushResponseBody renders a TaskFlushResult into a small JSON
// document the upstream provider (and any human reviewing the delivery
// log) can read at a glance.
func taskFlushResponseBody(res TaskFlushResult) map[string]any {
	body := map[string]any{
		"status":  "ok",
		"outcome": res.Outcome.String(),
	}
	if res.TaskNumber > 0 {
		body["task_number"] = res.TaskNumber
	}
	if res.TaskTitle != "" {
		body["task_title"] = res.TaskTitle
	}
	if res.ProjectID != "" {
		body["project_id"] = res.ProjectID
	}
	if res.ProjectName != "" {
		body["project_name"] = res.ProjectName
	}
	return body
}

// ResolveSharedTokenFromKeyring builds a `ResolveSharedToken` callback
// from a keyring fetch closure. Mirrors `ResolveSigningSecretFromKeyring`
// for the Slack handler — small wrapper that turns a string lookup into
// the `func() (string, error)` shape the GitLab handler expects.
func ResolveSharedTokenFromKeyring(fetch func() (string, error)) func() (string, error) {
	return func() (string, error) {
		v, err := fetch()
		if err != nil {
			return "", err
		}
		if v == "" {
			return "", errors.New("shared token not configured")
		}
		return v, nil
	}
}
