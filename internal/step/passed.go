package step

import (
	"context"
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
	attemptCost float64,
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

	hookEnv.Status = "passed"
	e.runHook(ctx, hooks.OnSuccess, hookEnv, t.ID)
	e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)

	e.commitPassedTask(r, t, report, reviewResult, nextTask)

	e.emit(event.Event{Type: event.Checkpoint, TaskID: t.ID, Phase: event.PhaseWorker})
	// `worker` on a number that is workerCost+reviewerCost: the honest split
	// would move the reviewer's share onto the verdict line, and inspect/run
	// show read this line as the task's total. Recorded in phase.go, not fixed
	// here.
	e.emit(event.Event{
		Type:       event.TaskComplete,
		TaskID:     t.ID,
		Phase:      event.PhaseWorker,
		Status:     types.StatusPassed,
		CostUSD:    attemptCost,
		TokensIn:   result.TokensIn + reviewResult.TokensIn,
		TokensOut:  result.TokensOut + reviewResult.TokensOut,
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
		if err := e.recovery.MarkCheckpoint(t.ID); err != nil {
			charmbraceletlog.Warn("marking checkpoint", "task", t.ID, "err", err)
		}
	}
	r.Completed[t.ID] = true
}
