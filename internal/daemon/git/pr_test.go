package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/diff"
)

// TestMain compiles the fake `gh` binary (testdata/fakegh) once and exposes
// its directory via the WATCHFIRE_FAKEGH_DIR env var. Each test that wants
// to put it on PATH calls withFakeGH(t).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "watchfire-fakegh-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	exe := "gh"
	if runtime.GOOS == "windows" {
		exe = "gh.exe"
	}
	binPath := filepath.Join(dir, exe)
	build := exec.Command("go", "build", "-o", binPath, "./testdata/fakegh")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("failed to build fakegh: " + err.Error())
	}
	if err := os.Setenv("WATCHFIRE_FAKEGH_DIR", dir); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// withFakeGH prepends the fake-gh dir to PATH for the duration of the test.
// Returns the path that gh writes argv to (FAKEGH_LOG).
func withFakeGH(t *testing.T, mode string) string {
	t.Helper()
	dir := os.Getenv("WATCHFIRE_FAKEGH_DIR")
	if dir == "" {
		t.Fatal("WATCHFIRE_FAKEGH_DIR not set — TestMain didn't run?")
	}
	logPath := filepath.Join(t.TempDir(), "fakegh.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKEGH_LOG", logPath)
	t.Setenv("FAKEGH_MODE", mode)
	return logPath
}

// withEmptyPATH wipes PATH for the duration of the test so the production
// `exec.LookPath` for `gh` fails. We can't simply rely on a missing fake
// binary because the test host may have a real gh installed.
func withEmptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
}

func newTempGitRepo(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "initial")
	run("checkout", "-b", "watchfire/0042")
	run("commit", "--allow-empty", "-m", "task work")
	run("checkout", "main")
	run("remote", "add", "origin", originURL)
	return dir
}

// TestParseGitHubOriginHTTPS covers the canonical HTTPS origin shape.
func TestParseGitHubOriginHTTPS(t *testing.T) {
	t.Parallel()
	repo := newTempGitRepo(t, "https://github.com/owner/repo.git")
	owner, name, err := parseGitHubOrigin(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("parseGitHubOrigin: %v", err)
	}
	if owner != "owner" || name != "repo" {
		t.Errorf("got %s/%s, want owner/repo", owner, name)
	}
}

// TestParseGitHubOriginSSH covers the git@github.com:owner/repo shape.
func TestParseGitHubOriginSSH(t *testing.T) {
	t.Parallel()
	repo := newTempGitRepo(t, "git@github.com:owner/repo.git")
	owner, name, err := parseGitHubOrigin(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("parseGitHubOrigin: %v", err)
	}
	if owner != "owner" || name != "repo" {
		t.Errorf("got %s/%s, want owner/repo", owner, name)
	}
}

// TestParseGitHubOriginNonGitHub asserts a gitlab URL maps to ErrNotGitHub.
func TestParseGitHubOriginNonGitHub(t *testing.T) {
	t.Parallel()
	repo := newTempGitRepo(t, "https://gitlab.com/owner/repo.git")
	_, _, err := parseGitHubOrigin(context.Background(), repo, "")
	if !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("got %v, want ErrNotGitHub", err)
	}
}

// TestParseGitHubOriginEnterpriseHTTPS exercises the v8.x path: a
// non-github.com URL is accepted when the user has paired the inbound
// config with their Enterprise hostname.
func TestParseGitHubOriginEnterpriseHTTPS(t *testing.T) {
	t.Parallel()
	repo := newTempGitRepo(t, "https://github.example.com/owner/repo.git")
	owner, name, err := parseGitHubOrigin(context.Background(), repo, "github.example.com")
	if err != nil {
		t.Fatalf("parseGitHubOrigin (enterprise): %v", err)
	}
	if owner != "owner" || name != "repo" {
		t.Errorf("got %s/%s, want owner/repo", owner, name)
	}
}

// TestParseGitHubOriginEnterpriseHostMismatch — Enterprise hostname is
// pinned to one host; an origin pointing at a different host returns
// ErrNotGitHub even with an Enterprise pairing configured.
func TestParseGitHubOriginEnterpriseHostMismatch(t *testing.T) {
	t.Parallel()
	repo := newTempGitRepo(t, "https://github.somewhere-else.com/owner/repo.git")
	_, _, err := parseGitHubOrigin(context.Background(), repo, "github.example.com")
	if !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("got %v, want ErrNotGitHub", err)
	}
}

// TestVerifyGHCLIMissing — empty PATH means `gh` can't be located.
func TestVerifyGHCLIMissing(t *testing.T) {
	withEmptyPATH(t)
	err := verifyGHCLI(context.Background(), "")
	if !errors.Is(err, ErrGHUnavailable) {
		t.Fatalf("got %v, want ErrGHUnavailable", err)
	}
}

// TestVerifyGHCLIUnauthenticated — fake gh returns non-zero from `auth status`.
func TestVerifyGHCLIUnauthenticated(t *testing.T) {
	withFakeGH(t, "unauthenticated")
	err := verifyGHCLI(context.Background(), "")
	if !errors.Is(err, ErrGHUnavailable) {
		t.Fatalf("got %v, want ErrGHUnavailable", err)
	}
}

// TestOpenPRHappyPath — the full flow against a temp repo + fake gh,
// verifying the api argv carries title / head / base / body / draft.
func TestOpenPRHappyPath(t *testing.T) {
	logPath := withFakeGH(t, "")
	repo := newTempGitRepo(t, "https://github.com/owner/repo.git")
	// Add a fake remote-tracking ref so resolveDefaultBranch returns a value.
	mustGit(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	// Stub the diff stats so RenderPRBody doesn't shell out to a non-existent
	// task YAML and so the test asserts the file-list block deterministically.
	prevDiff := diffStatsFn
	diffStatsFn = func(string, string, int) (*diff.FileDiffSet, error) {
		return &diff.FileDiffSet{
			Files: []diff.FileDiff{
				{Path: "internal/foo.go", Hunks: []diff.Hunk{{Lines: []diff.DiffLine{
					{Kind: diff.LineAdd}, {Kind: diff.LineAdd}, {Kind: diff.LineDel},
				}}}},
			},
			TotalAdditions: 2,
			TotalDeletions: 1,
		}, nil
	}
	t.Cleanup(func() { diffStatsFn = prevDiff })

	// Bare upstream redirected via pushInsteadOf — the origin URL stays
	// "https://github.com/owner/repo.git" so parseGitHubOrigin sees a real
	// github URL, but `git push origin` is rewritten to the local file:// path.
	upstream := t.TempDir()
	mustGit(t, upstream, "init", "--bare")
	mustGit(t, repo, "config", "url."+upstream+".pushInsteadOf", "https://github.com/owner/repo.git")

	res, err := OpenPR(context.Background(), OpenPROptions{
		ProjectPath:        repo,
		ProjectID:          "proj-1",
		TaskNumber:         42,
		TaskTitle:          "auto-PR happy path",
		TaskPrompt:         "Open a PR.",
		AcceptanceCriteria: "PR opened.",
		Agent:              "claude-code",
		DraftDefault:       true,
		CompletedAt:        time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if res.URL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("URL = %q, want canned URL", res.URL)
	}
	if res.Number != 42 || res.Branch != "watchfire/0042" {
		t.Errorf("got Number=%d Branch=%q, want 42 / watchfire/0042", res.Number, res.Branch)
	}

	// Assert the fakegh log captured a `gh api -X POST /repos/owner/repo/pulls`
	// invocation with title / head / base / body / draft fields.
	logBytes, err := os.ReadFile(logPath) //nolint:gosec // test-controlled
	if err != nil {
		t.Fatalf("read fakegh log: %v", err)
	}
	logStr := string(logBytes)
	wantSubstrs := []string{
		"api\x1f-X\x1fPOST\x1frepos/owner/repo/pulls",
		"title=[task 0042] auto-PR happy path",
		"head=watchfire/0042",
		"base=main",
		"draft=true",
	}
	for _, want := range wantSubstrs {
		if !strings.Contains(logStr, want) {
			t.Errorf("fakegh log missing %q\n--- log ---\n%s", want, logStr)
		}
	}
}

// TestOpenPRPushFailure — origin parses as github.com but the actual push
// destination doesn't exist → push returns non-zero. Verifies the error is
// not wrapped as ErrGHUnavailable / ErrNotGitHub so the caller logs ERROR.
func TestOpenPRPushFailure(t *testing.T) {
	withFakeGH(t, "")
	repo := newTempGitRepo(t, "https://github.com/owner/repo.git")
	mustGit(t, repo, "config", "url./nonexistent/bare/repo.pushInsteadOf", "https://github.com/owner/repo.git")

	prevDiff := diffStatsFn
	diffStatsFn = func(string, string, int) (*diff.FileDiffSet, error) {
		return &diff.FileDiffSet{}, nil
	}
	t.Cleanup(func() { diffStatsFn = prevDiff })

	_, err := OpenPR(context.Background(), OpenPROptions{
		ProjectPath: repo,
		ProjectID:   "proj-1",
		TaskNumber:  42,
		TaskTitle:   "push fail",
	})
	if err == nil {
		t.Fatal("expected push failure, got nil")
	}
	if errors.Is(err, ErrGHUnavailable) || errors.Is(err, ErrNotGitHub) {
		t.Errorf("push failure should not be ErrGHUnavailable / ErrNotGitHub: %v", err)
	}
}

// TestOpenPRAPIFailure — `gh api` returns non-zero (e.g. 422 unprocessable).
// Same wrapping rule as push failure: caller still falls back, but loud.
func TestOpenPRAPIFailure(t *testing.T) {
	withFakeGH(t, "api_fail")
	repo := newTempGitRepo(t, "https://github.com/owner/repo.git")
	upstream := t.TempDir()
	mustGit(t, upstream, "init", "--bare")
	mustGit(t, repo, "config", "url."+upstream+".pushInsteadOf", "https://github.com/owner/repo.git")

	prevDiff := diffStatsFn
	diffStatsFn = func(string, string, int) (*diff.FileDiffSet, error) {
		return &diff.FileDiffSet{}, nil
	}
	t.Cleanup(func() { diffStatsFn = prevDiff })

	_, err := OpenPR(context.Background(), OpenPROptions{
		ProjectPath: repo,
		ProjectID:   "proj-1",
		TaskNumber:  42,
		TaskTitle:   "api fail",
	})
	if err == nil {
		t.Fatal("expected gh api failure, got nil")
	}
	if !strings.Contains(err.Error(), "gh api") {
		t.Errorf("error should mention gh api: %v", err)
	}
}

// TestRenderPRBodyGolden locks down the rendered Markdown shape so future
// template tweaks are explicit. Uses an injected FileDiffSet so the test is
// hermetic. Not parallel — mutates the package-level diffStatsFn.
func TestRenderPRBodyGolden(t *testing.T) {
	prevDiff := diffStatsFn
	diffStatsFn = func(string, string, int) (*diff.FileDiffSet, error) {
		return &diff.FileDiffSet{
			Files: []diff.FileDiff{
				{Path: "internal/daemon/git/pr.go", Hunks: []diff.Hunk{{Lines: []diff.DiffLine{
					{Kind: diff.LineAdd}, {Kind: diff.LineAdd}, {Kind: diff.LineAdd},
					{Kind: diff.LineDel},
				}}}},
				{Path: "CHANGELOG.md", Hunks: []diff.Hunk{{Lines: []diff.DiffLine{
					{Kind: diff.LineAdd},
				}}}},
			},
			TotalAdditions: 4,
			TotalDeletions: 1,
			Truncated:      false,
		}, nil
	}
	t.Cleanup(func() { diffStatsFn = prevDiff })

	body, err := RenderPRBody(OpenPROptions{
		ProjectPath:        "/tmp/proj",
		ProjectID:          "proj-1",
		TaskNumber:         65,
		TaskTitle:          "GitHub auto-PR",
		TaskPrompt:         "Open a PR instead of merging.",
		AcceptanceCriteria: "PR opened with body rendered from task metadata.",
		Agent:              "claude-code",
		CompletedAt:        time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderPRBody: %v", err)
	}

	wantSubstrs := []string{
		"_Auto-opened by Watchfire from task 0065._",
		"## Task\n**GitHub auto-PR**",
		"Open a PR instead of merging.",
		"PR opened with body rendered from task metadata.",
		"`claude-code` · run completed 2026-05-02T10:30:00Z",
		"## Files changed (2)",
		"- `internal/daemon/git/pr.go` · +3 −1",
		"- `CHANGELOG.md` · +1 −0",
		"Total: **+4 −1**",
		"watchfire://project/proj-1/task/0065",
	}
	for _, want := range wantSubstrs {
		if !strings.Contains(body, want) {
			t.Errorf("rendered body missing %q\n--- body ---\n%s", want, body)
		}
	}

	// Truncation marker only appears when Truncated == true.
	if strings.Contains(body, "(diff truncated)") {
		t.Errorf("body should not carry truncation marker:\n%s", body)
	}
}

// TestRenderPRBodyTruncationMarker — the marker appears when Truncated is set.
// Not parallel — mutates the package-level diffStatsFn.
func TestRenderPRBodyTruncationMarker(t *testing.T) {
	prevDiff := diffStatsFn
	diffStatsFn = func(string, string, int) (*diff.FileDiffSet, error) {
		return &diff.FileDiffSet{Files: []diff.FileDiff{}, Truncated: true}, nil
	}
	t.Cleanup(func() { diffStatsFn = prevDiff })

	body, err := RenderPRBody(OpenPROptions{
		ProjectPath: "/tmp/proj",
		ProjectID:   "p",
		TaskNumber:  1,
		TaskTitle:   "x",
		Agent:       "claude-code",
		CompletedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("RenderPRBody: %v", err)
	}
	if !strings.Contains(body, "(diff truncated)") {
		t.Errorf("expected (diff truncated) marker in body:\n%s", body)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
	}
}
