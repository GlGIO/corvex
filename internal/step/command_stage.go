package step

import (
	"context"
	"fmt"
	"strings"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// runCommandStage executes a recipe "command" stage: it runs the stage's shell
// command as a deterministic pipeline step. Exit 0 → PASSED; non-zero → FAILED
// (a task-level failure, so dependents are skipped per CH-07). No LLM, no
// reviewer, no TASK-REPORT, no cost. Concurrency-safe like the AI path.
func (e *Executor) runCommandStage(ctx context.Context, r *Run, t *types.Task) error {
	if strings.TrimSpace(t.Command) == "" {
		if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); statusErr != nil {
			charmbraceletlog.Warn("updating command task status to failed", "task", t.ID, "err", statusErr)
		}
		e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Status: types.StatusFailed, Message: "command stage has no command"})
		return fmt.Errorf("task %s: command stage has no command", t.ID)
	}

	if statusErr := e.book.SetStatus(r.TasksPath, t.ID, types.StatusRunning); statusErr != nil {
		charmbraceletlog.Warn("updating command task status to running", "task", t.ID, "err", statusErr)
	}
	e.emit(event.Event{Type: event.TaskStart, TaskID: t.ID})

	// Reuse the per-task wall-clock ceiling so a hung command can't stall the run.
	cmdCtx := ctx
	if mins := e.cfg.Execution.TaskTimeoutMinutes; mins > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(mins)*time.Minute)
		defer cancel()
	}

	// Loop-with-policy: run the command up to maxIter times; the success
	// condition is LoopUntil (when set) or the command's own exit code. maxIter
	// is 1 for a plain command stage (no loop).
	maxIter := t.LoopMax
	if maxIter < 1 {
		maxIter = 1
	}

	start := time.Now()
	var lastErr error
	for iter := 1; iter <= maxIter; iter++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		label := "$ " + t.Command
		if maxIter > 1 {
			label = fmt.Sprintf("[%d/%d] %s", iter, maxIter, label)
		}
		e.emit(event.Event{Type: event.TaskStream, TaskID: t.ID, Stream: &types.StreamEvent{Type: types.EventToolUse, Tool: "command", Content: label}})

		out, runErr := e.runShell(cmdCtx, t.Command)
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			ev := types.StreamEvent{Type: types.EventToolResult, Content: trimmed}
			e.emit(event.Event{Type: event.TaskStream, TaskID: t.ID, Stream: &ev})
		}

		// Decide success for this iteration.
		ok := runErr == nil
		if strings.TrimSpace(t.LoopUntil) != "" {
			// The until-condition governs; the command is the work that may
			// make it pass over successive iterations.
			_, untilErr := e.runShell(cmdCtx, t.LoopUntil)
			ok = untilErr == nil
			lastErr = untilErr
		} else {
			lastErr = runErr
		}

		if ok {
			summary := "ran command: " + t.Command
			if maxIter > 1 {
				summary += fmt.Sprintf(" (passed on iteration %d/%d)", iter, maxIter)
			}
			e.markStagePassed(r, t, summary, time.Since(start).Milliseconds())
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
	msg := fmt.Sprintf("%s did not pass after %d iteration(s): %v", cond, maxIter, lastErr)
	e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Status: types.StatusFailed, Message: msg})
	return fmt.Errorf("task %s: %s", t.ID, msg)
}

// markStagePassed records a non-AI stage (command, approved human-gate) as
// PASSED with the serialised git+state bookkeeping the AI path uses, then emits
// the checkpoint + completion events. Concurrency-safe.
func (e *Executor) markStagePassed(r *Run, t *types.Task, summary string, durationMs int64) {
	e.book.Lock()
	if statusErr := task.UpdateTaskStatus(r.TasksPath, t.ID, types.StatusPassed); statusErr != nil {
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
		if err := e.recovery.MarkCheckpoint(t.ID); err != nil {
			charmbraceletlog.Warn("marking checkpoint", "task", t.ID, "err", err)
		}
	}
	r.Completed[t.ID] = true
	e.book.Unlock()

	e.emit(event.Event{Type: event.Checkpoint, TaskID: t.ID})
	e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Status: types.StatusPassed, DurationMs: durationMs})
}
