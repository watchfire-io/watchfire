package echo

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
)

// Bitbucket headers and event keys.
//
//   - X-Event-Key       — event kind (we handle `pullrequest:fulfilled`
//                          for merges and `pullrequest:rejected` for
//                          declined PRs; `pullrequest:created` and
//                          friends are 200-acked + ignored).
//   - X-Hub-Signature   — HMAC-SHA256 of the body, prefixed `sha256=` —
//                          identical wire format to GitHub. We reuse
//                          `VerifyGitHub` to verify it.
//   - X-Request-UUID    — per-delivery identifier, used for idempotency.
//
// Bitbucket Cloud signs requests when the user enables "Use a secret"
// on the webhook. Bitbucket Server / Data Center uses the same wire
// format with the same header. v8.x requires a configured secret —
// unsigned deliveries are rejected.
//
// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/
const (
	bitbucketHeaderEvent     = "X-Event-Key"
	bitbucketHeaderSignature = "X-Hub-Signature"
	bitbucketHeaderUUID      = "X-Request-UUID"
)

const (
	bitbucketEventPRFulfilled = "pullrequest:fulfilled" // merged
	bitbucketEventPRRejected  = "pullrequest:rejected"  // declined
)

// BitbucketHandlerConfig wires the per-request state the Bitbucket
// webhook handler needs. Mirrors `GitLabHandlerConfig` shape — the only
// material difference is `ResolveSecret` returns []byte (HMAC key) vs.
// the GitLab handler's plaintext shared token.
type BitbucketHandlerConfig struct {
	ResolveSecret  func() ([]byte, error)
	Idempotency    *Cache
	FlushTask      TaskFlusher
	RefundOnReplay func(r *http.Request)
	// RecordDelivery is the per-provider freshness hook the parent
	// Server uses to populate `LastDelivery("bitbucket")` for the
	// inbound status RPC. nil = no-op.
	RecordDelivery func()
	Logger         *log.Logger
}

// NewBitbucketHandler returns the http.Handler that lives at
// `POST /echo/bitbucket/webhook`. The handler verifies the HMAC-SHA256
// signature, deduplicates by `X-Request-UUID`, parses the
// `pullrequest:fulfilled` / `pullrequest:rejected` body, and dispatches
// into the shared `TaskFlusher`.
func NewBitbucketHandler(cfg BitbucketHandlerConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Idempotency == nil {
		cfg.Idempotency = NewCache(0, 0)
	}
	return &bitbucketHandler{cfg: cfg}
}

type bitbucketHandler struct{ cfg BitbucketHandlerConfig }

// bitbucketPRPayload is the subset of the Bitbucket Cloud
// `pullrequest:fulfilled` / `pullrequest:rejected` body Watchfire reads.
// The full schema is much richer; we only need the source branch and
// the repository's public URL for project matching.
//
// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/#PullRequest
type bitbucketPRPayload struct {
	PullRequest bitbucketPullRequest `json:"pullrequest"`
	Repository  bitbucketRepository  `json:"repository"`
}

type bitbucketPullRequest struct {
	State  string                 `json:"state"`  // "MERGED" / "DECLINED" / …
	Source bitbucketPullRequestEnd `json:"source"`
	Title  string                 `json:"title"`
}

type bitbucketPullRequestEnd struct {
	Branch     bitbucketBranch    `json:"branch"`
	Repository *bitbucketRepoMini `json:"repository"`
}

type bitbucketBranch struct {
	Name string `json:"name"`
}

type bitbucketRepoMini struct {
	FullName string `json:"full_name"`
}

type bitbucketRepository struct {
	FullName string             `json:"full_name"`
	Links    bitbucketRepoLinks `json:"links"`
}

type bitbucketRepoLinks struct {
	HTML bitbucketHref `json:"html"`
	Self bitbucketHref `json:"self"`
}

type bitbucketHref struct {
	Href string `json:"href"`
}

func (h *bitbucketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if IsTooLarge(err) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			h.cfg.Logger.Printf("WARN: echo: bitbucket webhook payload too large (>%d bytes)", MaxBodyBytes)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	secret, err := h.cfg.ResolveSecret()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: bitbucket secret not configured: %v", err)
		return
	}

	signature := r.Header.Get(bitbucketHeaderSignature)
	if signature == "" {
		http.Error(w, "missing signature", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: bitbucket webhook missing %s header", bitbucketHeaderSignature)
		return
	}
	// Bitbucket's `X-Hub-Signature` uses the same wire format as
	// GitHub's `X-Hub-Signature-256` (`sha256=<hex>`). Reuse the
	// constant-time GitHub verifier.
	if vErr := VerifyGitHub(secret, body, signature); vErr != nil {
		status := http.StatusUnauthorized
		if errors.Is(vErr, ErrMalformedHeader) {
			status = http.StatusBadRequest
		}
		http.Error(w, vErr.Error(), status)
		h.cfg.Logger.Printf("WARN: echo: bitbucket signature rejected: %v", vErr)
		return
	}

	event := r.Header.Get(bitbucketHeaderEvent)
	if event != bitbucketEventPRFulfilled && event != bitbucketEventPRRejected {
		writeJSONOK(w, map[string]any{"status": "ignored", "event": event})
		h.cfg.Logger.Printf("INFO: echo: bitbucket event %q ignored", event)
		return
	}

	deliveryID := r.Header.Get(bitbucketHeaderUUID)
	if deliveryID != "" && h.cfg.Idempotency.Seen(deliveryID) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		writeJSONOK(w, map[string]any{"status": "replay", "delivery": deliveryID})
		h.cfg.Logger.Printf("INFO: echo: bitbucket webhook delivery %s replayed", deliveryID)
		return
	}

	var payload bitbucketPRPayload
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		http.Error(w, "malformed payload", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: bitbucket webhook body malformed: %v", jsonErr)
		return
	}

	merged := event == bitbucketEventPRFulfilled
	repoURL := preferRepoURL(payload.Repository.Links.HTML.Href, payload.Repository.Links.Self.Href)
	if repoURL == "" && payload.Repository.FullName != "" {
		// Fallback: synthesize the bitbucket.org URL from `full_name` so
		// payloads from older configurations that omit the links block
		// still resolve. Self-hosted Bitbucket Server / DC always carries
		// links, so this branch is hit only on Bitbucket Cloud.
		repoURL = "https://bitbucket.org/" + payload.Repository.FullName
	}
	if repoURL == "" {
		http.Error(w, "missing repository URL", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: bitbucket payload missing repository URL")
		return
	}

	if h.cfg.FlushTask == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: bitbucket handler FlushTask not wired")
		return
	}

	flushReq := TaskFlushRequest{
		RepoURL:      repoURL,
		SourceBranch: payload.PullRequest.Source.Branch.Name,
		Merged:       merged,
	}
	if !merged {
		flushReq.FailureReason = "Bitbucket PR rejected without merge"
	}

	res, flushErr := h.cfg.FlushTask(r.Context(), flushReq)
	if flushErr != nil {
		http.Error(w, "flush failed", http.StatusInternalServerError)
		h.cfg.Logger.Printf("ERROR: echo: bitbucket flush task: %v", flushErr)
		return
	}

	if h.cfg.RecordDelivery != nil {
		h.cfg.RecordDelivery()
	}
	writeJSONOK(w, taskFlushResponseBody(res))
	h.cfg.Logger.Printf(
		"INFO: echo: bitbucket %s on %s branch=%s outcome=%s task=%d",
		event, repoURL, payload.PullRequest.Source.Branch.Name, res.Outcome, res.TaskNumber,
	)
}

// ResolveSecretFromKeyring builds a `ResolveSecret` callback for the
// Bitbucket handler from a keyring fetch closure. Mirrors the existing
// helpers for the Slack / GitLab handlers — small wrapper that turns a
// string lookup into the `func() ([]byte, error)` shape the handler
// expects.
func ResolveSecretFromKeyring(fetch func() (string, error)) func() ([]byte, error) {
	return func() ([]byte, error) {
		v, err := fetch()
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, errors.New("HMAC secret not configured")
		}
		return []byte(v), nil
	}
}
