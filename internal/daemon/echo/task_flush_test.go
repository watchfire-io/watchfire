package echo

import "testing"

func TestParseTaskBranch(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"watchfire/42", 42, true},
		{"watchfire/0042", 42, true},
		{"watchfire/00000001", 1, true},
		{"refs/heads/watchfire/0042", 42, true},
		{"  watchfire/7  ", 7, true},
		{"feature/foo", 0, false},
		{"watchfire/", 0, false},
		{"watchfire/0", 0, false},
		{"watchfire/abc", 0, false},
		{"watchfire/12/sub", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseTaskBranch(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("ParseTaskBranch(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/org/repo", "github.com/org/repo"},
		{"https://github.com/org/repo.git", "github.com/org/repo"},
		{"https://github.com/org/repo/", "github.com/org/repo"},
		{"git@github.com:org/repo.git", "github.com/org/repo"},
		{"git@github.com:org/repo", "github.com/org/repo"},
		{"https://github.example.com/org/repo", "github.example.com/org/repo"},
		{"https://gitlab.com/group/sub/repo", "gitlab.com/group/sub/repo"},
		{"https://bitbucket.org/team/repo.git", "bitbucket.org/team/repo"},
		{"HTTPS://GitHub.COM/org/Repo", "github.com/org/Repo"},
		{"", ""},
		{"not-a-url", ""},
	}
	for _, c := range cases {
		got := NormalizeRepoURL(c.in)
		if got != c.want {
			t.Errorf("NormalizeRepoURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHostFromRepoURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/org/repo", "github.com"},
		{"git@github.example.com:org/repo.git", "github.example.com"},
		{"https://gitlab.com/group/repo", "gitlab.com"},
		{"", ""},
	}
	for _, c := range cases {
		got := HostFromRepoURL(c.in)
		if got != c.want {
			t.Errorf("HostFromRepoURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHostFromBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.example.com", "github.example.com"},
		{"https://github.example.com/", "github.example.com"},
		{"github.example.com", "github.example.com"},
		{"http://gitlab.local:8080", "gitlab.local:8080"},
		{"", ""},
	}
	for _, c := range cases {
		got := HostFromBaseURL(c.in)
		if got != c.want {
			t.Errorf("HostFromBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
