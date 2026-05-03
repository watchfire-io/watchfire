package echo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Slack action_id values emitted by the v7.0 Relay outbound TASK_FAILED
// template (`internal/daemon/relay/templates/slack_task_failed.json.tmpl`).
// Keep these in sync with the template — a mismatch silently breaks the
// button-click loop without a 4xx because Slack still validates the
// signature.
const (
	slackActionRetry = "watchfire_retry"
	slackActionCancel = "watchfire_cancel"
	slackActionView   = "watchfire_view" // pure URL link; never round-trips back to Watchfire
)

// Cancel modal identifiers. The view's `callback_id` keys the
// view_submission dispatch path; the input block + element ids are
// where the handler reads the user-supplied reason from
// `view.state.values`.
const (
	cancelModalCallbackID   = "watchfire_cancel_reason"
	cancelModalReasonBlock  = "reason_block"
	cancelModalReasonAction = "reason_input"
)

// slackInteractionTypeBlockActions / slackInteractionTypeViewSubmission
// are the two payload shapes this handler dispatches on. Slack defines a
// few more (block_suggestion, message_action, …) but Watchfire emits
// neither button menus nor message shortcuts in v8.x, so other types are
// 200-acked with no action.
//
// https://api.slack.com/reference/interaction-payloads
const (
	slackInteractionTypeBlockActions   = "block_actions"
	slackInteractionTypeViewSubmission = "view_submission"
)

// SlackHandlerConfig wires the per-request state the Slack interactivity
// handler needs. Mirrors `DiscordHandlerConfig`:
//
//   - ResolveSigningSecret  — keyring fetch for the v0 HMAC secret. Run
//     per request so a Settings-side rotation takes effect without a
//     daemon restart.
//   - Idempotency           — LRU+TTL cache keyed on the per-interaction
//     `trigger_id` (block_actions) / `view.id` (view_submission).
//   - CommandContextFor     — factory that scopes FindProjects / Retry /
//     Cancel / LookupTask to the calling Slack team. Keyed on team_id +
//     user_id so a daemon serving multiple workspaces never crosses
//     project boundaries.
//   - OpenModal             — optional Slack-API caller used to open the
//     Cancel-reason modal in response to a `watchfire_cancel` click. nil
//     = fall back to immediate cancel with empty reason. Tests inject a
//     mock; production wiring posts to `https://slack.com/api/views.open`
//     with a bot token (deferred to v8.x `echo8oauth`).
//   - Logger                — instrumentation. Defaults to log.Default().
type SlackHandlerConfig struct {
	ResolveSigningSecret func() ([]byte, error)
	Idempotency          *Cache
	CommandContextFor    func(teamID, userID string) CommandContext
	OpenModal            func(ctx context.Context, triggerID string, view map[string]any) error
	// RefundOnReplay is the per-IP rate-limit refund hook the parent
	// Server wires when the limiter is enabled. nil = no-op (Slack
	// retries still hit the LRU cache; they just don't get budget back).
	RefundOnReplay       func(r *http.Request)
	Logger               *log.Logger
}

// NewSlackInteractivityHandler returns the http.Handler that lives at
// `POST /echo/slack/interactivity`. Slack delivers both block_actions
// (button clicks) and view_submission (modal submits) to this single
// endpoint; the handler dispatches on the parsed payload's `type`.
//
// The signature verifier reads the raw body before form-decoding (Slack
// signs the urlencoded request body byte-for-byte). The form payload is
// re-parsed in process from the same buffer so we never round-trip
// through net/http's lossy form parser.
func NewSlackInteractivityHandler(cfg SlackHandlerConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Idempotency == nil {
		cfg.Idempotency = NewCache(0, 0)
	}
	return &slackHandler{cfg: cfg}
}

type slackHandler struct{ cfg SlackHandlerConfig }

// slackInteraction is the subset of the Slack interactivity payload
// Watchfire reads. Slack's full schema is much richer; we only need the
// type, the per-team identifier (for project routing), the user (for
// audit), the trigger_id (for `views.open` + idempotency), and the type-
// specific bits (`actions[]` for block_actions, `view` for view_submission).
//
// https://api.slack.com/reference/interaction-payloads/block-actions
// https://api.slack.com/reference/interaction-payloads/views
type slackInteraction struct {
	Type      string             `json:"type"`
	Team      slackTeam          `json:"team"`
	User      slackUser          `json:"user"`
	TriggerID string             `json:"trigger_id"`
	ResponseURL string           `json:"response_url"`
	Actions   []slackAction      `json:"actions"`
	View      *slackView         `json:"view"`
}

type slackTeam struct {
	ID string `json:"id"`
}

type slackUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type slackAction struct {
	ActionID string `json:"action_id"`
	BlockID  string `json:"block_id"`
	Value    string `json:"value"`
	Type     string `json:"type"`
}

type slackView struct {
	ID              string                  `json:"id"`
	CallbackID      string                  `json:"callback_id"`
	PrivateMetadata string                  `json:"private_metadata"`
	State           slackViewState          `json:"state"`
	Hash            string                  `json:"hash"`
}

type slackViewState struct {
	Values map[string]map[string]slackViewElement `json:"values"`
}

type slackViewElement struct {
	Type     string `json:"type"`
	Value    string `json:"value"`
	SelectedOption *struct {
		Value string `json:"value"`
	} `json:"selected_option,omitempty"`
}

func (h *slackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if IsTooLarge(err) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			h.cfg.Logger.Printf("WARN: echo: slack interactivity payload too large (>%d bytes)", MaxBodyBytes)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")
	if timestamp == "" || signature == "" {
		http.Error(w, "missing signature headers", http.StatusUnauthorized)
		h.cfg.Logger.Printf("WARN: echo: slack interactivity missing signature headers")
		return
	}

	secret, err := h.cfg.ResolveSigningSecret()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		h.cfg.Logger.Printf("ERROR: echo: slack signing secret not configured: %v", err)
		return
	}

	if vErr := VerifySlack(secret, timestamp, body, signature); vErr != nil {
		status := http.StatusUnauthorized
		if errors.Is(vErr, ErrMalformedHeader) {
			status = http.StatusBadRequest
		}
		http.Error(w, vErr.Error(), status)
		h.cfg.Logger.Printf("WARN: echo: slack signature rejected: %v", vErr)
		return
	}

	form, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "malformed body", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: slack interactivity body not form-encoded: %v", err)
		return
	}
	rawPayload := form.Get("payload")
	if rawPayload == "" {
		http.Error(w, "missing payload field", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: slack interactivity missing payload field")
		return
	}

	var interaction slackInteraction
	if err := json.Unmarshal([]byte(rawPayload), &interaction); err != nil {
		http.Error(w, "malformed interaction", http.StatusBadRequest)
		h.cfg.Logger.Printf("WARN: echo: slack interactivity payload malformed: %v", err)
		return
	}

	switch interaction.Type {
	case slackInteractionTypeBlockActions:
		h.handleBlockActions(w, r, &interaction)
	case slackInteractionTypeViewSubmission:
		h.handleViewSubmission(w, r, &interaction)
	default:
		// Polite ack — Slack will redeliver if we don't 200, so emit a
		// minimal ephemeral acknowledging the request rather than letting
		// it spin in the retry queue.
		writeSlackEphemeral(w, "Not supported in v8.x — Watchfire only handles button clicks and modal submissions.")
		h.cfg.Logger.Printf("INFO: echo: slack interaction type %q ignored", interaction.Type)
	}
}

// handleBlockActions dispatches each `actions[]` entry. Slack typically
// delivers a single action per click, but the list shape allows multi-
// action surfaces (e.g. checkbox groups). Watchfire only emits one
// button per click site, so we route on the first non-noop action and
// 200-ack the rest.
func (h *slackHandler) handleBlockActions(w http.ResponseWriter, r *http.Request, interaction *slackInteraction) {
	if len(interaction.Actions) == 0 {
		writeSlackEmpty(w)
		return
	}

	// Idempotency: replay protection on the trigger_id. Slack re-fires
	// the same trigger_id if we don't ack within 3 seconds; we want the
	// second hit to no-op (we already routed Retry once) rather than
	// double-flip the task to ready.
	if interaction.TriggerID != "" && h.cfg.Idempotency.Seen(interaction.TriggerID) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		h.cfg.Logger.Printf("INFO: echo: slack block_actions trigger %s replayed, returning empty ack", interaction.TriggerID)
		writeSlackEmpty(w)
		return
	}

	action := interaction.Actions[0]
	cc := h.cfg.CommandContextFor(interaction.Team.ID, interaction.User.ID)

	switch action.ActionID {
	case slackActionRetry:
		taskRef := taskRefFromValue(action.Value)
		resp := Route(r.Context(), "/watchfire", "retry", taskRef, cc)
		writeSlackResponse(w, RenderSlack(resp))
		h.cfg.Logger.Printf("INFO: echo: slack retry button team=%s user=%s value=%q", interaction.Team.ID, interaction.User.ID, action.Value)

	case slackActionCancel:
		// When OpenModal is wired, ask the user for a cancellation
		// reason before mutating task state. When unwired, fall through
		// to the slash-command equivalent (cancel with empty reason)
		// so the button is never a dead-end.
		if h.cfg.OpenModal != nil && interaction.TriggerID != "" {
			projectID, taskNumber, ok := splitProjectTaskRef(action.Value)
			if !ok {
				writeSlackResponse(w, RenderSlack(errorResponse(fmt.Sprintf("Cancel button value malformed: %q", action.Value))))
				return
			}
			taskTitle := ""
			if cc.LookupTask != nil {
				if t, _, err := cc.LookupTask(r.Context(), strconv.Itoa(taskNumber)); err == nil && t != nil {
					taskTitle = t.Title
				}
			}
			view := CancelReasonModalView(projectID, taskNumber, taskTitle)
			if err := h.cfg.OpenModal(r.Context(), interaction.TriggerID, view); err != nil {
				h.cfg.Logger.Printf("ERROR: echo: slack views.open failed: %v", err)
				// Fall back to direct cancel rather than leaving the
				// user staring at a button that did nothing.
				resp := Route(r.Context(), "/watchfire", "cancel", taskRefFromValue(action.Value), cc)
				writeSlackResponse(w, RenderSlack(resp))
				return
			}
			// `views.open` succeeded — Slack expects an empty 200 so
			// the button click ack returns to the user immediately.
			writeSlackEmpty(w)
			h.cfg.Logger.Printf("INFO: echo: slack cancel modal opened team=%s user=%s value=%q", interaction.Team.ID, interaction.User.ID, action.Value)
			return
		}
		taskRef := taskRefFromValue(action.Value)
		resp := Route(r.Context(), "/watchfire", "cancel", taskRef, cc)
		writeSlackResponse(w, RenderSlack(resp))
		h.cfg.Logger.Printf("INFO: echo: slack cancel button team=%s user=%s value=%q", interaction.Team.ID, interaction.User.ID, action.Value)

	case slackActionView:
		// Pure URL button — Slack handles the redirect client-side and
		// shouldn't have round-tripped here. Empty 200.
		writeSlackEmpty(w)

	default:
		// Unknown action_id — return help text rather than silent
		// failure so the user sees something is wrong with the wiring.
		writeSlackResponse(w, RenderSlack(helpResponse(action.ActionID)))
		h.cfg.Logger.Printf("WARN: echo: slack unknown action_id %q", action.ActionID)
	}
}

// handleViewSubmission processes a modal submission. The only modal
// Watchfire opens in v8.x is the Cancel-reason prompt; submissions for
// any other callback_id are 200-acked with no action.
func (h *slackHandler) handleViewSubmission(w http.ResponseWriter, r *http.Request, interaction *slackInteraction) {
	if interaction.View == nil {
		writeSlackEmpty(w)
		return
	}
	view := interaction.View

	// Idempotency: Slack re-delivers a view_submission up to 3 times if
	// we don't 200 within 3 seconds. Key on view.id so a duplicate
	// delivery doesn't double-cancel.
	if view.ID != "" && h.cfg.Idempotency.Seen(view.ID) {
		if h.cfg.RefundOnReplay != nil {
			h.cfg.RefundOnReplay(r)
		}
		h.cfg.Logger.Printf("INFO: echo: slack view_submission %s replayed, returning empty ack", view.ID)
		writeSlackEmpty(w)
		return
	}

	switch view.CallbackID {
	case cancelModalCallbackID:
		projectID, taskNumber, ok := splitProjectTaskRef(view.PrivateMetadata)
		if !ok {
			writeViewSubmissionError(w, cancelModalReasonBlock, "Internal error: missing task reference.")
			h.cfg.Logger.Printf("ERROR: echo: slack cancel modal private_metadata malformed: %q", view.PrivateMetadata)
			return
		}
		reason := readModalReason(view)
		cc := h.cfg.CommandContextFor(interaction.Team.ID, interaction.User.ID)
		if cc.Cancel == nil {
			writeViewSubmissionError(w, cancelModalReasonBlock, "Cancel handler not wired.")
			return
		}
		if err := cc.Cancel(r.Context(), projectID, taskNumber, reason); err != nil {
			writeViewSubmissionError(w, cancelModalReasonBlock, fmt.Sprintf("Cancel failed: %v", err))
			h.cfg.Logger.Printf("ERROR: echo: slack cancel modal cc.Cancel: %v", err)
			return
		}
		// Empty 200 closes the modal; the user sees their original
		// channel ping mutate via response_url (deferred to a future
		// refinement) — for now the modal closing is enough confirmation.
		writeSlackEmpty(w)
		h.cfg.Logger.Printf("INFO: echo: slack cancel modal submitted team=%s user=%s project=%s task=%d reason=%q", interaction.Team.ID, interaction.User.ID, projectID, taskNumber, reason)

	default:
		writeSlackEmpty(w)
		h.cfg.Logger.Printf("WARN: echo: slack unknown view callback_id %q", view.CallbackID)
	}
}

// taskRefFromValue extracts the task-number portion of a button value
// shaped `<projectID>|<taskNumber>`. The Slack handler hands the bare
// task number to `commands.Route` since the router resolves projects
// itself from the calling team's mapping. Falls back to the raw value
// when the pipe separator is missing so legacy buttons still route.
func taskRefFromValue(value string) string {
	_, n, ok := splitProjectTaskRef(value)
	if !ok {
		return strings.TrimSpace(value)
	}
	return strconv.Itoa(n)
}

// joinProjectTaskRef formats the canonical `<projectID>|<taskNumber>`
// reference used in button values + modal private_metadata.
func joinProjectTaskRef(projectID string, taskNumber int) string {
	return fmt.Sprintf("%s|%d", projectID, taskNumber)
}

// splitProjectTaskRef parses the inverse. ok=false on missing pipe or
// non-numeric task number — callers translate that into a user-visible
// error rather than guessing.
func splitProjectTaskRef(s string) (projectID string, taskNumber int, ok bool) {
	idx := strings.LastIndex(s, "|")
	if idx <= 0 || idx == len(s)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[idx+1:])
	if err != nil {
		return "", 0, false
	}
	return s[:idx], n, true
}

func readModalReason(view *slackView) string {
	if view == nil {
		return ""
	}
	block, ok := view.State.Values[cancelModalReasonBlock]
	if !ok {
		return ""
	}
	el, ok := block[cancelModalReasonAction]
	if !ok {
		return ""
	}
	return strings.TrimSpace(el.Value)
}

// writeSlackEmpty writes the empty 200 Slack expects when the handler
// does not want to surface a synchronous response (e.g. a button click
// whose only effect is opening a modal).
func writeSlackEmpty(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
}

// writeSlackEphemeral is the response shape for an ephemeral-only
// reply — used for unsupported interaction types so the button click
// does not silently disappear.
func writeSlackEphemeral(w http.ResponseWriter, content string) {
	body, _ := json.Marshal(map[string]any{
		"response_type": "ephemeral",
		"text":          content,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeSlackResponse writes a pre-rendered Slack response body.
func writeSlackResponse(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeViewSubmissionError sends Slack's `response_action: errors` reply
// keyed on the input block_id. The error text shows under the input
// rather than dismissing the modal, which lets the user fix the input
// and resubmit.
//
// https://api.slack.com/surfaces/modals#displaying_errors
func writeViewSubmissionError(w http.ResponseWriter, blockID, msg string) {
	body, _ := json.Marshal(map[string]any{
		"response_action": "errors",
		"errors": map[string]string{
			blockID: msg,
		},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// PostToResponseURL is a small helper for handlers that want to send a
// delayed reply to the Slack `response_url` carried on the interaction
// payload (e.g. a Retry button that fires Route() and then wants to
// replace the original message). Kept here so production wiring + tests
// can reuse a single implementation.
func PostToResponseURL(ctx context.Context, client *http.Client, responseURL string, body []byte) error {
	if responseURL == "" {
		return fmt.Errorf("slack: empty response_url")
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack response_url: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("slack response_url: POST: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack response_url: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ResolveSigningSecretFromKeyring builds a `ResolveSigningSecret` callback
// from a keyring fetch closure. Mirrors `ResolvePublicKeyFromHex` for the
// Discord handler — a small wrapper that turns a string lookup into the
// `func() ([]byte, error)` shape the handler expects.
func ResolveSigningSecretFromKeyring(fetch func() (string, error)) func() ([]byte, error) {
	return func() ([]byte, error) {
		v, err := fetch()
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, fmt.Errorf("slack signing secret not configured")
		}
		return []byte(v), nil
	}
}
