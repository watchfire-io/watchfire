package echo

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// TaskFlushOutcome describes what `TaskFlusher` did with a delivery.
// Handlers translate it into a log line + a small response body so the
// upstream provider's "delivery details" page surfaces a useful summary
// without any further round-trip.
type TaskFlushOutcome int

const (
	// TaskFlushNoMatch — the branch did not parse as `watchfire/<n>` or
	// no project matched the repository URL. The delivery was accepted
	// (signature verified, idempotency cache updated) but no task state
	// changed. The most common cause is a non-Watchfire branch the user
	// merged through the same webhook URL.
	TaskFlushNoMatch TaskFlushOutcome = iota

	// TaskFlushAlreadyDone — the task existed and was already in the
	// `done` state. v8.x treats this as success: GitHub / GitLab /
	// Bitbucket all redeliver on transient failure, and a redelivery of
	// a successful merge should not error.
	TaskFlushAlreadyDone

	// TaskFlushedSuccess — the task was found in a non-done state and
	// has now been transitioned to `done` + `success: true`. The watcher
	// will pick up the YAML change and run the merge / cleanup pipeline.
	TaskFlushedSuccess

	// TaskFlushedFailure — the task was found and transitioned to `done`
	// + `success: false`. Used when the upstream provider reported the
	// PR / MR was closed without merging (`pull_request.closed` with
	// `merged: false`, GitLab `action: close`, Bitbucket
	// `pullrequest:rejected`).
	TaskFlushedFailure
)

func (o TaskFlushOutcome) String() string {
	switch o {
	case TaskFlushNoMatch:
		return "no-match"
	case TaskFlushAlreadyDone:
		return "already-done"
	case TaskFlushedSuccess:
		return "flushed-success"
	case TaskFlushedFailure:
		return "flushed-failure"
	default:
		return fmt.Sprintf("unknown(%d)", int(o))
	}
}

// TaskFlushRequest is the input to a `TaskFlusher` call. The fields are
// the bits all three handlers (GitHub, GitLab, Bitbucket) extract from
// the verified webhook body before dispatching:
//
//   - RepoURL is the canonical https URL of the upstream repository.
//     Handlers normalise via `NormalizeRepoURL` before passing in so the
//     match works whether the payload carries `https://github.com/x/y`
//     or `https://github.com/x/y.git`.
//   - SourceBranch is the head branch name from the merge event. Echo
//     parses `watchfire/<n>` to recover the task number; non-matching
//     branches return `TaskFlushNoMatch` without further work.
//   - Merged toggles the success bit on the target task. False maps to
//     `TaskFlushedFailure`; true to `TaskFlushedSuccess`.
//   - FailureReason is propagated to the task's YAML when Merged=false.
type TaskFlushRequest struct {
	RepoURL       string
	SourceBranch  string
	Merged        bool
	FailureReason string
}

// TaskFlushResult bundles the outcome of a single flush call. The
// `ProjectID` / `TaskNumber` / `TaskTitle` fields are populated when an
// outcome other than `TaskFlushNoMatch` is returned so handlers can
// echo them back to the upstream delivery for an audit trail.
type TaskFlushResult struct {
	Outcome     TaskFlushOutcome
	ProjectID   string
	ProjectName string
	TaskNumber  int
	TaskTitle   string
}

// TaskFlusher is the dispatch hook injected into the GitLab / Bitbucket
// (and future GitHub) handlers. The daemon-side implementation walks
// the projects index, matches by `git remote get-url origin`, loads the
// task from disk, calls `MarkDone`, and writes the YAML back. Tests
// inject a closure that validates the request and returns a fixed
// outcome so the handler logic can be exercised without standing up a
// real project tree.
type TaskFlusher func(ctx context.Context, req TaskFlushRequest) (TaskFlushResult, error)

// taskBranchPattern matches the `watchfire/<n>` (or zero-padded
// `watchfire/0042`) head branch every Watchfire-generated PR / MR uses.
// The branch is the only handle the inbound payload carries that maps
// reliably back to a local task — the upstream PR number is unknown to
// the daemon at the time the auto-PR is opened.
var taskBranchPattern = regexp.MustCompile(`^watchfire/(\d{1,8})$`)

// ParseTaskBranch returns the task number encoded in `branch` if the
// branch is a Watchfire task branch (`watchfire/<n>` with or without
// zero-padding), or 0 + false otherwise.
func ParseTaskBranch(branch string) (int, bool) {
	branch = strings.TrimSpace(branch)
	branch = strings.TrimPrefix(branch, "refs/heads/")
	m := taskBranchPattern.FindStringSubmatch(branch)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// NormalizeRepoURL canonicalises a repo URL so two equivalent forms
// (`git@github.com:org/repo.git`, `https://github.com/org/repo`,
// `https://github.com/org/repo.git`) compare equal. The output is
// always `<host>/<owner>/<repo>` (lowercased host + repo, no scheme,
// no trailing `.git` / slash). Returns the empty string for inputs
// that do not parse.
func NormalizeRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// SSH-style: git@host:owner/repo(.git)
	if strings.HasPrefix(raw, "git@") {
		raw = strings.TrimPrefix(raw, "git@")
		// Replace the first `:` with `/` so the rest parses as a path.
		if idx := strings.IndexByte(raw, ':'); idx > 0 {
			raw = raw[:idx] + "/" + raw[idx+1:]
		}
	} else {
		// Strip the scheme so https / http / ssh all collapse.
		if idx := strings.Index(raw, "://"); idx >= 0 {
			raw = raw[idx+3:]
		}
		// Strip any user-info prefix (e.g. `git@host`).
		if at := strings.IndexByte(raw, '@'); at >= 0 {
			raw = raw[at+1:]
		}
	}

	raw = strings.TrimRight(raw, "/")
	raw = strings.TrimSuffix(raw, ".git")

	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	host := strings.ToLower(parts[0])
	repoPath := parts[1]
	// Tolerate Bitbucket's project / GitLab's group nesting — keep all
	// path segments after the host. Lowercase the leaf only; nested
	// groups can be case-sensitive on some hosts.
	dir, leaf := path.Split(strings.TrimPrefix(repoPath, "/"))
	leaf = strings.TrimSuffix(leaf, ".git")
	if leaf == "" {
		leaf = strings.TrimRight(dir, "/")
	}
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return host + "/" + leaf
	}
	return host + "/" + dir + "/" + leaf
}

// HostFromRepoURL extracts just the host portion of a repo URL (after
// `NormalizeRepoURL` has folded scheme / user / `.git`). Used by the
// daemon-side `TaskFlusher` implementation to ensure the delivery's
// host matches the configured `GitHostBaseURL` before treating it as
// a candidate for the inbound flow.
func HostFromRepoURL(raw string) string {
	norm := NormalizeRepoURL(raw)
	if norm == "" {
		return ""
	}
	if idx := strings.IndexByte(norm, '/'); idx >= 0 {
		return norm[:idx]
	}
	return norm
}

// HostFromBaseURL extracts the host portion of a user-supplied
// `GitHostBaseURL` (the `https://github.example.com` form). Returns the
// empty string for inputs that do not parse. Tolerant of trailing
// slashes + paths.
func HostFromBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}
