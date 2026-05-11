package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	pb "github.com/watchfire-io/watchfire/proto"
)

// fixed sample for swap-logic tests: 1 draft + 3 ready, all active.
func reorderSampleTasks() []*pb.Task {
	return []*pb.Task{
		{TaskNumber: 1, Title: "Draft one", Status: "draft", Position: 1},
		{TaskNumber: 2, Title: "Ready alpha", Status: "ready", Position: 2},
		{TaskNumber: 3, Title: "Ready beta", Status: "ready", Position: 3},
		{TaskNumber: 4, Title: "Ready gamma", Status: "ready", Position: 4},
	}
}

func TestReorderActiveTasksSwapDownWithinSection(t *testing.T) {
	active := activeTasksInDisplayOrder(reorderSampleTasks())
	// active = [#1 draft, #2 ready, #3 ready, #4 ready]
	out, ok := reorderActiveTasks(active, 1, +1)
	if !ok {
		t.Fatalf("expected swap to succeed within Ready section")
	}
	got := taskNumbersOf(out)
	want := []int32{1, 3, 2, 4}
	if !int32SliceEqual(got, want) {
		t.Fatalf("swap result = %v, want %v", got, want)
	}
}

func TestReorderActiveTasksSwapUpWithinSection(t *testing.T) {
	active := activeTasksInDisplayOrder(reorderSampleTasks())
	// active = [#1 draft, #2 ready, #3 ready, #4 ready]; move #4 up.
	out, ok := reorderActiveTasks(active, 3, -1)
	if !ok {
		t.Fatalf("expected swap to succeed within Ready section")
	}
	got := taskNumbersOf(out)
	want := []int32{1, 2, 4, 3}
	if !int32SliceEqual(got, want) {
		t.Fatalf("swap result = %v, want %v", got, want)
	}
}

func TestReorderActiveTasksBoundaryTopIsNoOp(t *testing.T) {
	active := activeTasksInDisplayOrder(reorderSampleTasks())
	out, ok := reorderActiveTasks(active, 0, -1)
	if ok || out != nil {
		t.Fatalf("expected top-boundary swap to return (nil,false); got (%v, %v)", out, ok)
	}
}

func TestReorderActiveTasksBoundaryBottomIsNoOp(t *testing.T) {
	active := activeTasksInDisplayOrder(reorderSampleTasks())
	out, ok := reorderActiveTasks(active, len(active)-1, +1)
	if ok || out != nil {
		t.Fatalf("expected bottom-boundary swap to return (nil,false); got (%v, %v)", out, ok)
	}
}

func TestReorderActiveTasksCrossSectionIsNoOp(t *testing.T) {
	// #1 is the only Draft, #2 is the first Ready. Up from #2 (idx=1)
	// would cross the Draft/Ready boundary — must be a silent no-op.
	active := activeTasksInDisplayOrder(reorderSampleTasks())
	out, ok := reorderActiveTasks(active, 1, -1)
	if ok || out != nil {
		t.Fatalf("expected cross-section swap to return (nil,false); got (%v, %v)", out, ok)
	}
}

func TestActiveTasksInDisplayOrderGroupsByStatus(t *testing.T) {
	tasks := []*pb.Task{
		{TaskNumber: 10, Status: "done"},
		{TaskNumber: 20, Status: "draft"},
		{TaskNumber: 30, Status: "ready"},
		{TaskNumber: 40, Status: "draft"},
		{TaskNumber: 50, Status: "ready"},
	}
	got := taskNumbersOf(activeTasksInDisplayOrder(tasks))
	// Draft first (preserves slice order), then Ready, then Done.
	want := []int32{20, 40, 30, 50, 10}
	if !int32SliceEqual(got, want) {
		t.Fatalf("display order = %v, want %v", got, want)
	}
}

func TestActiveTasksInDisplayOrderDropsDeleted(t *testing.T) {
	mix := trashTaskMix()
	got := taskNumbersOf(activeTasksInDisplayOrder(mix))
	for _, n := range got {
		if n == 3 || n == 4 {
			t.Fatalf("deleted task #%d should not appear in active list", n)
		}
	}
}

func TestMergeActiveWithDeletedAppendsDeletedTail(t *testing.T) {
	mix := trashTaskMix()
	active := activeTasksInDisplayOrder(mix)
	merged := mergeActiveWithDeleted(active, mix)
	if len(merged) != len(mix) {
		t.Fatalf("merged length = %d, want %d", len(merged), len(mix))
	}
	// Last two entries must be the deleted ones (#3 and #4 in any order).
	deletedHead := merged[len(active):]
	for _, task := range deletedHead {
		if task.GetDeletedAt() == nil {
			t.Fatalf("expected trailing deleted tasks, got active #%d", task.TaskNumber)
		}
	}
}

// TestMoveTaskNoopOnHeaderSelection covers the "selected row is a section
// header" branch — SelectedTask() returns nil for that case, and the
// dispatch must produce no RPC.
func TestMoveTaskNoopOnHeaderSelection(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	m.taskList.SetTasks(m.tasks)
	// Force cursor onto the header by zeroing it; rebuild puts the
	// Draft header at index 0, but skipHeaders advances past it. To
	// simulate the no-op directly, we test the action path on a nil
	// selection.
	m.taskList.cursor = 0
	// Override the cursor onto the header row explicitly.
	for i, item := range m.taskList.activeItems() {
		if item.isHeader {
			m.taskList.cursor = i
			break
		}
	}
	if got := m.moveTaskUp(); got != nil {
		t.Fatalf("expected nil cmd on header row; got %T", got)
	}
	if got := m.moveTaskDown(); got != nil {
		t.Fatalf("expected nil cmd on header row; got %T", got)
	}
}

// TestMoveTaskNoopWithoutDaemonConn guards the "RPC requires conn" path
// — Shift+↑/↓ on a valid row must not panic when offline.
func TestMoveTaskNoopWithoutDaemonConn(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	m.taskList.SetTasks(m.tasks)
	// Land cursor on #2 (first Ready) — a known-active row.
	m.taskList.SelectTaskByNumber(2)
	if got := m.moveTaskDown(); got != nil {
		t.Fatalf("expected nil cmd without daemon connection; got %T", got)
	}
}

// TestReorderFailedRevertsLocalOrdering simulates a Shift+↓ that fired
// the optimistic local swap but had its RPC fail. The msg handler must
// restore m.tasks to the pre-swap snapshot, clear the in-flight flag,
// and surface a toast in m.err.
func TestReorderFailedRevertsLocalOrdering(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	m.taskList.SetTasks(m.tasks)
	// Capture the pre-swap snapshot and apply an optimistic order: swap
	// #2 and #3 in m.tasks.
	pre := append([]*pb.Task(nil), m.tasks...)
	m.preReorderTasks = pre
	m.inFlightReorder = true
	m.tasks[1], m.tasks[2] = m.tasks[2], m.tasks[1]
	m.taskList.SetTasks(m.tasks)

	handled, _ := m.handleMessage(ReorderFailedMsg{
		Err:     errors.New("rpc canceled"),
		Focused: 2,
	})
	if !handled {
		t.Fatalf("handler did not match ReorderFailedMsg")
	}
	if m.inFlightReorder {
		t.Fatalf("inFlightReorder should be cleared after failure")
	}
	if m.preReorderTasks != nil {
		t.Fatalf("preReorderTasks should be cleared after revert")
	}
	got := taskNumbersOf(m.tasks)
	want := []int32{1, 2, 3, 4}
	if !int32SliceEqual(got, want) {
		t.Fatalf("post-revert order = %v, want %v", got, want)
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "Reorder failed") {
		t.Fatalf("expected reorder-failed toast on m.err; got %v", m.err)
	}
}

// TestReorderCompletedAcceptsServerOrdering confirms that the success
// path replaces m.tasks with the server response and re-selects the
// moved row.
func TestReorderCompletedAcceptsServerOrdering(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	m.taskList.SetTasks(m.tasks)
	m.inFlightReorder = true
	m.preReorderTasks = append([]*pb.Task(nil), m.tasks...)

	serverResp := []*pb.Task{
		{TaskNumber: 1, Status: "draft", Position: 1},
		{TaskNumber: 3, Status: "ready", Position: 2},
		{TaskNumber: 2, Status: "ready", Position: 3},
		{TaskNumber: 4, Status: "ready", Position: 4},
	}
	handled, _ := m.handleMessage(ReorderCompletedMsg{
		Tasks:   serverResp,
		Focused: 3,
	})
	if !handled {
		t.Fatalf("handler did not match ReorderCompletedMsg")
	}
	if m.inFlightReorder || m.preReorderTasks != nil {
		t.Fatalf("in-flight state should be cleared after completion")
	}
	got := taskNumbersOf(m.tasks)
	want := []int32{1, 3, 2, 4}
	if !int32SliceEqual(got, want) {
		t.Fatalf("post-complete order = %v, want %v", got, want)
	}
	sel := m.taskList.SelectedTask()
	if sel == nil || sel.TaskNumber != 3 {
		t.Fatalf("expected highlight to follow #3, got %+v", sel)
	}
}

// TestTasksLoadedRaceDropsStaleRefreshDuringReorder covers the race fix:
// a TasksLoadedMsg arriving while inFlightReorder is true and whose
// active task-number set matches the local set must be dropped in
// favour of the optimistic ordering.
func TestTasksLoadedRaceDropsStaleRefreshDuringReorder(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	// Optimistic local swap of #2 and #3 is already in place.
	m.tasks[1], m.tasks[2] = m.tasks[2], m.tasks[1]
	m.taskList.SetTasks(m.tasks)
	m.inFlightReorder = true

	// Server delivers the stale (pre-swap) order via a watcher poll.
	stale := reorderSampleTasks()
	m.handleMessage(TasksLoadedMsg{Tasks: stale})

	got := taskNumbersOf(m.tasks)
	want := []int32{1, 3, 2, 4}
	if !int32SliceEqual(got, want) {
		t.Fatalf("stale refresh should be dropped; got %v, want %v", got, want)
	}
}

func TestTasksLoadedAcceptsRefreshWhenSetChanged(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	m.taskList.SetTasks(m.tasks)
	m.inFlightReorder = true

	// Server delivers a different active set (task #99 was created).
	updated := append(reorderSampleTasks(),
		&pb.Task{TaskNumber: 99, Status: "ready", Position: 5})
	m.handleMessage(TasksLoadedMsg{Tasks: updated})

	if got := taskNumbersOf(m.tasks); !containsTaskNumber(got, 99) {
		t.Fatalf("refresh with new task should win over optimistic order; got %v", got)
	}
}

// TestMoveUpKeyDispatchOnFocusedActiveTask wires the full key handler
// for shift+up on a valid row. Without a daemon conn the action returns
// nil, but the dispatch must hit moveTaskUp and not fall through to the
// generic Up navigation handler — assert by checking that the cursor
// did not move.
func TestMoveUpKeyDispatchDoesNotNavigate(t *testing.T) {
	m := &Model{
		taskList: NewTaskList(),
		tasks:    reorderSampleTasks(),
	}
	m.taskList.SetTasks(m.tasks)
	// Land cursor on the first Ready task (#2).
	m.taskList.SelectTaskByNumber(2)
	before := m.taskList.cursor
	cmd := m.handleTaskListKey(shiftArrowKey("shift+up"))
	if cmd != nil {
		t.Fatalf("expected nil cmd without daemon connection; got %T", cmd)
	}
	if m.taskList.cursor != before {
		t.Fatalf("Shift+↑ must not also trigger navigation; cursor moved from %d to %d",
			before, m.taskList.cursor)
	}
}

// shiftArrowKey synthesises a tea.KeyMsg whose String() matches the
// shift+arrow chord, the same shape Bubble Tea hands to the handler.
func shiftArrowKey(name string) tea.KeyMsg {
	switch name {
	case "shift+up":
		return tea.KeyMsg{Type: tea.KeyShiftUp}
	case "shift+down":
		return tea.KeyMsg{Type: tea.KeyShiftDown}
	}
	return tea.KeyMsg{}
}

// taskNumbersOf extracts the task numbers from a slice.
func taskNumbersOf(tasks []*pb.Task) []int32 {
	out := make([]int32, len(tasks))
	for i, t := range tasks {
		out[i] = t.TaskNumber
	}
	return out
}

func int32SliceEqual(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsTaskNumber(xs []int32, x int32) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
