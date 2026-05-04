package agent

// TaskDoneOutcome enumerates the post-task-completion paths the merge / PR
// flow can take. The v5.0 spec replaces the bare `bool` that previously
// signalled "continue chaining" with this richer return so the manager can
// distinguish a successful merge from a merge that silently halted the
// run-all queue (the old `false`) and surface the reason to the user.
type TaskDoneOutcome int

// Possible TaskDoneOutcome values.
const (
	// TaskDoneOK — task completed and the merge / PR flow finished cleanly.
	// Chaining proceeds. Equivalent to the pre-v5.0 `true` return.
	TaskDoneOK TaskDoneOutcome = iota

	// TaskDoneMergeFailed — the agent reported `success: true` on the task
	// but the silent auto-merge into the project's default branch failed
	// (dirty target, merge conflict, post-merge hook, …). Chain control
	// matches the old `false`: the run-all queue halts so the user can
	// resolve the conflict on `main` manually. The merge error string is
	// carried in TaskDoneResult.Reason.
	TaskDoneMergeFailed

	// TaskDoneCancelled — reserved for a future v5.x cancel-mid-merge
	// feature. No current code path produces it; defined so adding the
	// behaviour later does not require another callback signature change.
	TaskDoneCancelled
)

// TaskDoneResult is what the post-task-done callback returns to the agent
// manager. `Reason` is a free-text error message populated for
// `TaskDoneMergeFailed` / `TaskDoneCancelled`; empty for `TaskDoneOK`.
type TaskDoneResult struct {
	Outcome TaskDoneOutcome
	Reason  string
}

// ShouldContinueChain reports whether the run-all / wildfire queue should
// advance to the next task. Mirrors the pre-v5.0 bare-bool semantics:
// `TaskDoneOK` is the only outcome that allows chaining.
func (r TaskDoneResult) ShouldContinueChain() bool {
	return r.Outcome == TaskDoneOK
}
