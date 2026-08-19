package orchestrator

import (
	"context"
	"fmt"
	"sync"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/step"
	"github.com/giovannialves/corvex/internal/types"
)

// schedule is the mutable state of one walk through the DAG.
type schedule struct {
	// run is the state each step reads and updates (paths, anchor, completed
	// set, cumulative cost). The same value is handed to every step.
	run   *step.Run
	tasks []types.Task
	// terminal holds every task that must never be re-scheduled: PASSED ones
	// satisfy dependencies (they live in run.Completed), while FAILED and
	// SKIPPED ones do not — but all three are done. Without tracking
	// FAILED/SKIPPED separately, NextReady (which only consults Completed)
	// would hand a failed task back every iteration and spin forever.
	terminal map[string]bool
	failed   []string
	skipped  []string
	failures []string
	// totalCostUSD backs run.TotalCostUSD.
	totalCostUSD float64
	// generatedBy is the tasks.md frontmatter marker, preserved so a fan-out
	// rewrite does not erase which recipe produced the file.
	generatedBy string
}

func newSchedule(tasks []types.Task, completed map[string]bool, d *dag.DAG, tasksPath, anchorPath string, anchorState *types.AnchorState, ident step.RunIdentity) *schedule {
	s := &schedule{
		tasks:    tasks,
		terminal: make(map[string]bool, len(completed)),
	}
	for id := range completed {
		s.terminal[id] = true
	}
	s.run = &step.Run{
		TasksPath:    tasksPath,
		AnchorPath:   anchorPath,
		Anchor:       anchorState,
		Completed:    completed,
		DAG:          d,
		TotalCostUSD: &s.totalCostUSD,
		Identity:     ident,
	}
	return s
}

// walkDAG runs one wave of ready tasks at a time until nothing runnable is
// left, or a fatal error / targeted run ends the walk early.
func (o *Orchestrator) walkDAG(ctx context.Context, s *schedule) error {
	for {
		// Between waves, and only between waves: this is the one point in the
		// loop where no worker goroutine is alive, so the task list and the DAG
		// can be swapped without racing every step that reads them.
		//
		// The cross-process pause is read in the same window and for a second
		// reason on top of that one: pausing inside a step would kill a provider
		// call already paid for (see waitWhilePausedOnDisk). It comes first so a
		// paused run does not expand a fan-out it may never walk.
		if err := o.waitWhilePausedOnDisk(ctx); err != nil {
			return err
		}
		if err := o.expandFanouts(s); err != nil {
			return err
		}
		ready, err := o.nextWave(s)
		if err != nil {
			return err
		}
		if len(ready) == 0 {
			return nil
		}

		if o.runsWaveInParallel() {
			if err := o.runWaveParallel(ctx, s, ready); err != nil {
				return err
			}
			continue
		}

		if err := o.runWaveSerial(ctx, s, ready); err != nil {
			return err
		}
		if o.targetTask != "" || o.singleTask {
			return nil
		}
	}
}

// nextWave returns the next DAG level to execute: tasks whose dependencies are
// satisfied, minus those already terminal, narrowed by --task / --single. An
// empty result means the walk is done.
func (o *Orchestrator) nextWave(s *schedule) ([]string, error) {
	ready := s.run.DAG.NextReady(s.run.Completed)
	// Drop tasks already terminal (failed before, or skipped because an
	// upstream task failed). If nothing runnable remains, we're done.
	runnable := ready[:0:0]
	for _, id := range ready {
		if !s.terminal[id] {
			runnable = append(runnable, id)
		}
	}
	ready = runnable
	if len(ready) == 0 {
		return nil, nil
	}

	if o.targetTask != "" {
		filtered := make([]string, 0, 1)
		for _, id := range ready {
			if id == o.targetTask {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			taskExists := false
			for _, t := range s.tasks {
				if t.ID == o.targetTask {
					taskExists = true
					break
				}
			}
			if !taskExists {
				return nil, fmt.Errorf("task %s not found", o.targetTask)
			}
			return nil, fmt.Errorf("task %s dependencies not met", o.targetTask)
		}
		ready = filtered
	}

	if o.singleTask {
		ready = ready[:1]
	}
	return ready, nil
}

// runsWaveInParallel reports whether the whole ready batch may run
// concurrently. Targeted, single-task and A/B runs stay serial by construction.
func (o *Orchestrator) runsWaveInParallel() bool {
	return o.cfg.Execution.Parallel && o.targetTask == "" && !o.singleTask && len(o.abModels) != 2
}

// runWaveParallel runs one DAG level concurrently. step.Executor.Execute is
// concurrency-safe (per-task worker clone + Bookkeeper-guarded state/writes), so
// the LLM calls overlap while bookkeeping stays serialised. Results are
// processed after the barrier on this single scheduler goroutine.
func (o *Orchestrator) runWaveParallel(ctx context.Context, s *schedule, ready []string) error {
	o.drainCommands(ctx, s)
	if err := o.waitWhilePaused(ctx, s); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	type batchResult struct {
		t   *types.Task
		err error
	}
	results := make([]batchResult, 0, len(ready))
	var wg sync.WaitGroup
	var resMu sync.Mutex
	// Bound concurrency so a wide DAG level doesn't spawn dozens of expensive
	// provider processes at once.
	maxParallel := o.cfg.Execution.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 4
	}
	if fp := fanoutParallelism(s, ready); fp > 0 && fp < maxParallel {
		maxParallel = fp
	}
	sem := make(chan struct{}, maxParallel)
	for _, taskID := range ready {
		if o.skip[taskID] {
			if err := o.book.SetStatus(s.run.TasksPath, taskID, types.StatusSkipped); err != nil {
				charmbraceletlog.Warn("updating task status to skipped", "task", taskID, "err", err)
			}
			o.book.Lock()
			s.run.Completed[taskID] = true
			o.book.Unlock()
			s.terminal[taskID] = true
			o.emit(Event{Type: EventTaskComplete, TaskID: taskID, Status: types.StatusSkipped, Message: "skipped by user"})
			continue
		}
		t := findTask(s.tasks, taskID)
		if t == nil {
			return fmt.Errorf("task %s not found in parsed tasks", taskID)
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t *types.Task) {
			defer wg.Done()
			defer func() { <-sem }()
			err := o.exec.Execute(ctx, s.run, t)
			resMu.Lock()
			results = append(results, batchResult{t: t, err: err})
			resMu.Unlock()
		}(t)
	}
	wg.Wait()

	// Post-barrier: no worker goroutines are running, so the scheduler state
	// (terminal/failed/skipped) is mutated single-threaded here.
	for _, r := range results {
		if r.err == nil {
			s.terminal[r.t.ID] = true
			continue
		}
		if step.IsFatal(r.err) || ctx.Err() != nil {
			return r.err
		}
		o.recordFailure(s, r.t.ID, r.err)
	}
	return nil
}

// runWaveSerial runs the ready tasks one at a time, honouring pause/skip
// commands between them.
func (o *Orchestrator) runWaveSerial(ctx context.Context, s *schedule, ready []string) error {
	for _, taskID := range ready {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		o.drainCommands(ctx, s)
		if err := o.waitWhilePaused(ctx, s); err != nil {
			return err
		}

		if o.skip[taskID] {
			if err := o.book.SetStatus(s.run.TasksPath, taskID, types.StatusSkipped); err != nil {
				charmbraceletlog.Warn("updating task status to skipped", "task", taskID, "err", err)
			}
			s.run.Completed[taskID] = true
			o.emit(Event{Type: EventTaskComplete, TaskID: taskID, Status: types.StatusSkipped, Message: "skipped by user"})
			continue
		}

		t := findTask(s.tasks, taskID)
		if t == nil {
			return fmt.Errorf("task %s not found in parsed tasks", taskID)
		}

		if len(o.abModels) == 2 {
			if err := o.exec.RunAB(ctx, t, o.abModels); err != nil {
				return err
			}
			s.run.Completed[t.ID] = true
			s.terminal[t.ID] = true
			if err := o.book.SetStatus(s.run.TasksPath, t.ID, types.StatusPassed); err != nil {
				charmbraceletlog.Warn("updating task status to passed after a/b", "task", t.ID, "err", err)
			}
			continue
		}

		err := o.exec.Execute(ctx, s.run, t)
		if err == nil {
			s.terminal[t.ID] = true
			continue
		}
		// Fatal errors (cost ceiling, human-prompt escalation) and context
		// cancellation abort the whole run immediately. A targeted/single run
		// also surfaces the error directly. Any other error is a task-level
		// failure: record it, skip the tasks that transitively depend on it,
		// and keep executing the independent branches of the DAG.
		if step.IsFatal(err) || ctx.Err() != nil || o.targetTask != "" || o.singleTask {
			return err
		}
		o.recordFailure(s, t.ID, err)
	}
	return nil
}

// recordFailure marks a failed task terminal and cascades SKIPPED to every task
// that transitively depends on it, so the independent branches of the DAG keep
// running.
func (o *Orchestrator) recordFailure(s *schedule, taskID string, err error) {
	s.terminal[taskID] = true
	s.failed = append(s.failed, taskID)
	s.failures = append(s.failures, err.Error())
	charmbraceletlog.Warn("task failed; skipping its dependents and continuing independent branches", "task", taskID, "err", err)
	for _, dep := range s.run.DAG.TransitiveDependents(taskID) {
		if s.terminal[dep] {
			continue
		}
		s.terminal[dep] = true
		s.skipped = append(s.skipped, dep)
		if statusErr := o.book.SetStatus(s.run.TasksPath, dep, types.StatusSkipped); statusErr != nil {
			charmbraceletlog.Warn("marking dependent skipped", "task", dep, "err", statusErr)
		}
		o.emit(Event{
			Type:    EventTaskComplete,
			TaskID:  dep,
			Status:  types.StatusSkipped,
			Message: fmt.Sprintf("skipped: depends on failed task %s", taskID),
		})
	}
}

// findTask returns a pointer into tasks so status updates mutate the parsed
// slice the scheduler keeps reading.
func findTask(tasks []types.Task, id string) *types.Task {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
}
