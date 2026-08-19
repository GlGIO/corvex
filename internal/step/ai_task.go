package step

import (
	"context"
	"fmt"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/types"
)

// aiTask is the state that survives across the retries of one AI task: the
// per-task Worker clone (escalation may upgrade its model), the diagnosis fed
// to the next attempt, the per-category rejection counts the escalation policy
// keys off, and the task's cumulative cost.
type aiTask struct {
	worker *Worker
	// diagnosis is the full text fed back into the next attempt's PROMPT. It
	// may carry anything the provider or a stack trace said, including absolute
	// paths and whatever the user exported — so it must never be published.
	diagnosis string
	// reason is the publishable half: what the ledger line says happened. The
	// two are separate fields rather than one string used twice because that is
	// exactly how the leak happened — one value with two readers, and only one
	// of them safe.
	reason         string
	categoryCounts map[string]int
	costUSD        float64
	maxRetries     int
}

// runAITaskGated drives the worker/review retry loop for a code task and, when
// it passes, hands the result to the after-gates before the pass is recorded.
//
// The order matters: an after-gate that ran once the task was already PASSED and
// checkpointed would have to flip a committed state back, which is a worse thing
// to own than an extra branch here.
func (e *Executor) runAITaskGated(ctx context.Context, r *Run, t *types.Task, acc *evidenceSet) error {
	if err := e.runAITask(ctx, r, t, acc); err != nil {
		return err
	}
	if err := e.runGates(ctx, r, t, types.GateAfter, acc); err != nil {
		e.markGateFailure(r, t)
		return err
	}
	return nil
}

// runAITask drives the worker/review retry loop for a normal (AI) task.
func (e *Executor) runAITask(ctx context.Context, r *Run, t *types.Task, acc *evidenceSet) error {
	maxRetries := e.cfg.Execution.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}
	// A policy gate's max_attempts is the recipe capping this step, and the
	// recipe is more specific than the global config — this is how "fix loop,
	// cap 2" stops being a number in a config file shared by every step.
	if cap := policyFor(t).maxAttempts; cap > 0 {
		maxRetries = cap - 1
		if maxRetries < 0 {
			maxRetries = 0
		}
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
		passed, err := e.attempt(ctx, r, t, st, attempt, acc)
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
func (e *Executor) attempt(ctx context.Context, r *Run, t *types.Task, st *aiTask, attempt int, acc *evidenceSet) (bool, error) {
	if attempt > 0 {
		// A retry line is the worker being sent back in, even though the
		// diagnosis on it was written by the reviewer: what the next attempt
		// costs is worker spend.
		e.emit(event.Event{Type: event.Retry, TaskID: t.ID, Phase: event.PhaseWorker, Attempt: attempt, Message: st.publishableReason()})
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
	e.emit(event.Event{Type: event.TaskStart, TaskID: t.ID, Phase: event.PhaseWorker, Attempt: attempt, Message: t.Title})

	result, err := e.runWorker(ctx, r, t, st)
	var workerCost float64
	if result != nil {
		workerCost = result.CostUSD
	}
	if err != nil {
		if ceilErr := e.charge(r, t, st, workerCost); ceilErr != nil {
			return false, ceilErr
		}
		// The attempt is charged, so it is also RECORDED. Until a dogfood run
		// spent 36 minutes over two attempts and reported `$0.00`, cost only
		// reached the ledger on the pass path — which made the accounting blind
		// in exactly the case somebody is trying to account for: the run that
		// burned money and produced nothing. The line is `retry`-shaped even on
		// the last attempt, because what it reports is one attempt's spend, not
		// the task's outcome.
		if workerCost > 0 {
			e.emitAttemptCost(st, t, attempt, result, "worker attempt not completed")
		}
		if attempt == st.maxRetries {
			if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
			}
			hookEnv.Status = "failed"
			e.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
			e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
			e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: event.PhaseWorker, Status: types.StatusFailed})
			return false, fmt.Errorf("task %s failed after %d attempts: %w", t.ID, attempt+1, err)
		}
		// Two readers, two needs: the next attempt's PROMPT wants the whole
		// error (that is what makes the retry informed), and the ledger LINE
		// must not have it — the provider's error text carries its raw stderr,
		// which carries paths and whatever the user exported. So the full text
		// goes to the prompt and to the log, and the published line says only
		// what happened.
		charmbraceletlog.Warn("worker attempt failed", "task", t.ID, "attempt", attempt, "err", err)
		st.diagnosis = err.Error()
		st.reason = "worker call failed"
		return false, nil
	}

	e.emit(event.Event{Type: event.ReviewStart, TaskID: t.ID, Phase: event.PhaseReview})
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
		charmbraceletlog.Warn("reviewer failed", "task", t.ID, "attempt", attempt, "err", reviewErr)
		st.diagnosis = reviewErr.Error()
		st.reason = "reviewer call failed"
		return false, nil
	}

	// The reviewer's own spend rides on this line, not on task_complete's: that
	// is what lets the phase breakdown attribute review money to the review
	// bucket instead of folding it into the worker's roll-up.
	e.emit(event.Event{
		Type:      event.ReviewResult,
		TaskID:    t.ID,
		Phase:     event.PhaseReview,
		Message:   string(reviewResult.Verdict),
		CostUSD:   reviewerCost,
		TokensIn:  reviewResult.TokensIn,
		TokensOut: reviewResult.TokensOut,
	})
	// The reviewer's verdict is evidence whether it passed or not: a gate
	// downstream of a code step is exactly where somebody needs to read why the
	// judge said yes.
	acc.add(gate.FromVerdict("Review de "+stageEvidenceLabel(t), string(reviewResult.Verdict),
		reviewResult.Category, reviewResult.Summary, reviewResult.Verdict == VerdictPass))

	if reviewResult.Verdict == VerdictPass {
		e.addDiffEvidence(ctx, r, t, acc)
		return e.finishPassedTask(ctx, r, t, st, attempt, hookEnv, result, reviewResult, workerCost)
	}

	if reviewResult.Verdict != VerdictPass && attempt < st.maxRetries {
		// A rejected attempt is money spent on work that will be redone. It was
		// charged against the ceiling all along; now it is visible.
		e.emitAttemptCost(st, t, attempt, result, "worker attempt rejected by review")
	}

	if reviewResult.Verdict == VerdictIndeterminate {
		if attempt == st.maxRetries {
			if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
			}
			return false, fmt.Errorf("task %s reviewer never produced a verdict after %d attempts", t.ID, st.maxRetries+1)
		}
		st.diagnosis = "reviewer produced no parseable verdict on the previous attempt"
		st.reason = st.diagnosis
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
	// A reviewer verdict is prose about the user's own code, written by the
	// model — the closest thing to publishable text this loop produces. It is
	// still passed through the ledger's path redaction on the way in.
	st.reason = reviewResult.Summary
	hookEnv.Status = "failed"
	e.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
	e.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
	// The step's terminal line belongs to the work that was attempted, not to
	// the judge that rejected it: the judgement already has its own line.
	e.emit(event.Event{
		Type:    event.TaskComplete,
		TaskID:  t.ID,
		Phase:   event.PhaseWorker,
		Status:  types.StatusFailed,
		Message: st.publishableReason(),
	})
	return e.applyEscalation(ctx, r, t, st, attempt, reviewResult)
}

// emitAttemptCost records what one attempt spent, on the paths where the
// attempt does not end in a task_complete. It is a method rather than two
// inline literals so the two producers cannot drift into reporting different
// things about the same event.
//
// A nil result means the provider returned nothing at all — there is no number
// to report, and inventing a zero would be worse than the silence it replaces.
func (e *Executor) emitAttemptCost(_ *aiTask, t *types.Task, attempt int, result *types.ExecuteResult, why string) {
	if result == nil {
		return
	}
	e.emit(event.Event{
		Type:      event.AttemptCost,
		TaskID:    t.ID,
		Phase:     event.PhaseWorker,
		Attempt:   attempt,
		CostUSD:   result.CostUSD,
		TokensIn:  result.TokensIn,
		TokensOut: result.TokensOut,
		Message:   why,
	})
}

// publishableReason is what a ledger line may say about a failure. Empty falls
// back to a fixed string rather than to the diagnosis: a missing reason must
// degrade to less information, never to more.
func (st *aiTask) publishableReason() string {
	if st.reason != "" {
		return st.reason
	}
	return "attempt failed"
}

// charge adds an attempt's cost to the per-task and run totals and reports a
// fatal error when either ceiling is breached.
func (e *Executor) charge(r *Run, t *types.Task, st *aiTask, cost float64) error {
	taskTotal, runTotal := e.book.AddCost(&st.costUSD, r.TotalCostUSD, cost)
	// A policy gate's max_cost_usd replaces the global per-task default rather
	// than stacking with it: the recipe knows which step is expensive, the
	// config file only knows an average, and two ceilings where the looser one
	// is declared locally would make the local declaration a lie.
	if ceiling := policyFor(t).maxCostUSD; ceiling > 0 {
		if taskTotal > ceiling {
			return Fatal(fmt.Errorf("task %s cost $%.2f exceeded its policy gate ceiling $%.2f", t.ID, taskTotal, ceiling))
		}
	} else if ceiling := e.cfg.Execution.MaxCostPerTaskUSD; ceiling > 0 && taskTotal > ceiling {
		return Fatal(fmt.Errorf("task %s cost $%.2f exceeded per-task ceiling $%.2f (configure execution.max_cost_per_task_usd to raise)", t.ID, taskTotal, ceiling))
	}
	if ceiling := e.cfg.Execution.MaxCostUSD; ceiling > 0 && runTotal > ceiling {
		return Fatal(fmt.Errorf("run aborted: cumulative cost $%.2f exceeded ceiling $%.2f (configure execution.max_cost_usd to raise)", runTotal, ceiling))
	}
	return nil
}
