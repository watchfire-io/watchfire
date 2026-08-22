//go:build darwin

package agent

import (
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"regexp"
)

// claudeExpectedProfile is the canonical seatbelt profile we produced before
// the SandboxExtras refactor. Any deviation here means the rendered profile
// for a Claude session is no longer byte-equivalent to pre-refactor output,
// which would be a regression in Claude sandboxing. Placeholders use
// deterministic fake paths so the golden doesn't depend on the runner's
// $HOME or CWD.
const claudeExpectedProfile = `(version 1)
(deny default)

; READ ACCESS - Allow all, deny sensitive
(allow file-read* (subpath "/"))

; DENY sensitive credential paths
(deny file-read* (subpath "/home/test/.ssh"))
(deny file-read* (subpath "/home/test/.aws"))
(deny file-read* (subpath "/home/test/.gnupg"))
(deny file-read* (literal "/home/test/.netrc"))
(deny file-read* (literal "/home/test/.npmrc"))

; DENY protected user folders (prevents TCC prompts)
(deny file-read* (subpath "/home/test/Desktop"))
(deny file-read* (subpath "/home/test/Documents"))
(deny file-read* (subpath "/home/test/Downloads"))
(deny file-read* (subpath "/home/test/Music"))
(deny file-read* (subpath "/home/test/Movies"))
(deny file-read* (subpath "/home/test/Pictures"))
; NOTE: Keychains allowed - required for agent auth

; WRITE ACCESS - Project + agent extras + caches + temp
(allow file-write* (subpath "/projects/foo"))
(allow file-write* (subpath "/home/test/.claude"))
(allow file-write* (literal "/home/test/.claude.json"))
(allow file-write* (subpath "/home/test/Library/Caches/claude-cli-nodejs"))
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "/private/tmp"))
(allow file-write* (subpath "/var/folders"))
(allow file-write* (subpath "/private/var/folders"))

; PACKAGE MANAGER CACHES
(allow file-write* (subpath "/home/test/.npm"))
(allow file-write* (subpath "/home/test/.yarn"))
(allow file-write* (subpath "/home/test/.pnpm-store"))
(allow file-write* (subpath "/home/test/.cache"))
(allow file-write* (subpath "/home/test/.local/share/pnpm"))
(allow file-write* (subpath "/home/test/Library/Caches/npm"))
(allow file-write* (subpath "/home/test/Library/Caches/yarn"))

; CLI TOOL CONFIG (Vercel, Firebase, gcloud, etc.)
(allow file-write* (subpath "/home/test/Library/Application Support"))

; KEYCHAIN - Agent auth token persistence
(allow file-write* (literal "/home/test/Library/Keychains/login.keychain-db"))
(allow file-write* (literal "/home/test/Library/Keychains/login.keychain-db-shm"))
(allow file-write* (literal "/home/test/Library/Keychains/login.keychain-db-wal"))
(allow file-write* (regex #"^/home/test/Library/Keychains/[^/]+/keychain-2\.db(-shm|-wal|-journal)?$"))

; DEV TOOL CACHES
(allow file-write* (subpath "/home/test/.cargo"))
(allow file-write* (subpath "/home/test/go"))
(allow file-write* (subpath "/home/test/.rustup"))

; PROTECTED - Block writes even in project
(deny file-write* (regex #"/\.env($|[^/]*)"))
(deny file-write* (subpath "/projects/foo/.git/hooks"))

; NETWORK, DEVICES, PROCESS, IPC
(allow network*)
(allow file-read* (subpath "/dev"))
(allow file-write* (subpath "/dev"))
(allow process-exec*)
(allow process-fork)
(allow process-info*)
(allow signal)
(allow mach*)
(allow sysctl*)
(allow ipc*)
(allow file-ioctl)
`

// TestGenerateProfile_ClaudeSnapshot verifies that rendering the seatbelt
// profile for a Claude session with the backend's SandboxExtras produces
// the exact same text as the pre-refactor hardcoded policy. This guards
// against accidental drift when the extras plumbing changes.
func TestGenerateProfile_ClaudeSnapshot(t *testing.T) {
	claude := &backend.Claude{}
	policy := DefaultPolicy("/home/test", "/projects/foo", claude.SandboxExtras())
	got := GenerateProfile(policy)

	if got != claudeExpectedProfile {
		t.Fatalf("profile drift from pre-refactor baseline.\n--- diff (first mismatch) ---\n%s", firstLineDiff(claudeExpectedProfile, got))
	}
}

// TestGenerateProfile_NoExtras verifies that a backend contributing no
// extras produces a profile with no agent-specific writable paths — only
// the project dir and the generic caches.
func TestGenerateProfile_NoExtras(t *testing.T) {
	policy := DefaultPolicy("/home/test", "/projects/foo", backend.SandboxExtras{})
	got := GenerateProfile(policy)

	forbidden := []string{
		"/home/test/.claude",
		"/home/test/.claude.json",
		"/home/test/Library/Caches/claude-cli-nodejs",
	}
	for _, s := range forbidden {
		if strings.Contains(got, s) {
			t.Errorf("profile unexpectedly contains agent-specific path %q", s)
		}
	}
	if !strings.Contains(got, `(allow file-write* (subpath "/projects/foo"))`) {
		t.Error("profile missing project dir write allow")
	}
}

// TestGenerateProfile_DataProtectionKeychain pins the write rule for the
// modern (10.15+) data-protection keychain. The profile long allowed
// only the legacy login.keychain-db literals, so an agent that
// re-authenticated inside a sandboxed session had nowhere to persist the
// refreshed OAuth token: Claude Code printed "Login successful" and the
// next turn still failed with 401, having fallen back to API-key
// billing. The UUID container is per-machine, hence a regex.
func TestGenerateProfile_DataProtectionKeychain(t *testing.T) {
	policy := DefaultPolicy("/home/test", "/projects/foo", backend.SandboxExtras{})
	got := GenerateProfile(policy)

	want := `(allow file-write* (regex #"^/home/test/Library/Keychains/[^/]+/keychain-2\.db(-shm|-wal|-journal)?$"))`
	if !strings.Contains(got, want) {
		t.Fatalf("profile missing data-protection keychain write allow:\nwant line: %s", want)
	}

	// The rule must stay scoped: neighbouring keychain files an agent
	// never needs to write must not become writable by widening it.
	re := regexp.MustCompile(`\^/home/test/Library/Keychains/\[\^/\]\+/keychain-2\\\.db\(-shm\|-wal\|-journal\)\?\$`)
	if !re.MatchString(got) {
		t.Error("keychain-2 regex is not anchored at both ends")
	}
	for _, unwanted := range []string{
		`(allow file-write* (subpath "/home/test/Library/Keychains"))`,
		"user.kb",
		"TrustedPeersHelper",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("keychain allow is too broad — profile contains %q", unwanted)
		}
	}
}

// TestKeychainV2Pattern_MatchesRealPaths checks the emitted pattern
// against the paths macOS actually uses, and against the ones it must
// not cover.
func TestKeychainV2Pattern_MatchesRealPaths(t *testing.T) {
	re := regexp.MustCompile(keychainV2Pattern("/home/test/Library/Keychains"))
	base := "/home/test/Library/Keychains/B05D070B-B2D3-590A-89B0-CDF11334F2C1/"
	for _, ok := range []string{"keychain-2.db", "keychain-2.db-shm", "keychain-2.db-wal", "keychain-2.db-journal"} {
		if !re.MatchString(base + ok) {
			t.Errorf("pattern should match %q", base+ok)
		}
	}
	for _, bad := range []string{
		base + "user.kb",
		base + "com.apple.security.keychain-defaultContext.TrustedPeersHelper.db",
		base + "sub/keychain-2.db",
		"/home/test/Library/Keychains/keychain-2.db",
		base + "keychain-2.db.bak",
	} {
		if re.MatchString(bad) {
			t.Errorf("pattern must NOT match %q", bad)
		}
	}
}

// firstLineDiff returns a compact summary of the first differing line
// between expected and actual profile strings.
func firstLineDiff(expected, got string) string {
	el := strings.Split(expected, "\n")
	gl := strings.Split(got, "\n")
	n := len(el)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if el[i] != gl[i] {
			return "line " + itoa(i+1) + ":\n  want: " + el[i] + "\n   got: " + gl[i]
		}
	}
	if len(el) != len(gl) {
		return "line count mismatch: want=" + itoa(len(el)) + " got=" + itoa(len(gl))
	}
	return "(no diff found — byte identical?)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
