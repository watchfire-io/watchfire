package tray

import (
	"fmt"
	"sort"
	"time"
)

// MaxIdleProjects caps the number of idle project rows in the Idle section.
// Anything above this is collapsed into a single "... and N more idle" overflow row.
const MaxIdleProjects = 50

// MaxNotifications caps the number of items in the Notifications submenu.
const MaxNotifications = 10

// truncTitle is the visible-character cap for "Working" project subtitles.
const truncTitle = 32

// ClickKind identifies the action a menu row triggers when clicked.
type ClickKind string

const (
	ClickNone           ClickKind = ""
	ClickFocusMain      ClickKind = "focus_main"
	ClickFocusTasks     ClickKind = "focus_tasks"
	ClickFocusTask      ClickKind = "focus_task"
	ClickFocusDigest    ClickKind = "focus_digest"
	ClickOpenWatchfire  ClickKind = "open_watchfire"
	ClickOpenDashboard  ClickKind = "open_dashboard"
	ClickOpenNotifLog   ClickKind = "open_notif_log"
	ClickQuitWatchfire  ClickKind = "quit_watchfire"
	ClickReloadNotifs   ClickKind = "reload_notifs"
	ClickUpdateAvail    ClickKind = "update_available"
)

// ClickAction is what a click on a MenuNode requests. Empty Kind means the
// row is not clickable.
type ClickAction struct {
	Kind       ClickKind
	ProjectID  string
	TaskNumber int32
	// DigestDate is the YYYY-MM-DD identifier for ClickFocusDigest actions.
	DigestDate string
}

// MenuNode is the unit of the tray menu tree. Section headers are rendered
// with Disabled=true and OnClick.Kind == ClickNone. Clickable rows have a
// non-empty OnClick.Kind. Submenus are represented via Children.
type MenuNode struct {
	Title    string
	Subtitle string
	Disabled bool
	OnClick  ClickAction
	Children []MenuNode
}

// ProjectStatus categorises a project for display in the tray menu.
type ProjectStatus int

const (
	ProjectIdle    ProjectStatus = iota
	ProjectWorking               // an autonomous (non-chat) agent is running
	ProjectFailed                // has at least one failed task
)

// ProjectMenuInfo carries everything BuildMenu needs to know about a single
// project. Compiled by the daemon before the call.
type ProjectMenuInfo struct {
	ProjectID    string
	ProjectName  string
	ProjectColor string
	Status       ProjectStatus
	// FailedCount is the number of tasks where status==done && success==false.
	// Surfaced as the subtitle for ProjectFailed entries.
	FailedCount int
	// CurrentTaskTitle is the running task's title (only meaningful when
	// Status==ProjectWorking).
	CurrentTaskTitle  string
	CurrentTaskNumber int32
}

// NotificationLogEntry is a single line of the on-disk notifications.log
// surfaced in the tray's Notifications submenu.
type NotificationLogEntry struct {
	ID          string
	ProjectID   string
	ProjectName string // resolved by the caller (notifications.log carries only the ID)
	TaskNumber  int32
	Kind        string // "TASK_FAILED" | "RUN_COMPLETE"
	Title       string
	Body        string
	// AgeText is a human-readable relative time, e.g. "2m ago". The tray
	// caller formats this so BuildMenu stays time-source-free for tests.
	AgeText string
}

// MenuInputs is the entire snapshot BuildMenu needs to produce a deterministic
// menu tree. Every input is owned by the caller — BuildMenu does no I/O.
type MenuInputs struct {
	DaemonRunning bool
	Projects      []ProjectMenuInfo
	// Notifications must already be filtered to today-only and sorted newest-first.
	Notifications []NotificationLogEntry
	// NotificationsTodayCount is the unclamped count for the "Notifications
	// (N today)" header. May exceed len(Notifications) when capped.
	NotificationsTodayCount int
	// LatestDigest is the most recently-emitted weekly digest, or zero-value
	// when none exists. Surfaced as the topmost row in the Notifications
	// submenu (v6.0 Ember).
	LatestDigest DigestEntry
	// UpdateAvailable, when true, surfaces the "Update Available — vX" row.
	UpdateAvailable bool
	UpdateVersion   string
}

// DigestEntry is a single weekly-digest summary surfaced in the tray.
type DigestEntry struct {
	// Date is the YYYY-MM-DD key the digest was persisted under.
	Date string
	// EmittedAt is the wall-clock timestamp the digest was emitted at.
	EmittedAt time.Time
}

// BuildMenu builds the static menu tree the tray should render for the given
// snapshot. The result is fully deterministic — same inputs always produce
// the same tree, so it goldens cleanly in tests.
//
// Layout:
//
//	[header]              Watchfire (running|stopped)
//	[separator]
//	[needs attention]     ⚠ Needs attention (N) + per-project rows
//	[working]             ● Working (N) + per-project rows
//	[idle]                ○ Idle (N) + per-project rows + overflow row
//	[separator]
//	[Open Watchfire]
//	[Open Dashboard…]
//	[separator]
//	[Notifications (N today) ▸] (submenu)
//	[separator]
//	[Update Available]    (when UpdateAvailable)
//	[Quit Watchfire]
//
// Empty sections are hidden entirely (no header rendered).
func BuildMenu(in MenuInputs) []MenuNode {
	out := make([]MenuNode, 0, 16)

	// Header — always-on status line.
	headerTitle := "Watchfire (stopped)"
	if in.DaemonRunning {
		headerTitle = "Watchfire (running)"
	}
	out = append(out, MenuNode{Title: headerTitle, Disabled: true})
	out = append(out, separator())

	attention, working, idle := bucketProjects(in.Projects)

	// Needs attention section.
	if len(attention) > 0 {
		out = append(out, MenuNode{
			Title:    fmt.Sprintf("⚠  Needs attention (%d)", len(attention)),
			Disabled: true,
		})
		for _, p := range attention {
			subtitle := fmt.Sprintf("%d failed task", p.FailedCount)
			if p.FailedCount != 1 {
				subtitle += "s"
			}
			out = append(out, MenuNode{
				Title:    fmt.Sprintf("• %s", p.ProjectName),
				Subtitle: subtitle,
				OnClick: ClickAction{
					Kind:      ClickFocusTasks,
					ProjectID: p.ProjectID,
				},
			})
		}
	}

	// Working section.
	if len(working) > 0 {
		out = append(out, MenuNode{
			Title:    fmt.Sprintf("●  Working (%d)", len(working)),
			Disabled: true,
		})
		for _, p := range working {
			row := MenuNode{
				Title: fmt.Sprintf("• %s", p.ProjectName),
				OnClick: ClickAction{
					Kind:      ClickFocusMain,
					ProjectID: p.ProjectID,
				},
			}
			if t := truncate(p.CurrentTaskTitle, truncTitle); t != "" {
				row.Subtitle = t
			}
			out = append(out, row)
		}
	}

	// Idle section.
	if len(idle) > 0 {
		out = append(out, MenuNode{
			Title:    fmt.Sprintf("○  Idle (%d)", len(idle)),
			Disabled: true,
		})
		shown := idle
		overflow := 0
		if len(shown) > MaxIdleProjects {
			overflow = len(shown) - MaxIdleProjects
			shown = shown[:MaxIdleProjects]
		}
		for _, p := range shown {
			out = append(out, MenuNode{
				Title: fmt.Sprintf("• %s", p.ProjectName),
				OnClick: ClickAction{
					Kind:      ClickFocusMain,
					ProjectID: p.ProjectID,
				},
			})
		}
		if overflow > 0 {
			out = append(out, MenuNode{
				Title:    fmt.Sprintf("… and %d more idle projects", overflow),
				Disabled: true,
			})
		}
	}

	out = append(out, separator())
	out = append(out, MenuNode{
		Title:   "Open Watchfire",
		OnClick: ClickAction{Kind: ClickOpenWatchfire},
	})
	out = append(out, MenuNode{
		Title:   "Open Dashboard…",
		OnClick: ClickAction{Kind: ClickOpenDashboard},
	})
	out = append(out, separator())

	// Notifications submenu — always rendered (so the user has a stable place
	// to find it), but the children list is empty when the count is zero.
	notifRoot := MenuNode{
		Title:   fmt.Sprintf("Notifications (%d today) ▸", in.NotificationsTodayCount),
		OnClick: ClickAction{Kind: ClickReloadNotifs},
	}

	// v6.0 Ember — surface the most recent weekly digest as the topmost item.
	if in.LatestDigest.Date != "" {
		notifRoot.Children = append(notifRoot.Children, MenuNode{
			Title:    fmt.Sprintf("📊 Weekly digest · %s", digestRelativeLabel(in.LatestDigest.EmittedAt, time.Now())),
			Subtitle: in.LatestDigest.Date,
			OnClick: ClickAction{
				Kind:       ClickFocusDigest,
				DigestDate: in.LatestDigest.Date,
			},
		})
	}

	for i, n := range in.Notifications {
		if i >= MaxNotifications {
			break
		}
		var click ClickAction
		switch n.Kind {
		case "TASK_FAILED":
			click = ClickAction{
				Kind:       ClickFocusTask,
				ProjectID:  n.ProjectID,
				TaskNumber: n.TaskNumber,
			}
		case "RUN_COMPLETE":
			click = ClickAction{
				Kind:      ClickFocusMain,
				ProjectID: n.ProjectID,
			}
		default:
			click = ClickAction{
				Kind:      ClickFocusMain,
				ProjectID: n.ProjectID,
			}
		}
		notifRoot.Children = append(notifRoot.Children, MenuNode{
			Title:    notificationRowTitle(n),
			Subtitle: n.AgeText,
			OnClick:  click,
		})
	}
	if len(notifRoot.Children) == 0 {
		notifRoot.Children = append(notifRoot.Children, MenuNode{
			Title:    "No notifications today",
			Disabled: true,
		})
	}
	out = append(out, notifRoot)
	out = append(out, separator())

	if in.UpdateAvailable {
		title := "Update Available"
		if in.UpdateVersion != "" {
			title = fmt.Sprintf("Update Available — v%s", in.UpdateVersion)
		}
		out = append(out, MenuNode{
			Title:   title,
			OnClick: ClickAction{Kind: ClickUpdateAvail},
		})
	}

	out = append(out, MenuNode{
		Title:   "Quit Watchfire",
		OnClick: ClickAction{Kind: ClickQuitWatchfire},
	})

	return out
}

func separator() MenuNode {
	return MenuNode{Title: "---", Disabled: true}
}

// bucketProjects splits the input into attention / working / idle in stable
// order (input order preserved within each bucket).
func bucketProjects(projects []ProjectMenuInfo) (attention, working, idle []ProjectMenuInfo) {
	for _, p := range projects {
		switch p.Status {
		case ProjectFailed:
			attention = append(attention, p)
		case ProjectWorking:
			working = append(working, p)
		default:
			idle = append(idle, p)
		}
	}
	return attention, working, idle
}

// SortProjects orders projects by name (case-insensitive), then ID, so the
// menu is deterministic regardless of the source's iteration order. The
// caller invokes this before passing the slice to BuildMenu when the
// underlying source (e.g. a map) has no inherent ordering.
func SortProjects(projects []ProjectMenuInfo) {
	sort.SliceStable(projects, func(i, j int) bool {
		ai := lower(projects[i].ProjectName)
		aj := lower(projects[j].ProjectName)
		if ai != aj {
			return ai < aj
		}
		return projects[i].ProjectID < projects[j].ProjectID
	})
}

func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

// digestRelativeLabel returns a short relative descriptor for the digest's
// emit time — "today", "yesterday", or "last <Mon>". Used as the tray's
// "📊 Weekly digest · last Mon" subtitle so the user can tell at a glance
// when the most recent digest dropped.
func digestRelativeLabel(emittedAt, now time.Time) string {
	emitted := emittedAt.Local()
	yEY, yEM, yED := emitted.Date()
	yY, yM, yD := now.Local().Date()

	emittedDay := time.Date(yEY, yEM, yED, 0, 0, 0, 0, emitted.Location())
	today := time.Date(yY, yM, yD, 0, 0, 0, 0, now.Location())

	switch days := int(today.Sub(emittedDay).Hours() / 24); {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 7:
		return "last " + emitted.Format("Mon")
	default:
		return emitted.Format("Mon, Jan 2")
	}
}

func notificationRowTitle(n NotificationLogEntry) string {
	prefix := n.ProjectName
	if prefix == "" {
		prefix = n.ProjectID
	}
	switch n.Kind {
	case "TASK_FAILED":
		return fmt.Sprintf("%s: task failed", prefix)
	case "RUN_COMPLETE":
		return fmt.Sprintf("%s: run complete", prefix)
	default:
		return fmt.Sprintf("%s: %s", prefix, n.Kind)
	}
}
