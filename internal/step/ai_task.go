package step

import (
	"context"
	"fmt"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/types"
)

// aiTask is the state that survives across the retries of one AI task: the
// per-task Worker clone (escalation may upgrade its model), the diagnosis fed
// to the next attempt, the per-category rejection counts the escalation policy
// keys off, and the task's cumulative cost.
type aiTask struct {
	worker         *Worker
	diagnosis      string
	categoryCounts map[string]int
	costUSD        float64
	maxRetries     int
}

// runAITask drives the worker/review retry loop for a normal (AI) task.
func (e *Executor) runAITask(ctx context.Context, r *Run, t *types.Task) error {
	maxRetries := e.cfg.Execution.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	st := &aiTask{
		// Per-task worker clone: escalation upgrades this clone's model and
		// sets its stream callback, so parallel tasks never race on shared
		// worker state.
		worker:         e.worker.clone(),
		categoryCounts: make(map[string]int),
		maxRetries:     maxRetries,
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Respect cancellation between attempts so a cancelled run aborts
		// promptly instead of burning the remaining retries.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		passed, err := e.attempt(ctx, r, t, st, attempt)
		if err != nil {
			return err
		}
		if passed {
			return nil
		}
	}

	if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
		charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
	}
	return fmt.Errorf("task %s failed review after %d attempts", t.ID, maxRetries+1)
}

// attempt runs one worker-then-review cycle. It reports passed=true when the
// task reached PASSED, returns an error when the caller must stop (cost
// ceiling, human escalation, cancellation, or retries exhausted), and
// (false, nil) when the loop should retry with the updated diagnosis.
func (e *Executor) attempt(ctx context.Context, r *Run, t *types.Task, st *aiTask, attempt int) (bool, error) {
	if attempt > 0 {
		e.emit(event.Event{Type: event.Retry, TaskID: t.ID, Attempt: attempt, Message: st.diagnosis})
		if _, err := e.recovery.Check(); err != nil {
			charmbraceletlog.Warn("recovery check on retry", "task", t.ID, "err", err)
		}
	}

	hookEnv := hooks.HookEnv{TaskID: t.ID, Project: e.cfg.Project.Name, Status: "running"}
	if _, err := e.hooks.Run(ctx, hooks.PreTask, hookEnv); err != nil {
		charmbraceletlog.Warn("pre-task hook", "task", t.ID, "err", err)
	}

	if err := e.book.SetStatus(r.TasksPath, t.ID, types.StatusRunning); err != nil {
		charmbraceletlog.Warn("updating task status to running", "task", t.ID, "err", err)
	}
	e.emit(event.Event{Type: event.TaskStart, TaskID: t.ID, Attempt: attempt, Message: t.Title})

	result, err := e.runWorker(ctx, r, t, st)
	var workerCost float64
	if result != nil {
		workerCost = result.CostUSD
	}
	if err != nil {
		if ceilErr := e.charge(r, t, st, workerCost); ceilErr != nil {
			return false, ceilErr
		}
		if attempt == st.maxRetries {
			if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
			}
			hookEnv.Status = "failed"
			e.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
			e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
			e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Status: types.StatusFailed})
			return false, fmt.Errorf("task %s failed after %d attempts: %w", t.ID, attempt+1, err)
		}
		st.diagnosis = err.Error()
		return false, nil
	}

	e.emit(event.Event{Type: event.ReviewStart, TaskID: t.ID})
	reviewResult, reviewErr := e.reviewer.Review(ctx, t)
	var reviewerCost float64
	if reviewErr == nil && reviewResult != nil {
		reviewerCost = reviewResult.CostUSD
	}
	attemptCost := workerCost + reviewerCost
	if ceilErr := e.charge(r, t, st, attemptCost); ceilErr != nil {
		return false, ceilErr
	}
	if reviewErr != nil {
		if attempt == st.maxRetries {
			if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
			}
			return false, fmt.Errorf("task %s review failed: %w", t.ID, reviewErr)
		}
		st.diagnosis = reviewErr.Error()
		return false, nil
	}

	e.emit(event.Event{
		Type:    event.ReviewResult,
		TaskID:  t.ID,
		Message: string(reviewResult.Verdict),
	})

	if reviewResult.Verdict == VerdictPass {
		return e.finishPassedTask(ctx, r, t, st, attempt, hookEnv, result, reviewResult, attemptCost)
	}

	if reviewResult.Verdict == VerdictIndeterminate {
		if attempt == st.maxRetries {
			if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
			}
			return false, fmt.Errorf("task %s reviewer never produced a verdict after %d attempts", t.ID, st.maxRetries+1)
		}
		st.diagnosis = "reviewer produced no parseable verdict on the previous attempt"
		return false, nil
	}

	return false, e.rejectAttempt(ctx, r, t, st, attempt, hookEnv, reviewResult)
}

// rejectAttempt records a FAIL verdict — hooks, completion event, escalation —
// and returns a fatal error only when the escalation policy hands the task to a
// human.
func (e *Executor) rejectAttempt(
	ctx context.Context,
	r *Run,
	t *types.Task,
	st *aiTask,
	attempt int,
	hookEnv hooks.HookEnv,
	reviewResult *ReviewResult,
) error {
	st.diagnosis = reviewResult.Summary
	hookEnv.Status = "failed"
	e.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
	e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
	e.emit(event.Event{
		Type:    event.TaskComplete,
		TaskID:  t.ID,
		Status:  types.StatusFailed,
		Message: st.diagnosis,
	})
	return e.applyEscalation(ctx, r, t, st, attempt, reviewResult)
}

// charge adds an attempt's cost to the per-task and run totals and reports a
// fatal error when either ceiling is breached.
func (e *Executor) charge(r *Run, t *types.Task, st *aiTask, cost float64) error {
	taskTotal, runTotal := e.book.AddCost(&st.costUSD, r.TotalCostUSD, cost)
	if ceiling := e.cfg.Execution.MaxCostPerTaskUSD; ceiling > 0 && taskTotal > ceiling {
		return Fatal(fmt.Errorf("task %s cost $%.2f exceeded per-task ceiling $%.2f (configure execution.max_cost_per_task_usd to raise)", t.ID, taskTotal, ceiling))
	}
	if ceiling := e.cfg.Execution.MaxCostUSD; ceiling > 0 && runTotal > ceiling {
		return Fatal(fmt.Errorf("run aborted: cumulative cost $%.2f exceeded ceiling $%.2f (configure execution.max_cost_usd to raise)", runTotal, ceiling))
	}
	return nil
}
