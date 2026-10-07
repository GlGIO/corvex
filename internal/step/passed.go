package step

import (
	"context"
	"errors"
	"fmt"
	"strings"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// finishPassedTask handles a PASS verdict: it enforces the TASK-REPORT/HANDOFF
// contract, then runs the success hooks, the checkpoint bookkeeping and the
// completion events. It reports passed=false when the missing report sends the
// task back for another attempt.
func (e *Executor) finishPassedTask(
	ctx context.Context,
	r *Run,
	t *types.Task,
	st *aiTask,
	attempt int,
	hookEnv hooks.HookEnv,
	result *types.ExecuteResult,
	reviewResult *ReviewResult,
	workerCost float64,
	acc *evidenceSet,
) (bool, error) {
	// Determine the next task first — it decides whether a HANDOFF is required
	// (the last task in the DAG has nothing to hand off to).
	e.book.Lock()
	nextCompleted := make(map[string]bool, len(r.Completed)+1)
	for k, v := range r.Completed {
		nextCompleted[k] = v
	}
	e.book.Unlock()
	nextCompleted[t.ID] = true

	nextReady := r.DAG.NextReady(nextCompleted)
	nextTask := ""
	if len(nextReady) > 0 {
		nextTask = nextReady[0]
	}

	// The Worker must hand off structured context to the next task. A passing
	// implementation with no TASK-REPORT (or an empty HANDOFF when a next task
	// exists) is rejected and retried — a silent empty anchor entry is worse
	// than a retry, because every downstream task then runs blind. The final
	// task may omit HANDOFF.
	report, hasReport := parseTaskReport(result.Output)
	missingHandoff := nextTask != "" && strings.TrimSpace(report.Handoff) == ""
	if !hasReport || missingHandoff {
		st.diagnosis = "your previous response passed review but was missing the required TASK-REPORT block (with a non-empty HANDOFF). Re-do the task and end your response with the TASK-REPORT block: SUMMARY, DECISIONS, HANDOFF."
		if attempt == st.maxRetries {
			if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
			}
			e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: event.PhaseWorker, Status: types.StatusFailed, Message: "passed review but never produced a TASK-REPORT/HANDOFF"})
			return false, fmt.Errorf("task %s passed review but never produced a TASK-REPORT with a HANDOFF after %d attempts", t.ID, st.maxRetries+1)
		}
		e.emit(event.Event{Type: event.Retry, TaskID: t.ID, Phase: event.PhaseWorker, Attempt: attempt + 1, Message: "missing TASK-REPORT/HANDOFF"})
		return false, nil
	}

	// The after-gates judge the result while it is still a diff: before the
	// success hooks, the PASSED status and the checkpoint. Run after them, every
	// after-gate looked at a tree whose `git diff` was empty — the reviewer's own
	// prompt says to check it — and a refusal had to flip a step that was already
	// committed and announced as passed.
	mark := acc.mark()
	if err := e.runGates(ctx, r, t, types.GateAfter, acc); err != nil {
		// The worker's spend never reaches task_complete on this path, so it
		// gets its own line — refused or repaired, the attempt was paid.
		e.emitAttemptCost(st, t, attempt, result, "worker attempt refused by an after-gate")
		if e.repairAfterGate(t, st, attempt, err) {
			acc.rewind(mark)
			return false, nil
		}
		e.markGateFailure(r, t, err)
		hookEnv.Status = "failed"
		e.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
		e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
		return false, err
	}

	hookEnv.Status = "passed"
	e.runHook(ctx, hooks.OnSuccess, hookEnv, t.ID)
	e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)

	e.commitPassedTask(r, t, report, reviewResult, nextTask)

	e.emit(event.Event{Type: event.Checkpoint, TaskID: t.ID, Phase: event.PhaseWorker})
	// CostUSD here is the worker's own share only — the reviewer's share rode
	// on the review_result line already emitted for this attempt. Tokens and
	// duration stay combined: the ceiling and this line are the only readers
	// of cost, everything else is descriptive.
	e.emit(event.Event{
		Type:    event.TaskComplete,
		TaskID:  t.ID,
		Phase:   event.PhaseWorker,
		Status:  types.StatusPassed,
		CostUSD: workerCost,
		Model:   st.workerModel(),
		// Tokens follow the cost: this line is the worker's share, and the
		// reviewer's rides on review_result. Splitting one and not the other
		// would leave a line whose cost and tokens describe different work.
		TokensIn:   result.TokensIn,
		TokensOut:  result.TokensOut,
		DurationMs: result.DurationMs + reviewResult.DurationMs,
	})
	return true, nil
}

// commitPassedTask serialises the git + state writes as one unit. Under
// parallel execution, concurrent checkpoints (git add/commit) or interleaved
// tasks.md/anchor.yaml writes would corrupt the repo, so the whole bookkeeping
// block runs under the lock with raw (non-locking) ops. Writing state files
// BEFORE the commit captures them in the checkpoint — the previous commit-first
// order left them dirty and the next run's recovery reverted completed work.
func (e *Executor) commitPassedTask(
	r *Run,
	t *types.Task,
	report taskReport,
	reviewResult *ReviewResult,
	nextTask string,
) {
	e.book.Lock()
	defer e.book.Unlock()

	if statusErr := task.UpdateTaskStatus(r.TasksPath, t.ID, types.StatusPassed); statusErr != nil {
		charmbraceletlog.Warn("updating task status to passed", "task", t.ID, "err", statusErr)
	}
	// Capture real changed files while the working tree still diffs against
	// HEAD. Fall back to the planned lists on git error.
	realCreated, realModified, changedErr := e.recovery.ChangedFiles()
	if changedErr != nil {
		charmbraceletlog.Warn("capturing changed files", "task", t.ID, "err", changedErr)
		realCreated = t.Files.Create
		realModified = t.Files.Modify
	}
	// Prefer the Worker's own summary; fall back to the reviewer's.
	summary := strings.TrimSpace(report.Summary)
	if summary == "" {
		summary = reviewResult.Summary
	}
	*r.Anchor = anchor.Update(*r.Anchor, anchor.TaskResult{
		Completed: types.CompletedTask{
			ID:            t.ID,
			Title:         t.Title,
			Summary:       summary,
			FilesCreated:  realCreated,
			FilesModified: realModified,
			Decisions:     report.Decisions,
		},
		NextTask:        nextTask,
		NextTaskContext: report.Handoff,
		TotalTasks:      r.DAG.Size(),
	})
	if err := anchor.Save(r.AnchorPath, *r.Anchor); err != nil {
		charmbraceletlog.Warn("saving anchor", "task", t.ID, "err", err)
	}
	if e.cfg.Execution.AutoCommit {
		if err := e.checkpointer(t).MarkCheckpoint(t.ID); err != nil {
			charmbraceletlog.Warn("marking checkpoint", "task", t.ID, "err", err)
		}
	}
	r.Completed[t.ID] = true
}

// repairAfterGate decides whether a refused after-gate sends the worker back
// in instead of failing the step. Only a computational gate qualifies — its
// refusal is a command's output, which is a diagnosis; a person's rejection is
// a decision, and a judge's has the review loop already. Once per task, and
// only while attempts remain: the point is to turn "the lint failed" into one
// informed retry, not to open a second loop next to the review one.
func (e *Executor) repairAfterGate(t *types.Task, st *aiTask, attempt int, err error) bool {
	var gr *gateRefusal
	if !errors.As(err, &gr) || gr.nature != types.GateComputational {
		return false
	}
	if st.gateRepairs >= 1 || attempt >= st.maxRetries {
		return false
	}
	// A repair re-runs every after-gate, and a gate that parks on a person
	// cannot be run twice: its file exists, decided, and reopening it would
	// either kill the run or ask the person to approve code they never saw.
	for _, g := range gatesAt(t, types.GateAfter) {
		if g.Nature == types.GateHuman || g.Nature == types.GateQuestion {
			return false
		}
	}
	st.gateRepairs++
	st.diagnosis = "your change passed review, but an after-gate check refused it: " + gr.msg +
		"\n\nThe check printed (tail):\n" + tail(strings.TrimSpace(gr.output), repairOutputMax) +
		"\n\nYour change is still in the working tree. Fix what the check reports, keeping the rest of the change."
	st.keepTree = true
	// The retry line the next attempt opens with carries this reason; the
	// refusal itself is already on the ledger as gate_failed.
	st.reason = "after-gate refused; worker sent back once"
	return true
}

// repairOutputMax bounds how much of a check's output reaches the worker's
// prompt. The END is kept: that is where a test runner or a linter prints what
// failed, and a check that prints megabytes would otherwise spend the repair
// on a context overflow.
const repairOutputMax = 8 << 10

func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max:]
}
