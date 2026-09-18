package step

import (
	"context"
	"fmt"
	"strings"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/stepout"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// stageEvidenceLabel is what a computational step's own output is called on a
// gate screen: the step's title if it has one, its id otherwise.
func stageEvidenceLabel(t *types.Task) string {
	if strings.TrimSpace(t.Title) != "" {
		return t.Title
	}
	return t.ID
}

// runComputationalStage executes a tool, test or repro step: it runs the shell
// command as a deterministic pipeline step. Exit 0 → PASSED; non-zero → FAILED
// (a task-level failure, so dependents are skipped per CH-07). No LLM, no
// reviewer, no TASK-REPORT, no cost. Concurrency-safe like the AI path.
//
// One node inverts that rule: the "before" half of a repro passes only when the
// command fails (see recipe.expandRepro). It is the only place in the system
// where a non-zero exit is success, which is exactly why it is a declared flag
// on the task rather than a special case somewhere in this function.
func (e *Executor) runComputationalStage(ctx context.Context, r *Run, t *types.Task, acc *evidenceSet) error {
	if strings.TrimSpace(t.Command) == "" {
		if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
			charmbraceletlog.Warn("updating command task status to failed", "task", t.ID, "err", statusErr)
		}
		e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: event.PhaseValidate, Status: types.StatusFailed, Message: "command stage has no command"})
		return fmt.Errorf("task %s: command stage has no command", t.ID)
	}

	if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusRunning); statusErr != nil {
		charmbraceletlog.Warn("updating command task status to running", "task", t.ID, "err", statusErr)
	}
	// A command stage is the deterministic third of the cost-by-nature bar:
	// tool, test, repro. No LLM, so the honest number is a measured $0 rather
	// than an absent one.
	e.emit(event.Event{Type: event.TaskStart, TaskID: t.ID, Phase: event.PhaseValidate})

	// Reuse the per-task wall-clock ceiling so a hung command can't stall the
	// run — the step's own when it declared one, which is how a suite that takes
	// longer than the run's default says so.
	cmdCtx := ctx
	if ceiling := e.hardCeiling(t.Timeout); ceiling > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, ceiling)
		defer cancel()
	}

	// Loop-with-policy: run the command up to maxIter times; the success
	// condition is LoopUntil (when set) or the command's own exit code. maxIter
	// is 1 for a plain command stage (no loop).
	maxIter := t.LoopMax
	if maxIter < 1 {
		maxIter = 1
	}
	// A policy gate's max_attempts caps this step the same way it caps an AI
	// task's retries — it is the recipe saying "try this at most N times",
	// which is how the reference flow's fix loop is written.
	if cap := policyFor(t).maxAttempts; cap > 0 && cap < maxIter {
		maxIter = cap
	}

	start := time.Now()
	var lastErr error
	// lastOut is the output of whatever produced lastErr — the command, or the
	// until-condition when the loop has one. The two are assigned in the same
	// pair of branches on purpose: a diagnostic that showed the command's output
	// while the condition was what failed would name the wrong culprit, and the
	// operator would go read a command that worked.
	var lastOut string
	for iter := 1; iter <= maxIter; iter++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		label := "$ " + t.Command
		if maxIter > 1 {
			label = fmt.Sprintf("[%d/%d] %s", iter, maxIter, label)
		}
		e.emit(event.Event{Type: event.TaskStream, TaskID: t.ID, Phase: event.PhaseValidate, Stream: &types.StreamEvent{Type: types.EventToolUse, Tool: "command", Content: label}})

		out, runErr := e.runShellForTask(cmdCtx, r, t, t.Command)
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			ev := types.StreamEvent{Type: types.EventToolResult, Content: trimmed}
			e.emit(event.Event{Type: event.TaskStream, TaskID: t.ID, Phase: event.PhaseValidate, Stream: &ev})
		}

		// Decide success for this iteration.
		ok := runErr == nil
		if strings.TrimSpace(t.LoopUntil) != "" {
			// The until-condition governs; the command is the work that may
			// make it pass over successive iterations.
			untilOut, untilErr := e.runShellForTask(cmdCtx, r, t, t.LoopUntil)
			ok = untilErr == nil
			lastErr, lastOut = untilErr, untilOut
		} else {
			lastErr, lastOut = runErr, out
		}
		if t.ExpectFail {
			ok = !ok
		}

		if ok {
			// A discovery step's output is the fan-out's work list. It is
			// recorded on the task rather than re-derived later: a resumed run
			// must expand the same set, and re-running `ls` next week would
			// quietly pick up whatever changed in between.
			if strings.TrimSpace(t.Produces) == "items" {
				t.Items = ParseItems(out)
			}
			acc.add(gate.FromCommandOutput(stageEvidenceLabel(t), out, true))
			if gateErr := e.runGates(ctx, r, t, types.GateAfter, acc); gateErr != nil {
				e.markGateFailure(r, t)
				return gateErr
			}
			summary := "ran command: " + t.Command
			if maxIter > 1 {
				summary += fmt.Sprintf(" (passed on iteration %d/%d)", iter, maxIter)
			}
			e.markStagePassed(r, t, summary, time.Since(start).Milliseconds(), event.PhaseValidate)
			return nil
		}
	}

	if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
		charmbraceletlog.Warn("updating command task status to failed", "task", t.ID, "err", statusErr)
	}
	cond := "command exit"
	if strings.TrimSpace(t.LoopUntil) != "" {
		cond = fmt.Sprintf("loop condition %q", t.LoopUntil)
	}
	tail := e.recordStageOutput(r, t, lastOut)
	if t.ExpectFail {
		// The honest message for the most valuable repro verdict: the bug is
		// not there, so there is nothing to fix. The output still travels: what
		// the command printed while exiting 0 is how the operator learns whether
		// the bug is really gone or the repro stopped exercising it.
		msg := fmt.Sprintf("the repro command did not reproduce (it exited 0); nothing to fix at %s", t.ID)
		e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: event.PhaseValidate, Status: types.StatusFailed, Message: msg, Output: tail})
		return fmt.Errorf("task %s: %s", t.ID, msg)
	}
	// Three places, on purpose, and the split is about what each one is allowed
	// to hold.
	//
	// Message stops at the count, because it is the field that lands in
	// activity.jsonl — committed by corvex's own auto_commit — and a failing
	// command's output is where paths and exported credentials live. Output
	// carries the command's own last words to the renderers and nowhere else:
	// activity.Entry has no field for it, so it cannot reach the commit. And the
	// returned error keeps the exit status for the run's own failure summary.
	//
	// Before this, the two published surfaces held the count and nothing else,
	// and the sentence the tool had already written — the one naming the missing
	// flag — was captured by runShell and then discarded. That is the bug: the
	// diagnosis existed and was thrown away.
	charmbraceletlog.Warn("command stage failed", "task", t.ID, "err", lastErr)
	published := fmt.Sprintf("%s did not pass after %d iteration(s)", cond, maxIter)
	e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: event.PhaseValidate, Status: types.StatusFailed, Message: published, Output: tail})
	return fmt.Errorf("task %s: %s: %v", t.ID, published, lastErr)
}

// recordStageOutput truncates a failed stage's own output once and puts it where
// the two surfaces a person uses can find it: the returned tail rides the failed
// task_complete event to the terminal, and the same bytes are stored under
// `.corvex/runs/output/` for `run show --step`.
//
// One truncation, not two, so the screen and the file can never disagree about
// what the command said.
//
// The store is skipped when the run has no identity: an unregistered run has no
// addressable place to put it, and a step must degrade to less information
// rather than to a refused step (the same rule runID and runBase follow). The
// event still carries the tail, so the terminal loses nothing.
func (e *Executor) recordStageOutput(r *Run, t *types.Task, out string) string {
	tail := stepout.Tail(out)
	if tail == "" {
		return ""
	}
	if r == nil || r.Identity.RunID == "" || r.Identity.Repo == "" {
		return tail
	}
	if err := stepout.Write(r.Identity.Repo, r.Identity.RunID, t.ID, tail); err != nil {
		charmbraceletlog.Warn("recording failed stage output", "task", t.ID, "err", err)
	}
	return tail
}

// markStagePassed records a non-AI stage (command, approved human-gate) as
// PASSED with the serialised git+state bookkeeping the AI path uses, then emits
// the checkpoint + completion events. Concurrency-safe.
//
// phase is a parameter because the two callers are two different natures of
// work — a deterministic command and a gate that was the whole step — and the
// cost-by-nature bar needs them in different buckets. Deriving it from the task
// here would mean this function re-deciding something its callers already know.
func (e *Executor) markStagePassed(r *Run, t *types.Task, summary string, durationMs int64, phase string) {
	e.book.Lock()
	if len(t.Items) > 0 {
		// A discovery step carries state a status-only write would drop.
		passed := *t
		passed.Status = types.StatusPassed
		if replaceErr := task.ReplaceTask(r.TasksPath, passed); replaceErr != nil {
			charmbraceletlog.Warn("persisting discovered items", "task", t.ID, "err", replaceErr)
		}
	} else if statusErr := task.UpdateTaskStatus(r.TasksPath, t.ID, types.StatusPassed); statusErr != nil {
		charmbraceletlog.Warn("updating stage status to passed", "task", t.ID, "err", statusErr)
	}
	nextCompleted := make(map[string]bool, len(r.Completed)+1)
	for k, v := range r.Completed {
		nextCompleted[k] = v
	}
	nextCompleted[t.ID] = true
	nextTask := ""
	if nr := r.DAG.NextReady(nextCompleted); len(nr) > 0 {
		nextTask = nr[0]
	}
	*r.Anchor = anchor.Update(*r.Anchor, anchor.TaskResult{
		Completed:  types.CompletedTask{ID: t.ID, Title: t.Title, Summary: summary},
		NextTask:   nextTask,
		TotalTasks: r.DAG.Size(),
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
	e.book.Unlock()

	e.emit(event.Event{Type: event.Checkpoint, TaskID: t.ID, Phase: phase})
	e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: phase, Status: types.StatusPassed, DurationMs: durationMs})
}
