// Package git wraps the v7.0 Relay GitHub auto-PR flow: when a task lands
// successfully and the project opted into auto-PR, push the `watchfire/<n>`
// branch to GitHub and open a PR with body rendered from task metadata + the
// v6.0 Ember diff stats. The local merge is suppressed on success; on any
// failure the caller falls back to the existing silent-merge path.
package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/diff"
	"github.com/watchfire-io/watchfire/internal/daemon/relay"
)

// Sentinel errors so callers can distinguish "fall back silently" from
// "log loud and fall back". Push / API failures are wrapped with %w so
// the caller can still inspect the underlying error message.
var (
	// ErrGHUnavailable means the `gh` CLI is missing from PATH or
	// `gh auth status` returned non-zero. Callers fall back to the
	// silent-merge path.
	ErrGHUnavailable = errors.New("gh CLI is not installed or not authenticated")

	// ErrNotGitHub means the project's `origin` remote does not point at
	// github.com. Callers fall back to the silent-merge path.
	ErrNotGitHub = errors.New("origin is not a github.com URL")
)

// PRResult is what OpenPR returns on success.
type PRResult struct {
	URL    string
	Number int
	Branch string
}

// OpenPROptions carries everything OpenPR needs to push a branch and render
// a PR body. ProjectID feeds the deep-link footer; ProjectPath is the host
// project root (the main worktree, not a task worktree).
//
// `GitHubHostname` (v8.x) overrides github.com when the Watchfire installation
// is paired with a GitHub Enterprise instance. Empty = github.com — the
// default that preserves v7.0 behaviour. When set, the `gh` CLI invocation
// gets `--hostname <value>` so authentication and the API endpoint both
// route to the user's Enterprise host. The accepted-origin pattern is also
// widened to match `https://<hostname>/owner/repo` URLs.
type OpenPROptions struct {
	ProjectPath        string
	ProjectID          string
	TaskNumber         int
	TaskTitle          string
	TaskPrompt         string
	AcceptanceCriteria string
	Agent              string
	DraftDefault       bool
	CompletedAt        time.Time
	GitHubHostname     string
}

// commandContext is the exec entry-point for every shell-out OpenPR makes.
// Tests override this to inject deterministic argv capture / fake gh.
var commandContext = exec.CommandContext

// fileDiff is the projected file row used inside the PR body template.
// We don't embed the diff package's FileDiff directly because the template
// only needs path + counts, not hunks.
type fileDiff struct {
	Path      string
	Additions int
	Deletions int
}

type prBodyData struct {
	ProjectID          string
	TaskNumber         int
	TaskTitle          string
	TaskPrompt         string
	AcceptanceCriteria string
	Agent              string
	CompletedAt        time.Time
	Files              []fileDiff
	TotalAdditions     int
	TotalDeletions     int
	Truncated          bool
}

// OpenPR pushes the task branch and opens a PR via `gh api`.
//
// Steps:
//  1. `gh auth status` (gates ErrGHUnavailable)
//  2. parse `git remote get-url origin` for owner/repo (gates ErrNotGitHub)
//  3. `git push -u --force-with-lease origin watchfire/<n>`
//  4. render PR body from task metadata + v6.0 diff stats
//  5. `gh api -X POST /repos/<owner>/<repo>/pulls --field title=...`
func OpenPR(ctx context.Context, opts OpenPROptions) (*PRResult, error) {
	if opts.ProjectPath == "" {
		return nil, errors.New("OpenPR: ProjectPath required")
	}
	if opts.TaskNumber <= 0 {
		return nil, errors.New("OpenPR: TaskNumber required")
	}

	if err := verifyGHCLI(ctx, opts.GitHubHostname); err != nil {
		return nil, err
	}

	owner, repo, err := parseGitHubOrigin(ctx, opts.ProjectPath, opts.GitHubHostname)
	if err != nil {
		return nil, err
	}

	branch := fmt.Sprintf("watchfire/%04d", opts.TaskNumber)
	if err := pushBranch(ctx, opts.ProjectPath, branch); err != nil {
		return nil, fmt.Errorf("git push %s: %w", branch, err)
	}

	body, err := RenderPRBody(opts)
	if err != nil {
		return nil, fmt.Errorf("render PR body: %w", err)
	}

	baseBranch := resolveDefaultBranch(ctx, opts.ProjectPath)
	title := fmt.Sprintf("[task %04d] %s", opts.TaskNumber, opts.TaskTitle)

	return createPR(ctx, owner, repo, branch, baseBranch, title, body, opts.DraftDefault, opts.GitHubHostname)
}

func verifyGHCLI(ctx context.Context, hostname string) error {
	args := []string{"auth", "status"}
	if hostname != "" {
		// `gh auth status --hostname <h>` returns non-zero when the user
		// is not authenticated against that specific Enterprise host —
		// exactly the gate we want before attempting `gh api` calls
		// against the same hostname.
		args = append(args, "--hostname", hostname)
	}
	cmd := commandContext(ctx, "gh", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %v", ErrGHUnavailable, err)
	}
	return nil
}

// originHTTPSPattern matches https://<host>/<owner>/<repo>(.git)? URLs.
// The host is captured so callers can validate it against either
// github.com (default) or a user-supplied Enterprise hostname.
var originHTTPSPattern = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+?)(?:\.git)?/?$`)

// originSSHPattern matches git@<host>:<owner>/<repo>(.git)? URLs.
var originSSHPattern = regexp.MustCompile(`^git@([^:]+):([^/]+)/([^/]+?)(?:\.git)?$`)

// parseGitHubOrigin runs `git remote get-url origin` in projectPath and
// extracts <owner>/<repo>. Returns ErrNotGitHub for anything that isn't a
// recognisable URL on github.com OR on the user-supplied `enterpriseHost`
// (passed through from `OpenPROptions.GitHubHostname`). Empty
// `enterpriseHost` accepts only github.com — preserves v7.0 behaviour.
func parseGitHubOrigin(ctx context.Context, projectPath, enterpriseHost string) (owner, repo string, err error) {
	cmd := commandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = projectPath
	out, runErr := cmd.Output()
	if runErr != nil {
		return "", "", fmt.Errorf("%w: git remote get-url origin failed: %v", ErrNotGitHub, runErr)
	}
	url := strings.TrimSpace(string(out))
	if host, o, r, ok := matchGitHubOriginURL(url); ok {
		if hostMatches(host, enterpriseHost) {
			return o, r, nil
		}
	}
	return "", "", fmt.Errorf("%w: %q", ErrNotGitHub, url)
}

// matchGitHubOriginURL returns the host + owner + repo for an https or
// ssh github-style URL. ok=false on a URL we cannot parse.
func matchGitHubOriginURL(url string) (host, owner, repo string, ok bool) {
	if m := originHTTPSPattern.FindStringSubmatch(url); m != nil {
		return strings.ToLower(m[1]), m[2], m[3], true
	}
	if m := originSSHPattern.FindStringSubmatch(url); m != nil {
		return strings.ToLower(m[1]), m[2], m[3], true
	}
	return "", "", "", false
}

// hostMatches returns true if the URL's host is the canonical github.com
// (when enterpriseHost is empty) or matches the user-supplied Enterprise
// hostname. The comparison is case-insensitive on both sides.
func hostMatches(urlHost, enterpriseHost string) bool {
	urlHost = strings.ToLower(urlHost)
	if enterpriseHost == "" {
		return urlHost == "github.com"
	}
	enterpriseHost = strings.ToLower(strings.TrimSpace(enterpriseHost))
	if enterpriseHost == "" {
		return urlHost == "github.com"
	}
	return urlHost == enterpriseHost
}

func pushBranch(ctx context.Context, projectPath, branch string) error {
	cmd := commandContext(ctx, "git", "push", "--force-with-lease", "-u", "origin", branch)
	cmd.Dir = projectPath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// resolveDefaultBranch asks git which branch HEAD points at on the remote.
// Falls back to "main" if the symbolic-ref lookup fails — that's a sane
// default for a fresh GitHub repo and keeps the merge path unblocked.
func resolveDefaultBranch(ctx context.Context, projectPath string) string {
	cmd := commandContext(ctx, "git", "symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	val := strings.TrimSpace(string(out))
	val = strings.TrimPrefix(val, "origin/")
	if val == "" {
		return "main"
	}
	return val
}

// ghAPIResponse models the subset of `gh api .../pulls` JSON we actually need.
type ghAPIResponse struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

func createPR(ctx context.Context, owner, repo, head, base, title, body string, draft bool, hostname string) (*PRResult, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls", owner, repo)
	args := []string{
		"api", "-X", "POST", endpoint,
		"--field", "title=" + title,
		"--field", "head=" + head,
		"--field", "base=" + base,
		"--field", "body=" + body,
		"-F", fmt.Sprintf("draft=%t", draft),
	}
	if hostname != "" {
		// gh routes `--hostname <h>` to the Enterprise instance for
		// both auth resolution and the API endpoint. The flag must
		// precede the api subcommand for the parser to honour it.
		args = append([]string{"--hostname", hostname}, args...)
	}
	cmd := commandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh api %s failed: %v (stderr: %s)", endpoint, err, strings.TrimSpace(stderr.String()))
	}

	var resp ghAPIResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("gh api %s: parse response: %w", endpoint, err)
	}
	if resp.HTMLURL == "" || resp.Number == 0 {
		return nil, fmt.Errorf("gh api %s: unexpected response: %s", endpoint, strings.TrimSpace(stdout.String()))
	}
	return &PRResult{URL: resp.HTMLURL, Number: resp.Number, Branch: head}, nil
}

// RenderPRBody renders the PR body template against task metadata + v6.0
// diff stats. Exported so the unit test golden can call it directly without
// going through OpenPR's exec dance.
func RenderPRBody(opts OpenPROptions) (string, error) {
	data := prBodyData{
		ProjectID:          opts.ProjectID,
		TaskNumber:         opts.TaskNumber,
		TaskTitle:          opts.TaskTitle,
		TaskPrompt:         strings.TrimSpace(opts.TaskPrompt),
		AcceptanceCriteria: strings.TrimSpace(opts.AcceptanceCriteria),
		Agent:              opts.Agent,
		CompletedAt:        opts.CompletedAt.UTC(),
	}
	if data.AcceptanceCriteria == "" {
		data.AcceptanceCriteria = "_(none specified)_"
	}
	if data.Agent == "" {
		data.Agent = "unknown"
	}

	if set, err := loadDiffStats(opts); err == nil && set != nil {
		data.Files = make([]fileDiff, 0, len(set.Files))
		for _, f := range set.Files {
			adds, dels := countAddsDels(f)
			data.Files = append(data.Files, fileDiff{Path: f.Path, Additions: adds, Deletions: dels})
		}
		data.TotalAdditions = set.TotalAdditions
		data.TotalDeletions = set.TotalDeletions
		data.Truncated = set.Truncated
	}

	tmpl, err := template.New("pr_body").Funcs(template.FuncMap{
		"rfc3339": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	}).Parse(relay.PRBodyTemplate)
	if err != nil {
		return "", fmt.Errorf("parse PR body template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute PR body template: %w", err)
	}
	return buf.String(), nil
}

// diffStatsFn lets tests inject a deterministic FileDiffSet without spinning
// up a real git repo. Production calls go through diff.TaskDiff.
var diffStatsFn = func(projectPath, projectID string, taskNumber int) (*diff.FileDiffSet, error) {
	return diff.TaskDiff(projectPath, projectID, taskNumber)
}

func loadDiffStats(opts OpenPROptions) (*diff.FileDiffSet, error) {
	if opts.ProjectPath == "" || opts.TaskNumber <= 0 {
		return nil, nil
	}
	return diffStatsFn(opts.ProjectPath, opts.ProjectID, opts.TaskNumber)
}

func countAddsDels(f diff.FileDiff) (int, int) {
	adds, dels := 0, 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.LineAdd:
				adds++
			case diff.LineDel:
				dels++
			}
		}
	}
	return adds, dels
}
