package echo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
)

// GitHub headers and event keys.
//
//   - X-GitHub-Event       — event kind ("pull_request" is the only one
//                             this handler acts on; other events 200-ack +
//                             no-op so GitHub does not retry the delivery).
//   - X-Hub-Signature-256  — HMAC-SHA256 of the body, prefixed `sha256=`.
//                             Verified constant-time by `VerifyGitHub`.
//   - X-GitHub-Delivery    — per-delivery UUID, used for idempotency so
//                             a redelivery does not double-flush the task
//                             or double-emit the RUN_COMPLETE notification.
//
// https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
const (
	githubHeaderEvent     = "X-GitHub-Event"
	githubHeaderSignature = "X-Hub-Signature-256"
	githubHeaderDelivery  = "X-GitHub-Delivery"
)

// githubEventPullRequest is the value of `X-GitHub-Event` for pull-request
// activity. Only `action == "closed"` with `pull_request.merged == true`
// drives a task transition; other actions / events 200-ack + no-op.
const githubEventPullRequest = "pull_request"

// GitHubHandlerConfig wires the per-request state the GitHub webhook
// handler needs. Mirrors the shape of `BitbucketHandlerConfig` /
// `GitLabHandlerConfig`:
//
//   - ResolveSecret    — keyring fetch for the HMAC-SHA256 signing
//     secret. Run per request so a Settings-side rotation takes effect
//     without a daemon restart.
//   - Idempotency      — LRU+TTL cache keyed on `X-GitHub-Delivery` so
//     a redelivery does not double-flip the task to done or double-emit
//     a RUN_COMPLETE notification.
//   - FlushTask        — dispatch hook into the projects index +
//     `task.MarkDone` lifecycle. Concrete implementation lives in the
//     daemon's server package; tests inject a closure asserting on the
//     extracted branch + repo URL.
//   - EmitRunComplete  — fired exactly once per successful flush
//     (TaskFlushedSuccess outcome). The handler renders the title
//     `<project> — PR #<number> merged` and the bus / log appender
//     handles fan-out to live subscribers + the headless notifications
//     log. nil = no-op (still safe; tests inject a recorder).
//   - RefundOnReplay   — per-IP rate-limit refund hook the parent
//     Server wires when the limiter is enabled. nil = no-op.
//   - RecordDelivery   — per-provider freshness hook the parent Server
//     uses to populate `LastDelivery("github")` for the inbound status
//     RPC. nil = no-op.
//   - Logger           — instrumentation. Defaults to log.Default().
type GitHubHandlerConfig struct {
	ResolveSecret    func() ([]byte, error)
	Idempotency      *Cache
	FlushTask        TaskFlusher
	EmitRunComplete  func(n notify.Notification) error
	RefundOnReplay   func(r *http.Request)
	RecordDelivery   func()
	Logger           *log.Logger
}

// NewGitHubHandler returns the http.Handler that lives at
// `POST /echo/github/webhook`. The handler verifies the HMAC-SHA256
// signature, deduplicates by `X-GitHub-Delivery`, parses the
// `pull_request` payload, and on `action=closed` + `merged=true`
// dispatches into the shared `TaskFlusher` and emits a RUN_COMPLETE
// notification on a TaskFlushedSuccess outcome.
//
// Other GitHub events (push, issues, …) and other pull_request actions
// (opened, synchronize, reopened, closed-without-merge) are 200-acked
// without state change so GitHub does not redeliver them.
func NewGitHubHandler(cfg GitHubHandlerConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Idempotency == nil {
		cfg.Idempotency = NewCache(0, 0)
	}
	return &githubHandler{cfg: cfg}
}

type githubHandler struct{ cfg GitHubHandlerConfig }

// githubPRPayload is the subset of GitHub's `pull_request` event body
// Watchfire reads. The full schema is much richer; we only need the
// action, merged flag, PR number, head branch, and the repository URL
// for project matching.
//
// https://docs.github.com/en/webhooks/webhook-events-and-payloads#pull_request
type githubPRPayload struct {
	Action      string                 `json:"action"`
	Number      int                    `json:"number"`
	PullRequest githubPullRequest      `json:"pull_request"`
	Repository  githubRepositoryRef    `json:"repository"`
}

type githubPullRequest struct {
	Number int             `json:"number"`
	Merged bool            `json:"merged"`
	Title  string          `json:"title"`
	HTMLURL string         `json:"html_url"`
	Head   githubPRRef     `json:"head"`
	Base   githubPRRef     `json:"base"`
}

type githubPRRef struct {
	Ref  string              `json:"ref"`
	Repo githubRepositoryRef `json:"repo"`
}

type githubRepositoryRef struct {
	HTMLURL  string `json:"html_url"`
	CloneURL string `json:"clone_url"`
	SSHURL   string `json:"ssh_url"`
	FullName string `json:"full_name"`
}

func (h *githubHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if IsTooLarge(err) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			h.cfg.Logger.Printf("WARN: echo: github webhook payload too large (>%d bytes)", MaxBodyBytes)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get(githubHeaderSignature)
	if signature == "" {
		http.Error(w, "missing signature", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: github webhook missing %s header", githubHeaderSignature)
		return
	}

	secret, err := h.cfg.ResolveSecret()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: github secret not configured: %v", err)
		return
	}

	if vErr := VerifyGitHub(secret, body, signature); vErr != nil {
		status := http.StatusUnauthorized
		if errors.Is(vErr, ErrMalformedHeader) {
			status = http.StatusBadRequest
		}
		http.Error(w, vErr.Error(), status)
		h.cfg.Logger.Printf("WARN: echo: github signature rejected: %v", vErr)
		return
	}

	event := r.Header.Get(githubHeaderEvent)
	if event != githubEventPullRequest {
		// Polite 200 — GitHub redelivers 4xx and we don't want a flood of
		// spurious push / issue / star hooks pinning the inbound surface.
		writeJSONOK(w, map[string]any{"status": "ignored", "event": event})
		h.cfg.Logger.Printf("INFO: echo: github event %q ignored", event)
		return
	}

	// Idempotency check uses GitHub's per-delivery UUID. When the header
	// is missing (test harnesses, redelivery via the API) the cache is
	// bypassed — the worst case is a duplicate flush, which is itself
	// idempotent (`TaskFlushAlreadyDone` outcome on the second hit).
	deliveryID := r.Header.Get(githubHeaderDelivery)
	if deliveryID != "" && h.cfg.Idempotency.Seen(deliveryID) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		writeJSONOK(w, map[string]any{"status": "replay", "delivery": deliveryID})
		h.cfg.Logger.Printf("INFO: echo: github webhook delivery %s replayed", deliveryID)
		return
	}

	var payload githubPRPayload
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		http.Error(w, "malformed payload", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: github webhook body malformed: %v", jsonErr)
		return
	}

	// Only `closed` + `merged` drives a task transition. Other actions
	// (opened, synchronize, edited, reopened, closed-without-merge) are
	// 200-acked without state change. GitHub fires `closed` with
	// `merged=false` when a PR is closed without merging — Watchfire
	// treats that as a no-op (the auto-PR was opened on a successful
	// task; user choosing not to merge is a workflow decision, not a
	// task failure).
	if payload.Action != "closed" || !payload.PullRequest.Merged {
		writeJSONOK(w, map[string]any{
			"status": "ignored",
			"action": payload.Action,
			"merged": payload.PullRequest.Merged,
		})
		h.cfg.Logger.Printf(
			"INFO: echo: github pull_request action=%q merged=%v ignored",
			payload.Action, payload.PullRequest.Merged,
		)
		return
	}

	repoURL := preferRepoURL(
		payload.Repository.HTMLURL,
		payload.Repository.CloneURL,
		payload.PullRequest.Base.Repo.HTMLURL,
		payload.PullRequest.Base.Repo.CloneURL,
	)
	if repoURL == "" && payload.Repository.FullName != "" {
		// Fallback: synthesize the github.com URL from `full_name` so
		// payloads from older configurations (or test fixtures) that omit
		// the URL fields still resolve.
		repoURL = "https://github.com/" + payload.Repository.FullName
	}
	if repoURL == "" {
		http.Error(w, "missing repository URL", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: github payload missing repository URL")
		return
	}

	if h.cfg.FlushTask == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: github handler FlushTask not wired")
		return
	}

	flushReq := TaskFlushRequest{
		RepoURL:      repoURL,
		SourceBranch: payload.PullRequest.Head.Ref,
		Merged:       true,
	}

	res, flushErr := h.cfg.FlushTask(r.Context(), flushReq)
	if flushErr != nil {
		http.Error(w, "flush failed", http.StatusInternalServerError)
		h.cfg.Logger.Printf("ERROR: echo: github flush task: %v", flushErr)
		return
	}

	prNumber := payload.PullRequest.Number
	if prNumber == 0 {
		prNumber = payload.Number
	}

	// Emit RUN_COMPLETE only on a fresh transition. TaskFlushAlreadyDone
	// means the task was already in the `done` state on a previous
	// delivery / agent path — we already emitted the matching
	// notification at that point and a re-emit would surface a duplicate
	// "PR merged" toast.
	if res.Outcome == TaskFlushedSuccess && h.cfg.EmitRunComplete != nil {
		emittedAt := time.Now().UTC()
		title := titleForRunComplete(res.ProjectName, prNumber)
		body := bodyForRunComplete(res.TaskNumber, payload.PullRequest.HTMLURL)
		n := notify.Notification{
			ID:         notify.MakeID(notify.KindRunComplete, res.ProjectID, int32(res.TaskNumber), emittedAt),
			Kind:       notify.KindRunComplete,
			ProjectID:  res.ProjectID,
			TaskNumber: int32(res.TaskNumber),
			Title:      title,
			Body:       body,
			EmittedAt:  emittedAt,
		}
		if emitErr := h.cfg.EmitRunComplete(n); emitErr != nil {
			h.cfg.Logger.Printf("WARN: echo: github RUN_COMPLETE emit failed for task #%04d: %v", res.TaskNumber, emitErr)
		}
	}

	if h.cfg.RecordDelivery != nil {
		h.cfg.RecordDelivery()
	}
	writeJSONOK(w, taskFlushResponseBody(res))
	h.cfg.Logger.Printf(
		"INFO: echo: github pull_request merged on %s branch=%s pr=#%d outcome=%s task=%d",
		repoURL, payload.PullRequest.Head.Ref, prNumber, res.Outcome, res.TaskNumber,
	)
}

// titleForRunComplete renders the notification title in the format the
// task spec asks for: `<project> — PR #<number> merged`. The project
// name falls back to a bare "PR #<n> merged" when the project entry has
// no name (defensive — config.LoadProject normally fills this).
func titleForRunComplete(projectName string, prNumber int) string {
	if projectName == "" {
		return fmt.Sprintf("PR #%d merged", prNumber)
	}
	return fmt.Sprintf("%s — PR #%d merged", projectName, prNumber)
}

// bodyForRunComplete renders the notification body. Includes the task
// number for in-app routing + the PR URL so the desktop notification
// click can deep-link to the merged PR.
func bodyForRunComplete(taskNumber int, prURL string) string {
	if prURL != "" {
		return fmt.Sprintf("Task #%04d: %s", taskNumber, prURL)
	}
	return fmt.Sprintf("Task #%04d", taskNumber)
}

// ResolveGitHubSecretFromKeyring builds a `ResolveSecret` callback for
// the GitHub handler from a keyring fetch closure. Mirrors the existing
// helpers for the Slack / GitLab / Bitbucket handlers.
func ResolveGitHubSecretFromKeyring(fetch func() (string, error)) func() ([]byte, error) {
	return func() ([]byte, error) {
		v, err := fetch()
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, errors.New("github HMAC secret not configured")
		}
		return []byte(v), nil
	}
}

// EmitRunCompleteToBus is a small helper that wires an `EmitRunComplete`
// callback against a live `notify.Bus` + the JSONL fallback log. The
// daemon's server package uses this in `registerInboundProviderHandlers`;
// tests inject a closure that records the emitted notification.
func EmitRunCompleteToBus(bus *notify.Bus) func(n notify.Notification) error {
	return func(n notify.Notification) error {
		if bus != nil {
			bus.Emit(n)
		}
		return notify.AppendLogLine(n)
	}
}
