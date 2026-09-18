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

// writesTheRunsTree reports whether a task edits the checkout the RUN shares.
//
// The first version of this asked "does it write", and that was the wrong
// question by one word: a task with its own worktree writes plenty and shares
// nothing, while a generated `git merge` node is a fixed command that writes the
// tree everyone else is reading. Both answers were wrong in the same release —
// the first cost parallelism that was safe, the second cost a run that died on
// `.git/index.lock` with two merges in one wave.
//
//   - An isolated item (WorkDir set) owns its tree: never contends.
//   - A step with no fixed command produces its result by EDITING: contends.
//     Asked of the NORMALISED kind, so a planner-written task with no `kind` at
//     all (types.NormalizeKind maps it to KindCode) errs toward isolation.
//   - A command step contends only when it says so with `writes_run_tree`,
//     because `npm test` and `git merge` are the same kind from here.
func writesTheRunsTree(t *types.Task) bool {
	if t.WorkDir != "" {
		return false
	}
	if t.WritesRunTree {
		return true
	}
	return !types.NormalizeKind(t.Kind).IsComputational()
}

// runWaveParallel runs one DAG level concurrently. step.Executor.Execute is
// concurrency-safe (per-task worker clone + Bookkeeper-guarded state/writes), so
// the LLM calls overlap while bookkeeping stays serialised. Results are
// processed after the barrier on this single scheduler goroutine.
//
// # One checkout, so only the computational steps really overlap
//
// "Concurrency-safe" above is a statement about corvex's own bookkeeping, and it
// was read for years as if it covered the repository too. It does not. A `code`
// step's product is an EDIT to the working tree, and every task in a run shares
// one tree: two workers in the same wave see each other's half-written files,
// and the checkpoint commit after the first one sweeps up whatever the second
// had in flight. Nothing in the ledger records that as a defect — the tasks both
// pass, and the diff is simply wrong.
//
// So a tree lock serialises the steps that write, while tool/test/repro steps —
// fixed commands that observe or act outside the checkout — keep overlapping,
// which is where the wall-clock actually comes from in a recipe. The isolation a
// real fan-out of `code` steps needs is a worktree per item, which the runner
// does not have yet (the A/B path is the only place that opens one); until it
// does, this is the difference between slow and silently wrong.
//
// Lock order is sem → treeMu, never the reverse: a goroutine holding treeMu
// while waiting for a slot would deadlock against the goroutines holding slots
// while waiting for treeMu.
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
	// The lock the comment above argues for, plus one line on the ledger when it
	// actually costs something: a wave with two writers is a wave that LOOKS
	// parallel in the recipe and is not, and an operator reading the timings
	// deserves to know why rather than to infer it.
	var treeMu sync.Mutex
	writers := 0
	for _, taskID := range ready {
		if t := findTask(s.tasks, taskID); t != nil && writesTheRunsTree(t) {
			writers++
		}
	}
	if writers > 1 {
		msg := fmt.Sprintf("%d steps in this wave write the run's checkout; they run one at a time (isolated items are not among them)", writers)
		o.emit(Event{Type: EventTaskWarn, Message: msg})
		charmbraceletlog.Warn("serialising the writers of this wave", "writers", writers)
	}
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
			if writesTheRunsTree(t) {
				treeMu.Lock()
				defer treeMu.Unlock()
			}
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
