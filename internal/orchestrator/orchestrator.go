package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/recovery"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// Options configures an Orchestrator instance.
type Options struct {
	Config     *config.Config
	Provider   provider.Provider
	WorkDir    string
	Events     chan<- Event
	TargetTask string
	SingleTask bool
	Sandbox    sandbox.Sandbox
	// ABModels, when set, enables A/B comparison for the targeted task:
	// each model in the slice runs in its own worktree, the Reviewer judges
	// both, and the winner is merged back into HEAD. Requires TargetTask or
	// SingleTask. Exactly 2 distinct models are expected.
	ABModels []string
	// Commands carries runtime control messages from a UI (pause, skip,
	// retry). Optional — when nil, the orchestrator runs uninterrupted.
	Commands <-chan Command
	// NoReplan disables the automatic replan that fires when spec.md has
	// drifted from the hash stored in anchor.yaml. When true and the spec
	// has changed, Run returns an actionable error instead of regenerating
	// tasks.md. Use this to protect manual edits.
	NoReplan bool
	// Force lets the run proceed on a dirty working tree by discarding the
	// uncommitted changes (destructive). When false (default), a dirty tree
	// at run start aborts the run with an actionable hint instead of wiping
	// the user's work.
	Force bool
	// ApproveGates auto-approves recipe "human-gate" stages. When false
	// (default), reaching a gate stops the run with an actionable message
	// instead of blocking; re-run with this set to proceed past the gate.
	ApproveGates bool
}

// Orchestrator coordinates task planning, execution, review, and recovery.
type Orchestrator struct {
	cfg        *config.Config
	provider   provider.Provider
	hooks      *hooks.Runner
	recovery   *recovery.Manager
	planner    *Planner
	worker     *Worker
	reviewer   *Reviewer
	advisor    *Advisor
	sandbox    sandbox.Sandbox
	events     chan<- Event
	workDir    string
	targetTask string
	singleTask bool
	abModels   []string
	commands   <-chan Command
	skip       map[string]bool // task IDs skipped by the user at runtime
	paused     bool            // toggled by Cmd{Pause,Resume}
	noReplan     bool          // mirror of Options.NoReplan
	force        bool          // mirror of Options.Force
	approveGates bool          // mirror of Options.ApproveGates
	ledger       *activity.Ledger
	runCtx     context.Context // set at the start of Run; lets emit() abort on cancel
	// mu guards the shared scheduler state (completed/terminal sets,
	// cumulative cost, anchorState) and serialises tasks.md/anchor.yaml writes
	// when tasks run in parallel. The expensive LLM calls (worker, reviewer)
	// run OUTSIDE the lock — only the fast bookkeeping is serialised.
	mu sync.Mutex
	// emitMu serialises ledger appends so parallel tasks don't interleave
	// bytes in activity.jsonl. The events channel send is already goroutine-safe.
	emitMu sync.Mutex
}

// setStatus serialises tasks.md rewrites (a whole-file read-modify-write) so
// concurrent tasks can't clobber each other's status updates.
func (o *Orchestrator) setStatus(tasksPath, id string, status types.TaskStatus) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return task.UpdateTaskStatus(tasksPath, id, status)
}

// addCost accumulates an attempt's cost into the per-task and run totals under
// the lock and returns both updated totals for ceiling checks.
func (o *Orchestrator) addCost(taskTotal, runTotal *float64, c float64) (taskT, runT float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	*taskTotal += c
	*runTotal += c
	return *taskTotal, *runTotal
}

// anchorContext builds the prompt context from the shared anchor under the
// lock (parallel tasks may be updating it concurrently).
func (o *Orchestrator) anchorContext(state *types.AnchorState, taskID string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return anchor.GenerateContext(*state, taskID)
}

// New creates an Orchestrator from the given options.
func New(opts Options) *Orchestrator {
	return &Orchestrator{
		cfg:        opts.Config,
		provider:   opts.Provider,
		hooks:      hooks.NewRunner(opts.WorkDir, 0),
		recovery:   recovery.NewManager(opts.WorkDir),
		planner:    NewPlanner(opts.Provider, opts.Config.Provider.Models.Planner, opts.WorkDir, opts.Config.AgentRouting),
		worker:     NewWorker(opts.Provider, opts.Config.Provider.Models.Worker, opts.WorkDir, opts.Sandbox),
		reviewer:   NewReviewer(opts.Provider, opts.Config.Provider.Models.Reviewer, opts.WorkDir),
		advisor:    NewAdvisor(opts.Provider, opts.Config.Provider.Models.Planner, opts.WorkDir),
		sandbox:    opts.Sandbox,
		events:     opts.Events,
		workDir:    opts.WorkDir,
		targetTask: opts.TargetTask,
		singleTask: opts.SingleTask,
		abModels:   opts.ABModels,
		commands:   opts.Commands,
		skip:         make(map[string]bool),
		noReplan:     opts.NoReplan,
		force:        opts.Force,
		approveGates: opts.ApproveGates,
	}
}

// Run executes the full orchestration loop for the given project.
func (o *Orchestrator) Run(ctx context.Context, project string) error {
	o.runCtx = ctx
	specPath, tasksPath, anchorPath := o.projectPaths(project)

	// Open the activity ledger early so every emitted event gets persisted.
	// Failure to open (e.g. project not yet planned) is non-fatal — emit()
	// no-ops when ledger is nil.
	if l, lerr := activity.New(o.workDir, project); lerr == nil {
		o.ledger = l
	} else {
		charmbraceletlog.Warn("activity ledger unavailable", "err", lerr)
	}

	if o.sandbox != nil {
		o.emit(Event{Type: EventSandboxPrepare})
		if err := o.sandbox.Prepare(ctx); err != nil {
			return fmt.Errorf("preparing sandbox: %w", err)
		}
		defer func() {
			o.emit(Event{Type: EventSandboxCleanup})
			if cleanupErr := o.sandbox.Cleanup(context.Background()); cleanupErr != nil {
				charmbraceletlog.Warn("sandbox cleanup", "err", cleanupErr)
			}
		}()
	}

	o.emit(Event{Type: EventRecoveryCheck})
	recResult, err := o.recovery.Guard()
	if err != nil {
		charmbraceletlog.Warn("recovery guard failed", "err", err)
	}
	if recResult != nil && recResult.Action == recovery.AbortDirty {
		if o.force {
			// User opted in via --force: discard the dirty tree.
			if _, derr := o.recovery.Check(); derr != nil {
				return fmt.Errorf("--force: discarding dirty tree: %w", derr)
			}
			o.emit(Event{Type: EventRecoveryResult, Message: fmt.Sprintf("--force: discarded %d uncommitted change(s)", len(recResult.DirtyFiles))})
		} else {
			preview := recResult.DirtyFiles
			if len(preview) > 10 {
				preview = preview[:10]
			}
			return fmt.Errorf(
				"working tree has %d uncommitted change(s):\n  %s\n\n"+
					"Corvex won't run on a dirty tree: it commits a checkpoint after every task, "+
					"and its crash-recovery reset could discard this work.\n"+
					"→ commit or stash your changes, then re-run\n"+
					"→ or pass --force to let corvex reset the tree first (DESTRUCTIVE — discards the changes above)",
				len(recResult.DirtyFiles), strings.Join(preview, "\n  "),
			)
		}
	} else if recResult != nil {
		o.emit(Event{Type: EventRecoveryResult, Message: recResult.Message})
	}

	// Expose repo-local skills (.corvex/skills/*) to the Worker via
	// .claude/skills/ for this run; cleaned up when Run returns.
	defer o.materializeSkills()()

	anchorState, err := anchor.Load(anchorPath)
	if err != nil {
		charmbraceletlog.Warn("loading anchor", "err", err)
	}

	plan, planErr := o.needsPlanning(specPath, tasksPath, anchorState)
	if planErr != nil {
		return fmt.Errorf("checking planning needs: %w", planErr)
	}
	if plan {
		// If tasks.md already exists, this is a replan triggered by spec drift.
		// Honor --no-replan and refuse, or back up the existing file before
		// the planner overwrites it.
		_, tasksExist := os.Stat(tasksPath)
		isReplan := tasksExist == nil
		if isReplan && o.noReplan {
			return fmt.Errorf("spec.md has changed since the last plan, but --no-replan is set; run `corvex plan %s` to regenerate tasks.md, or revert spec.md to its previous state", project)
		}
		var backupPath string
		if isReplan {
			backupPath = tasksPath + ".bak-" + time.Now().UTC().Format("20060102-150405")
			if err := os.Rename(tasksPath, backupPath); err != nil {
				return fmt.Errorf("backing up tasks.md before replan: %w", err)
			}
		}
		msg := "Planning tasks..."
		if isReplan {
			msg = fmt.Sprintf("Spec changed — replanning. Backup: %s", filepath.Base(backupPath))
		}
		o.emit(Event{Type: EventPlanStart, Message: msg})
		if err := o.planner.Plan(ctx, specPath, anchorPath, tasksPath); err != nil {
			return fmt.Errorf("planning: %w", err)
		}
		o.emit(Event{Type: EventPlanComplete})
	}

	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}
	// A tasks.md that parses to zero tasks must never be reported as a
	// successful empty run. It means planning failed to produce structured
	// output (e.g. the model narrated instead of emitting the file). Fail
	// loudly with a path so the user can inspect and re-plan.
	if len(tasks) == 0 {
		return fmt.Errorf("no tasks to run: %s parsed to zero tasks (planning may have produced prose instead of a tasks.md). Inspect the file and re-run `corvex plan %s`", tasksPath, project)
	}

	d := dag.NewDAG(tasks)
	if err := d.Validate(); err != nil {
		return fmt.Errorf("validating DAG: %w", err)
	}
	o.emit(Event{Type: EventDAGResolved, Total: d.Size()})

	// Tasks left in RUNNING from a previous interrupted run would otherwise
	// stay in limbo: they're not in `completed` (so their dependents block)
	// but the scheduler also never re-picks them. Reset to PENDING so the
	// dependency chain can resume.
	for i := range tasks {
		if tasks[i].Status == types.StatusRunning {
			charmbraceletlog.Warn("task left RUNNING from a previous run; resetting to PENDING for re-execution", "task", tasks[i].ID)
			tasks[i].Status = types.StatusPending
			if err := o.setStatus(tasksPath, tasks[i].ID, types.StatusPending); err != nil {
				charmbraceletlog.Warn("persisting RUNNING→PENDING reset", "task", tasks[i].ID, "err", err)
			}
		}
	}

	completed := make(map[string]bool)
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			completed[t.ID] = true
		}
	}

	// DAG integrity check: every PASSED task must have all dependencies also
	// PASSED. Replans can change the DAG and leave previously-passed tasks
	// with stale (now-unsatisfied) deps; running their dependents would
	// compound the corruption invisibly.
	for _, t := range tasks {
		if t.Status != types.StatusPassed {
			continue
		}
		for _, dep := range t.DependsOn {
			if !completed[dep] {
				return fmt.Errorf(
					"DAG integrity violation: task %s is PASSED but its dependency %s is not.\n"+
						"This usually means a replan changed the DAG. To fix, choose one:\n"+
						"  1) Reset %s to ⬜ PENDING in tasks.md (delete its artifacts first if they were written), so it re-validates under the new DAG.\n"+
						"  2) Manually run %s by marking it PENDING and re-running, then retry.",
					t.ID, dep, t.ID, dep,
				)
			}
		}
	}

	var totalCostUSD float64

	// terminal holds every task that must never be re-scheduled: PASSED ones
	// satisfy dependencies (they live in `completed`), while FAILED and SKIPPED
	// ones do not — but all three are done. Without tracking FAILED/SKIPPED
	// separately, NextReady (which only consults `completed`) would hand a
	// failed task back every iteration and spin forever.
	terminal := make(map[string]bool, len(completed))
	for id := range completed {
		terminal[id] = true
	}
	var failedTasks, skippedTasks []string
	var failureDetails []string

	for {
		ready := d.NextReady(completed)
		// Drop tasks already terminal (failed before, or skipped because an
		// upstream task failed). If nothing runnable remains, we're done.
		runnable := ready[:0:0]
		for _, id := range ready {
			if !terminal[id] {
				runnable = append(runnable, id)
			}
		}
		ready = runnable
		if len(ready) == 0 {
			break
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
				for _, t := range tasks {
					if t.ID == o.targetTask {
						taskExists = true
						break
					}
				}
				if !taskExists {
					return fmt.Errorf("task %s not found", o.targetTask)
				}
				return fmt.Errorf("task %s dependencies not met", o.targetTask)
			}
			ready = filtered
		}

		if o.singleTask {
			ready = ready[:1]
		}

		// Parallel branch: when enabled and not in targeted/single/AB mode, run
		// the whole ready batch (one DAG level) concurrently. executeTask is
		// concurrency-safe (per-task worker clone + mutex-guarded state/writes),
		// so the LLM calls overlap while bookkeeping stays serialised. Results
		// are processed after the barrier on this single scheduler goroutine.
		if o.cfg.Execution.Parallel && o.targetTask == "" && !o.singleTask && len(o.abModels) != 2 {
			o.drainCommands(ctx, tasksPath, tasks, completed)
			if err := o.waitWhilePaused(ctx, tasksPath, tasks, completed); err != nil {
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
			// Bound concurrency so a wide DAG level doesn't spawn dozens of
			// expensive provider processes at once.
			maxParallel := o.cfg.Execution.MaxParallel
			if maxParallel <= 0 {
				maxParallel = 4
			}
			sem := make(chan struct{}, maxParallel)
			for _, taskID := range ready {
				if o.skip[taskID] {
					if err := o.setStatus(tasksPath, taskID, types.StatusSkipped); err != nil {
						charmbraceletlog.Warn("updating task status to skipped", "task", taskID, "err", err)
					}
					o.mu.Lock()
					completed[taskID] = true
					o.mu.Unlock()
					terminal[taskID] = true
					o.emit(Event{Type: EventTaskComplete, TaskID: taskID, Status: types.StatusSkipped, Message: "skipped by user"})
					continue
				}
				var t *types.Task
				for i := range tasks {
					if tasks[i].ID == taskID {
						t = &tasks[i]
						break
					}
				}
				if t == nil {
					return fmt.Errorf("task %s not found in parsed tasks", taskID)
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(t *types.Task) {
					defer wg.Done()
					defer func() { <-sem }()
					err := o.executeTask(ctx, t, tasksPath, anchorPath, &anchorState, completed, d, &totalCostUSD)
					resMu.Lock()
					results = append(results, batchResult{t: t, err: err})
					resMu.Unlock()
				}(t)
			}
			wg.Wait()

			// Post-barrier: no worker goroutines are running, so the scheduler
			// state (terminal/failed/skipped) is mutated single-threaded here.
			for _, r := range results {
				if r.err == nil {
					terminal[r.t.ID] = true
					continue
				}
				if isFatal(r.err) || ctx.Err() != nil {
					return r.err
				}
				terminal[r.t.ID] = true
				failedTasks = append(failedTasks, r.t.ID)
				failureDetails = append(failureDetails, r.err.Error())
				charmbraceletlog.Warn("task failed; skipping its dependents and continuing independent branches", "task", r.t.ID, "err", r.err)
				for _, dep := range d.TransitiveDependents(r.t.ID) {
					if terminal[dep] {
						continue
					}
					terminal[dep] = true
					skippedTasks = append(skippedTasks, dep)
					if statusErr := o.setStatus(tasksPath, dep, types.StatusSkipped); statusErr != nil {
						charmbraceletlog.Warn("marking dependent skipped", "task", dep, "err", statusErr)
					}
					o.emit(Event{Type: EventTaskComplete, TaskID: dep, Status: types.StatusSkipped, Message: fmt.Sprintf("skipped: depends on failed task %s", r.t.ID)})
				}
			}
			continue
		}

		for _, taskID := range ready {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			o.drainCommands(ctx, tasksPath, tasks, completed)
			if err := o.waitWhilePaused(ctx, tasksPath, tasks, completed); err != nil {
				return err
			}

			if o.skip[taskID] {
				if err := o.setStatus(tasksPath, taskID, types.StatusSkipped); err != nil {
					charmbraceletlog.Warn("updating task status to skipped", "task", taskID, "err", err)
				}
				completed[taskID] = true
				o.emit(Event{Type: EventTaskComplete, TaskID: taskID, Status: types.StatusSkipped, Message: "skipped by user"})
				continue
			}

			var t *types.Task
			for i := range tasks {
				if tasks[i].ID == taskID {
					t = &tasks[i]
					break
				}
			}
			if t == nil {
				return fmt.Errorf("task %s not found in parsed tasks", taskID)
			}

			if len(o.abModels) == 2 {
				if err := o.runAB(ctx, t, o.abModels); err != nil {
					return err
				}
				completed[t.ID] = true
				terminal[t.ID] = true
				if err := o.setStatus(tasksPath, t.ID, types.StatusPassed); err != nil {
					charmbraceletlog.Warn("updating task status to passed after a/b", "task", t.ID, "err", err)
				}
			} else if err := o.executeTask(ctx, t, tasksPath, anchorPath, &anchorState, completed, d, &totalCostUSD); err != nil {
				// Fatal errors (cost ceiling, human-prompt escalation) and
				// context cancellation abort the whole run immediately. A
				// targeted/single run also surfaces the error directly. Any
				// other error is a task-level failure: record it, skip the
				// tasks that transitively depend on it, and keep executing the
				// independent branches of the DAG.
				if isFatal(err) || ctx.Err() != nil || o.targetTask != "" || o.singleTask {
					return err
				}
				terminal[t.ID] = true
				failedTasks = append(failedTasks, t.ID)
				failureDetails = append(failureDetails, err.Error())
				charmbraceletlog.Warn("task failed; skipping its dependents and continuing independent branches", "task", t.ID, "err", err)
				for _, dep := range d.TransitiveDependents(t.ID) {
					if terminal[dep] {
						continue
					}
					terminal[dep] = true
					skippedTasks = append(skippedTasks, dep)
					if statusErr := o.setStatus(tasksPath, dep, types.StatusSkipped); statusErr != nil {
						charmbraceletlog.Warn("marking dependent skipped", "task", dep, "err", statusErr)
					}
					o.emit(Event{
						Type:    EventTaskComplete,
						TaskID:  dep,
						Status:  types.StatusSkipped,
						Message: fmt.Sprintf("skipped: depends on failed task %s", t.ID),
					})
				}
			} else {
				terminal[t.ID] = true
			}
		}

		if o.targetTask != "" || o.singleTask {
			break
		}
	}

	if len(failedTasks) > 0 {
		msg := fmt.Sprintf("run completed with failures: %d task(s) failed (%s)",
			len(failedTasks), strings.Join(failedTasks, ", "))
		if len(skippedTasks) > 0 {
			msg += fmt.Sprintf("; %d dependent task(s) skipped (%s)",
				len(skippedTasks), strings.Join(skippedTasks, ", "))
		}
		// Preserve each task's specific failure reason so the summary stays
		// diagnosable (e.g. "reviewer never produced a verdict").
		msg += "\n  - " + strings.Join(failureDetails, "\n  - ")
		return fmt.Errorf("%s", msg)
	}

	if threshold := o.cfg.Execution.InsightThreshold; threshold != 0 && o.targetTask == "" && !o.singleTask {
		insights, err := o.advisor.Analyze(ctx, tasks, o.cfg.AgentRouting, threshold)
		if err != nil {
			charmbraceletlog.Warn("advisor analysis failed", "err", err)
		}
		for i := range insights {
			o.emit(Event{Type: EventInsight, Insight: &insights[i]})
		}
	}

	o.emit(Event{Type: EventDone})
	return nil
}

func (o *Orchestrator) executeTask(
	ctx context.Context,
	t *types.Task,
	tasksPath, anchorPath string,
	anchorState *types.AnchorState,
	completed map[string]bool,
	d *dag.DAG,
	totalCostUSD *float64,
) error {
	// Command stages run a shell step instead of an AI worker — no LLM, no
	// reviewer, no TASK-REPORT. Dispatch early so the AI path stays clean.
	if t.Kind == "command" {
		return o.runCommandStage(ctx, t, tasksPath, anchorPath, anchorState, completed, d)
	}
	if t.Kind == "human-gate" {
		return o.runHumanGate(ctx, t, tasksPath, anchorPath, anchorState, completed, d)
	}

	maxRetries := o.cfg.Execution.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	diagnosis := ""
	categoryCounts := make(map[string]int)
	// Per-task worker clone: escalation upgrades this clone's model and sets
	// its stream callback, so parallel tasks never race on shared worker state.
	worker := o.worker.clone()

	var taskTotalCostUSD float64

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Respect cancellation between attempts so a cancelled run aborts
		// promptly instead of burning the remaining retries.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {
			o.emit(Event{Type: EventRetry, TaskID: t.ID, Attempt: attempt, Message: diagnosis})
			if _, err := o.recovery.Check(); err != nil {
				charmbraceletlog.Warn("recovery check on retry", "task", t.ID, "err", err)
			}
		}

		hookEnv := hooks.HookEnv{TaskID: t.ID, Project: o.cfg.Project.Name, Status: "running"}
		if _, err := o.hooks.Run(ctx, hooks.PreTask, hookEnv); err != nil {
			charmbraceletlog.Warn("pre-task hook", "task", t.ID, "err", err)
		}

		if err := o.setStatus(tasksPath, t.ID, types.StatusRunning); err != nil {
			charmbraceletlog.Warn("updating task status to running", "task", t.ID, "err", err)
		}
		o.emit(Event{Type: EventTaskStart, TaskID: t.ID, Attempt: attempt, Message: t.Title})

		contextDocs := loadContextDocs(o.workDir, o.cfg.Context.AlwaysInclude)
		agentPrompt := loadAgentPrompt(o.workDir, o.cfg.AgentRouting, t.Type)
		anchorCtx := o.anchorContext(anchorState, t.ID)

		// Stream per-chunk events from the worker so the TUI panel can show
		// what the AI is doing (tool calls, intermediate text) instead of
		// just "worker S03" for several minutes. Each stream event also
		// refreshes the liveness clock the watchdog reads.
		taskID := t.ID
		live := newLiveness()
		worker.SetOnStream(func(se types.StreamEvent) {
			live.touch(streamSummary(se))
			ev := se
			o.emit(Event{Type: EventTaskStream, TaskID: taskID, Stream: &ev})
		})

		// Watchdog: warn after task_warn_minutes, and CANCEL (not just warn)
		// when the attempt blows the wall-clock ceiling or goes idle with no
		// stream output — the signatures of a hung provider. Cancelling the
		// derived context unblocks worker.Execute, which returns a ctx error
		// and feeds the normal retry/fail path. A diagnostic event records
		// where it stalled.
		taskCtx, cancelTask := context.WithCancel(ctx)
		watchDone := make(chan struct{})
		timedOut := newTimeoutFlag()
		// Idle detection is only meaningful on the streaming path; a buffered
		// sandbox produces no per-chunk events, so its idle clock would tick
		// falsely. Buffered runs rely on the wall-clock ceiling.
		streaming := isLocalOrNilSandbox(o.sandbox)
		go o.watchTask(taskCtx, cancelTask, watchDone, taskID, live, timedOut, streaming)

		result, err := worker.Execute(taskCtx, t, anchorCtx, contextDocs, agentPrompt, diagnosis)
		close(watchDone)
		cancelTask()
		worker.SetOnStream(nil)
		if reason := timedOut.reason(); reason != "" && err != nil {
			// Replace the opaque "context canceled" with the watchdog's
			// diagnosis so retries and logs say *why* the attempt died.
			err = fmt.Errorf("%s", reason)
		}
		var workerCost float64
		if result != nil {
			workerCost = result.CostUSD
		}
		if err != nil {
			tt, rt := o.addCost(&taskTotalCostUSD, totalCostUSD, workerCost)
			if cap := o.cfg.Execution.MaxCostPerTaskUSD; cap > 0 && tt > cap {
				return fatal(fmt.Errorf("task %s cost $%.2f exceeded per-task ceiling $%.2f (configure execution.max_cost_per_task_usd to raise)", t.ID, tt, cap))
			}
			if cap := o.cfg.Execution.MaxCostUSD; cap > 0 && rt > cap {
				return fatal(fmt.Errorf("run aborted: cumulative cost $%.2f exceeded ceiling $%.2f (configure execution.max_cost_usd to raise)", rt, cap))
			}
			if attempt == maxRetries {
				if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
					charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
				}
				hookEnv.Status = "failed"
				o.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
				o.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
				o.emit(Event{Type: EventTaskComplete, TaskID: t.ID, Status: types.StatusFailed})
				return fmt.Errorf("task %s failed after %d attempts: %w", t.ID, attempt+1, err)
			}
			diagnosis = err.Error()
			continue
		}

		o.emit(Event{Type: EventReviewStart, TaskID: t.ID})
		reviewResult, reviewErr := o.reviewer.Review(ctx, t)
		var reviewerCost float64
		if reviewErr == nil && reviewResult != nil {
			reviewerCost = reviewResult.CostUSD
		}
		attemptCost := workerCost + reviewerCost
		tt, rt := o.addCost(&taskTotalCostUSD, totalCostUSD, attemptCost)
		if cap := o.cfg.Execution.MaxCostPerTaskUSD; cap > 0 && tt > cap {
			return fatal(fmt.Errorf("task %s cost $%.2f exceeded per-task ceiling $%.2f (configure execution.max_cost_per_task_usd to raise)", t.ID, tt, cap))
		}
		if cap := o.cfg.Execution.MaxCostUSD; cap > 0 && rt > cap {
			return fatal(fmt.Errorf("run aborted: cumulative cost $%.2f exceeded ceiling $%.2f (configure execution.max_cost_usd to raise)", rt, cap))
		}
		if reviewErr != nil {
			if attempt == maxRetries {
				if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
					charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
				}
				return fmt.Errorf("task %s review failed: %w", t.ID, reviewErr)
			}
			diagnosis = reviewErr.Error()
			continue
		}

		o.emit(Event{
			Type:    EventReviewResult,
			TaskID:  t.ID,
			Message: string(reviewResult.Verdict),
		})

		if reviewResult.Verdict == VerdictPass {
			// Determine the next task first — it decides whether a HANDOFF is
			// required (the last task in the DAG has nothing to hand off to).
			o.mu.Lock()
			nextCompleted := make(map[string]bool, len(completed)+1)
			for k, v := range completed {
				nextCompleted[k] = v
			}
			o.mu.Unlock()
			nextCompleted[t.ID] = true

			nextReady := d.NextReady(nextCompleted)
			nextTask := ""
			if len(nextReady) > 0 {
				nextTask = nextReady[0]
			}

			// The Worker must hand off structured context to the next task.
			// A passing implementation with no TASK-REPORT (or an empty HANDOFF
			// when a next task exists) is rejected and retried — a silent empty
			// anchor entry is worse than a retry, because every downstream task
			// then runs blind. The final task may omit HANDOFF.
			report, hasReport := parseTaskReport(result.Output)
			missingHandoff := nextTask != "" && strings.TrimSpace(report.Handoff) == ""
			if !hasReport || missingHandoff {
				diagnosis = "your previous response passed review but was missing the required TASK-REPORT block (with a non-empty HANDOFF). Re-do the task and end your response with the TASK-REPORT block: SUMMARY, DECISIONS, HANDOFF."
				if attempt == maxRetries {
					if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
						charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
					}
					o.emit(Event{Type: EventTaskComplete, TaskID: t.ID, Status: types.StatusFailed, Message: "passed review but never produced a TASK-REPORT/HANDOFF"})
					return fmt.Errorf("task %s passed review but never produced a TASK-REPORT with a HANDOFF after %d attempts", t.ID, maxRetries+1)
				}
				o.emit(Event{Type: EventRetry, TaskID: t.ID, Attempt: attempt + 1, Message: "missing TASK-REPORT/HANDOFF"})
				continue
			}

			hookEnv.Status = "passed"
			o.runHook(ctx, hooks.OnSuccess, hookEnv, t.ID)
			o.runHook(ctx, hooks.PostTask, hookEnv, t.ID)

			// Serialise the git + state writes as one unit. Under parallel
			// execution, concurrent checkpoints (git add/commit) or interleaved
			// tasks.md/anchor.yaml writes would corrupt the repo, so the whole
			// bookkeeping block runs under the lock with raw (non-locking) ops.
			// Writing state files BEFORE the commit captures them in the
			// checkpoint — the previous commit-first order left them dirty and
			// the next run's recovery reverted completed work.
			o.mu.Lock()
			if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusPassed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to passed", "task", t.ID, "err", statusErr)
			}
			// Capture real changed files while the working tree still diffs
			// against HEAD. Fall back to the planned lists on git error.
			realCreated, realModified, changedErr := o.recovery.ChangedFiles()
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
			*anchorState = anchor.Update(*anchorState, anchor.TaskResult{
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
				TotalTasks:      d.Size(),
			})
			if err := anchor.Save(anchorPath, *anchorState); err != nil {
				charmbraceletlog.Warn("saving anchor", "task", t.ID, "err", err)
			}
			if o.cfg.Execution.AutoCommit {
				if err := o.recovery.MarkCheckpoint(t.ID); err != nil {
					charmbraceletlog.Warn("marking checkpoint", "task", t.ID, "err", err)
				}
			}
			completed[t.ID] = true
			o.mu.Unlock()

			o.emit(Event{Type: EventCheckpoint, TaskID: t.ID})
			o.emit(Event{
				Type:       EventTaskComplete,
				TaskID:     t.ID,
				Status:     types.StatusPassed,
				CostUSD:    attemptCost,
				TokensIn:   result.TokensIn + reviewResult.TokensIn,
				TokensOut:  result.TokensOut + reviewResult.TokensOut,
				DurationMs: result.DurationMs + reviewResult.DurationMs,
			})
			return nil
		}

		if reviewResult.Verdict == VerdictIndeterminate {
			if attempt == maxRetries {
				if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
					charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
				}
				return fmt.Errorf("task %s reviewer never produced a verdict after %d attempts", t.ID, maxRetries+1)
			}
			diagnosis = "reviewer produced no parseable verdict on the previous attempt"
			continue
		}

		diagnosis = reviewResult.Summary
		hookEnv.Status = "failed"
		o.runHook(ctx, hooks.OnFailure, hookEnv, t.ID)
		o.runHook(ctx, hooks.PostTask, hookEnv, t.ID)
		o.emit(Event{
			Type:    EventTaskComplete,
			TaskID:  t.ID,
			Status:  types.StatusFailed,
			Message: diagnosis,
		})

		if cat := reviewResult.Category; cat != "" {
			categoryCounts[cat]++
			decision := resolveEscalation(o.cfg.Review, cat, categoryCounts[cat])
			switch decision.Action {
			case ActionUpgradeModel:
				if decision.UpgradeTo != "" && decision.UpgradeTo != worker.model {
					charmbraceletlog.Info("escalation: upgrading worker model",
						"task", t.ID, "category", cat, "from", worker.model, "to", decision.UpgradeTo)
					worker.model = decision.UpgradeTo
				}
			case ActionHumanPrompt:
				path, err := writeHumanEscalation(o.workDir, o.cfg.Project.Name, t.ID, cat, reviewResult.Summary)
				if err != nil {
					charmbraceletlog.Warn("writing human escalation", "task", t.ID, "err", err)
				} else {
					charmbraceletlog.Warn("escalation: human review requested",
						"task", t.ID, "category", cat, "file", path)
				}
				if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
					charmbraceletlog.Warn("updating task status to failed after escalation", "task", t.ID, "err", statusErr)
				}
				return fatal(fmt.Errorf("task %s escalated to human review (category %s); see %s", t.ID, cat, path))
			case ActionSpawnInvestigation:
				investigationDiagnosis := o.runInvestigation(ctx, t, reviewResult.Summary)
				diagnosis = investigationDiagnosis
				o.emit(Event{
					Type:    EventRetry,
					TaskID:  t.ID,
					Attempt: attempt,
					Message: "spawn-investigation: " + investigationDiagnosis,
				})
			}
		}
	}

	if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
		charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
	}
	return fmt.Errorf("task %s failed review after %d attempts", t.ID, maxRetries+1)
}

func (o *Orchestrator) runInvestigation(ctx context.Context, t *types.Task, reviewerSummary string) string {
	prompt := buildInvestigationPrompt(t, reviewerSummary)
	result, err := o.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        o.advisor.model,
		WorkDir:      o.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		charmbraceletlog.Warn("spawn-investigation failed", "task", t.ID, "err", err)
		return reviewerSummary
	}
	if result == nil || strings.TrimSpace(result.Output) == "" {
		return reviewerSummary
	}
	return strings.TrimSpace(result.Output)
}

func buildInvestigationPrompt(t *types.Task, reviewerSummary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a software engineering investigator. A task has been rejected by the reviewer.\n\n")
	fmt.Fprintf(&b, "## Task\n\n")
	fmt.Fprintf(&b, "ID: %s\n", t.ID)
	fmt.Fprintf(&b, "Title: %s\n", t.Title)
	if t.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", t.Description)
	}
	if len(t.Criteria) > 0 {
		b.WriteString("\nSuccess criteria:\n")
		for _, c := range t.Criteria {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\n## Reviewer Feedback\n\n%s\n\n", reviewerSummary)
	b.WriteString("## Your Goal\n\n")
	b.WriteString("Investigate the codebase (use Read, Glob, Grep — read-only) and provide:\n")
	b.WriteString("1. A concrete root-cause diagnosis: what exactly is wrong?\n")
	b.WriteString("2. A recommended fix approach: what specific changes should be made?\n\n")
	b.WriteString("Be concise and actionable. Your output will guide the next implementation attempt.\n")
	return b.String()
}

// runCommandStage executes a recipe "command" stage: it runs the stage's shell
// command as a deterministic pipeline step. Exit 0 → PASSED; non-zero → FAILED
// (a task-level failure, so dependents are skipped per CH-07). No LLM, no
// reviewer, no TASK-REPORT, no cost. Concurrency-safe like executeTask.
func (o *Orchestrator) runCommandStage(
	ctx context.Context,
	t *types.Task,
	tasksPath, anchorPath string,
	anchorState *types.AnchorState,
	completed map[string]bool,
	d *dag.DAG,
) error {
	if strings.TrimSpace(t.Command) == "" {
		if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
			charmbraceletlog.Warn("updating command task status to failed", "task", t.ID, "err", statusErr)
		}
		o.emit(Event{Type: EventTaskComplete, TaskID: t.ID, Status: types.StatusFailed, Message: "command stage has no command"})
		return fmt.Errorf("task %s: command stage has no command", t.ID)
	}

	if statusErr := o.setStatus(tasksPath, t.ID, types.StatusRunning); statusErr != nil {
		charmbraceletlog.Warn("updating command task status to running", "task", t.ID, "err", statusErr)
	}
	o.emit(Event{Type: EventTaskStart, TaskID: t.ID})

	// Reuse the per-task wall-clock ceiling so a hung command can't stall the run.
	cmdCtx := ctx
	if mins := o.cfg.Execution.TaskTimeoutMinutes; mins > 0 {
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
		o.emit(Event{Type: EventTaskStream, TaskID: t.ID, Stream: &types.StreamEvent{Type: types.EventToolUse, Tool: "command", Content: label}})

		out, runErr := o.runShell(cmdCtx, t.Command)
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			ev := types.StreamEvent{Type: types.EventToolResult, Content: trimmed}
			o.emit(Event{Type: EventTaskStream, TaskID: t.ID, Stream: &ev})
		}

		// Decide success for this iteration.
		ok := runErr == nil
		if strings.TrimSpace(t.LoopUntil) != "" {
			// The until-condition governs; the command is the work that may
			// make it pass over successive iterations.
			_, untilErr := o.runShell(cmdCtx, t.LoopUntil)
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
			o.markStagePassed(t, tasksPath, anchorPath, anchorState, completed, d, summary, time.Since(start).Milliseconds())
			return nil
		}
	}

	if statusErr := o.setStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
		charmbraceletlog.Warn("updating command task status to failed", "task", t.ID, "err", statusErr)
	}
	cond := "command exit"
	if strings.TrimSpace(t.LoopUntil) != "" {
		cond = fmt.Sprintf("loop condition %q", t.LoopUntil)
	}
	msg := fmt.Sprintf("%s did not pass after %d iteration(s): %v", cond, maxIter, lastErr)
	o.emit(Event{Type: EventTaskComplete, TaskID: t.ID, Status: types.StatusFailed, Message: msg})
	return fmt.Errorf("task %s: %s", t.ID, msg)
}

// runShell runs a shell command in the workDir and returns combined output.
func (o *Orchestrator) runShell(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = o.workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// markStagePassed records a non-AI stage (command, approved human-gate) as
// PASSED with the serialised git+state bookkeeping that executeTask uses, then
// emits the checkpoint + completion events. Concurrency-safe.
func (o *Orchestrator) markStagePassed(
	t *types.Task,
	tasksPath, anchorPath string,
	anchorState *types.AnchorState,
	completed map[string]bool,
	d *dag.DAG,
	summary string,
	durationMs int64,
) {
	o.mu.Lock()
	if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusPassed); statusErr != nil {
		charmbraceletlog.Warn("updating stage status to passed", "task", t.ID, "err", statusErr)
	}
	nextCompleted := make(map[string]bool, len(completed)+1)
	for k, v := range completed {
		nextCompleted[k] = v
	}
	nextCompleted[t.ID] = true
	nextTask := ""
	if nr := d.NextReady(nextCompleted); len(nr) > 0 {
		nextTask = nr[0]
	}
	*anchorState = anchor.Update(*anchorState, anchor.TaskResult{
		Completed:  types.CompletedTask{ID: t.ID, Title: t.Title, Summary: summary},
		NextTask:   nextTask,
		TotalTasks: d.Size(),
	})
	if err := anchor.Save(anchorPath, *anchorState); err != nil {
		charmbraceletlog.Warn("saving anchor", "task", t.ID, "err", err)
	}
	if o.cfg.Execution.AutoCommit {
		if err := o.recovery.MarkCheckpoint(t.ID); err != nil {
			charmbraceletlog.Warn("marking checkpoint", "task", t.ID, "err", err)
		}
	}
	completed[t.ID] = true
	o.mu.Unlock()

	o.emit(Event{Type: EventCheckpoint, TaskID: t.ID})
	o.emit(Event{Type: EventTaskComplete, TaskID: t.ID, Status: types.StatusPassed, DurationMs: durationMs})
}

// runHumanGate handles a recipe "human-gate" stage. With --approve-gates it is
// auto-approved and passes; otherwise it stops the run with an actionable
// message, leaving the gate PENDING so a re-run with approval proceeds past it.
func (o *Orchestrator) runHumanGate(
	_ context.Context,
	t *types.Task,
	tasksPath, anchorPath string,
	anchorState *types.AnchorState,
	completed map[string]bool,
	d *dag.DAG,
) error {
	o.emit(Event{Type: EventHumanGate, TaskID: t.ID, Message: t.Title})
	if o.approveGates {
		charmbraceletlog.Info("human-gate auto-approved (--approve-gates)", "task", t.ID)
		o.markStagePassed(t, tasksPath, anchorPath, anchorState, completed, d, "human-gate approved: "+t.Title, 0)
		return nil
	}
	return fatal(fmt.Errorf("human-gate %q (%s) reached — review the work so far, then re-run with --approve-gates to proceed past it", t.ID, t.Title))
}

func (o *Orchestrator) projectPaths(project string) (specPath, tasksPath, anchorPath string) {
	base := filepath.Join(o.workDir, ".corvex", "tasks", project)
	return filepath.Join(base, "spec.md"),
		filepath.Join(base, "tasks.md"),
		filepath.Join(base, "anchor.yaml")
}

func (o *Orchestrator) needsPlanning(specPath, tasksPath string, state types.AnchorState) (bool, error) {
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := os.Stat(tasksPath); os.IsNotExist(err) {
		return true, nil
	}

	hash, err := anchor.SpecHash(specPath)
	if err != nil {
		return false, err
	}
	return hash != state.SpecHash, nil
}

func (o *Orchestrator) emit(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}

	// Persist to the activity ledger (skip noisy stream chunks — those go to
	// the TUI but explode disk usage and reading time without adding
	// debugging signal). Errors are warned, never blocking the run.
	if o.ledger != nil && ev.Type != EventTaskStream {
		o.emitMu.Lock()
		err := o.ledger.Append(ledgerEntryFromEvent(ev))
		o.emitMu.Unlock()
		if err != nil {
			charmbraceletlog.Warn("activity ledger append", "type", ev.Type, "err", err)
		}
	}

	if o.events == nil {
		return
	}
	// Block until the consumer drains the channel — but never forever. The
	// previous unconditional `o.events <- ev` deadlocked the orchestrator if
	// the consumer (e.g. the TUI) exited early and stopped draining: the send
	// blocked, Run never returned, and ctx cancellation couldn't unwedge it.
	// Selecting on the run context lets a cancelled run drain to completion.
	var done <-chan struct{}
	if o.runCtx != nil {
		done = o.runCtx.Done()
	}
	select {
	case o.events <- ev:
	case <-done:
	}
}

// ledgerEntryFromEvent translates an orchestration event into the compact
// JSONL schema. Status is the canonical PASSED/FAILED/etc. string for
// task_complete events, empty otherwise.
func ledgerEntryFromEvent(ev Event) activity.Entry {
	e := activity.Entry{
		Timestamp:  ev.Timestamp,
		Type:       string(ev.Type),
		TaskID:     ev.TaskID,
		Attempt:    ev.Attempt,
		DurationMs: ev.DurationMs,
		CostUSD:    ev.CostUSD,
		TokensIn:   ev.TokensIn,
		TokensOut:  ev.TokensOut,
		Message:    ev.Message,
	}
	if ev.Status != "" {
		e.Status = string(ev.Status)
	}
	return e
}

func (o *Orchestrator) runHook(ctx context.Context, name string, env hooks.HookEnv, taskID string) {
	if _, err := o.hooks.Run(ctx, name, env); err != nil {
		charmbraceletlog.Warn("hook failed", "hook", name, "task", taskID, "err", err)
	}
}

// drainCommands processes any commands queued on o.commands without blocking.
// Pause flips a flag (consumed by waitWhilePaused); skip marks a task to be
// short-circuited; retry resets a FAILED task back to PENDING.
func (o *Orchestrator) drainCommands(ctx context.Context, tasksPath string, tasks []types.Task, completed map[string]bool) {
	if o.commands == nil {
		return
	}
	for {
		select {
		case cmd, ok := <-o.commands:
			if !ok {
				o.commands = nil
				return
			}
			o.applyCommand(cmd, tasksPath, tasks, completed)
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

// waitWhilePaused blocks until a CmdResume arrives or the context cancels.
// While paused, it still applies non-pause commands as they arrive.
func (o *Orchestrator) waitWhilePaused(ctx context.Context, tasksPath string, tasks []types.Task, completed map[string]bool) error {
	if !o.isPaused() {
		return nil
	}
	o.emit(Event{Type: EventError, Message: "paused — press p to resume"})
	for o.isPaused() {
		if o.commands == nil {
			return fmt.Errorf("paused but no command channel attached")
		}
		select {
		case cmd, ok := <-o.commands:
			if !ok {
				o.commands = nil
				return nil
			}
			o.applyCommand(cmd, tasksPath, tasks, completed)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (o *Orchestrator) applyCommand(cmd Command, tasksPath string, tasks []types.Task, completed map[string]bool) {
	switch cmd.Type {
	case CmdPause:
		o.setPaused(true)
	case CmdResume:
		o.setPaused(false)
	case CmdSkip:
		if cmd.TaskID == "" {
			return
		}
		o.skip[cmd.TaskID] = true
	case CmdRetry:
		if cmd.TaskID == "" {
			return
		}
		for i := range tasks {
			if tasks[i].ID == cmd.TaskID && tasks[i].Status == types.StatusFailed {
				tasks[i].Status = types.StatusPending
				if err := o.setStatus(tasksPath, cmd.TaskID, types.StatusPending); err != nil {
					charmbraceletlog.Warn("retry: updating task status", "task", cmd.TaskID, "err", err)
				}
				delete(completed, cmd.TaskID)
				return
			}
		}
	}
}

// paused state lives on the orchestrator alongside the skip map; both are
// guarded by the orchestrator goroutine because Run is the only writer.
func (o *Orchestrator) isPaused() bool { return o.paused }
func (o *Orchestrator) setPaused(p bool) {
	o.paused = p
	if p {
		o.emit(Event{Type: EventError, Message: "paused"})
	} else {
		o.emit(Event{Type: EventError, Message: "resumed"})
	}
}
