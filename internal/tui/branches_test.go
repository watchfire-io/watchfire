package tui

import (
	"testing"
	"time"

	pb "github.com/watchfire-io/watchfire/proto"
)

func TestBranchesOverlayEmpty(t *testing.T) {
	state := BranchesState{}
	out := renderBranchesOverlay(state, "watchfire", 92, time.Now())
	mustContain(t, out, "Branches")
	mustContain(t, out, "watchfire")
	mustContain(t, out, "No branches yet")
	mustContain(t, out, "0 branches · 0 unmerged · 0 merged-orphans")
	mustContain(t, out, "esc")
}

func TestBranchesOverlayLoading(t *testing.T) {
	state := BranchesState{Loading: true}
	out := renderBranchesOverlay(state, "watchfire", 80, time.Now())
	mustContain(t, out, "Loading…")
}

func TestBranchesOverlayError(t *testing.T) {
	state := BranchesState{Err: errString("boom")}
	out := renderBranchesOverlay(state, "watchfire", 80, time.Now())
	mustContain(t, out, "Failed to load")
	mustContain(t, out, "boom")
}

// TestBranchesOverlayPopulated renders three branches — one unmerged with
// worktree, one merged with worktree, one merged-orphan (merged + no
// worktree) — and asserts the columns + rollup all show through. The
// merged-orphan row should render `absent*` (the orange-coloured marker
// the prune action targets).
func TestBranchesOverlayPopulated(t *testing.T) {
	state := BranchesState{
		Branches: []*pb.Branch{
			{
				Name:            "watchfire/0087",
				TaskNumber:      87,
				Status:          "unmerged",
				WorktreePath:    "/tmp/0087",
				CommitTimestamp: time.Now().Add(-3 * time.Hour).Unix(),
			},
			{
				Name:            "watchfire/0085",
				TaskNumber:      85,
				Status:          "merged",
				WorktreePath:    "/tmp/0085",
				CommitTimestamp: time.Now().Add(-2 * 24 * time.Hour).Unix(),
			},
			{
				Name:            "watchfire/0083",
				TaskNumber:      83,
				Status:          "merged",
				WorktreePath:    "",
				CommitTimestamp: time.Now().Add(-4 * 24 * time.Hour).Unix(),
			},
		},
	}
	out := renderBranchesOverlay(state, "watchfire", 90, time.Now())

	mustContain(t, out, "watchfire/0087")
	mustContain(t, out, "#0087")
	mustContain(t, out, "3h")
	mustContain(t, out, "unmerged")
	mustContain(t, out, "present")

	mustContain(t, out, "watchfire/0085")
	mustContain(t, out, "#0085")
	mustContain(t, out, "2d")
	mustContain(t, out, "merged")

	mustContain(t, out, "watchfire/0083")
	mustContain(t, out, "absent*")

	// Rollup: 3 total · 1 unmerged · 1 merged-orphan.
	mustContain(t, out, "3 branches · 1 unmerged · 1 merged-orphans")
	mustContain(t, out, "Branch")
	mustContain(t, out, "Task")
	mustContain(t, out, "Age")
	mustContain(t, out, "Status")
	mustContain(t, out, "Worktree")
}

// TestBranchesPendingSuffix asserts the in-flight row gets the
// `(merging…)` suffix while the RPC is in flight — the spec calls this
// out as the affordance that other actions are blocked.
func TestBranchesPendingSuffix(t *testing.T) {
	state := BranchesState{
		Branches: []*pb.Branch{
			{Name: "watchfire/0087", TaskNumber: 87, Status: "unmerged", CommitTimestamp: time.Now().Add(-time.Hour).Unix()},
		},
		PendingBranch: "watchfire/0087",
		PendingAction: "merging",
	}
	out := renderBranchesOverlay(state, "watchfire", 80, time.Now())
	mustContain(t, out, "(merging…)")
}

// TestBranchesConfirmFooter asserts a pending confirm prompt is surfaced
// in the footer position, replacing the keymap hint.
func TestBranchesConfirmFooter(t *testing.T) {
	state := BranchesState{
		Branches:      []*pb.Branch{{Name: "watchfire/0087", Status: "merged", CommitTimestamp: time.Now().Unix()}},
		ConfirmPrompt: "Delete merged branch watchfire/0087? y/N",
		ConfirmKind:   branchConfirmDelete,
		ConfirmTarget: "watchfire/0087",
	}
	out := renderBranchesOverlay(state, "watchfire", 80, time.Now())
	mustContain(t, out, "Delete merged branch watchfire/0087?")
}

func TestFormatRelativeAge(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		secondsAgo int64
		want       string
	}{
		{0, "—"},
		{30, "now"},
		{120, "2m"},
		{3 * 3600, "3h"},
		{2 * 24 * 3600, "2d"},
		{8 * 24 * 3600, "1w"},
		{45 * 24 * 3600, "1mo"},
	}
	for _, c := range cases {
		var ts int64
		if c.secondsAgo > 0 {
			ts = now.Add(-time.Duration(c.secondsAgo) * time.Second).Unix()
		}
		got := formatRelativeAge(ts, now)
		if got != c.want {
			t.Errorf("formatRelativeAge(secondsAgo=%d) = %q, want %q", c.secondsAgo, got, c.want)
		}
	}
}

func TestIsMergedOrphan(t *testing.T) {
	cases := []struct {
		name string
		br   *pb.Branch
		want bool
	}{
		{"merged-with-worktree", &pb.Branch{Status: "merged", WorktreePath: "/tmp/x"}, false},
		{"merged-no-worktree", &pb.Branch{Status: "merged", WorktreePath: ""}, true},
		{"unmerged-with-worktree", &pb.Branch{Status: "unmerged", WorktreePath: "/tmp/x"}, false},
		{"unmerged-no-worktree", &pb.Branch{Status: "unmerged", WorktreePath: ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsMergedOrphan(c.br); got != c.want {
				t.Errorf("IsMergedOrphan(%+v) = %v, want %v", c.br, got, c.want)
			}
		})
	}
}

func TestBranchesStateSetBranchesClampsCursor(t *testing.T) {
	s := &BranchesState{Cursor: 5}
	s.SetBranches([]*pb.Branch{{Name: "a"}, {Name: "b"}})
	if s.Cursor != 1 {
		t.Errorf("expected cursor clamped to 1, got %d", s.Cursor)
	}

	s.SetBranches(nil)
	if s.Cursor != 0 {
		t.Errorf("expected cursor reset to 0, got %d", s.Cursor)
	}
}

func TestBranchesStateMoveUpDown(t *testing.T) {
	s := &BranchesState{
		Branches: []*pb.Branch{{Name: "a"}, {Name: "b"}, {Name: "c"}},
	}
	s.MoveDown()
	s.MoveDown()
	if s.Cursor != 2 {
		t.Errorf("MoveDown twice → cursor=2, got %d", s.Cursor)
	}
	s.MoveDown()
	if s.Cursor != 2 {
		t.Errorf("MoveDown clamps at end, want 2, got %d", s.Cursor)
	}
	s.MoveUp()
	if s.Cursor != 1 {
		t.Errorf("MoveUp → 1, got %d", s.Cursor)
	}
}

func TestBranchesStateCountMergedOrphans(t *testing.T) {
	s := &BranchesState{
		Branches: []*pb.Branch{
			{Status: "merged", WorktreePath: ""},
			{Status: "merged", WorktreePath: "/tmp/x"},
			{Status: "merged", WorktreePath: ""},
			{Status: "unmerged", WorktreePath: ""},
		},
	}
	if got := s.CountMergedOrphans(); got != 2 {
		t.Errorf("CountMergedOrphans = %d, want 2", got)
	}
}
