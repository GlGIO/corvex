package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/recovery"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// Run executes the full orchestration loop for the given project.
func (o *Orchestrator) Run(ctx context.Context, project string) error {
	o.runCtx = ctx
	specPath, tasksPath, anchorPath := o.projectPaths(project)

	o.openLedger(project)

	teardownSandbox, err := o.prepareSandbox(ctx)
	if err != nil {
		return err
	}
	defer teardownSandbox()

	if err := o.guardWorkingTree(); err != nil {
		return err
	}

	// Expose repo-local skills (.corvex/skills/*) to the Worker via
	// .claude/skills/ for this run; cleaned up when Run returns.
	defer o.materializeSkills()()

	anchorState, err := anchor.Load(anchorPath)
	if err != nil {
		charmbraceletlog.Warn("loading anchor", "err", err)
	}

	if err := o.planIfNeeded(ctx, project, specPath, tasksPath, anchorPath, anchorState); err != nil {
		return err
	}

	tasks, err := loadTasks(tasksPath, project)
	if err != nil {
		return err
	}

	d := dag.NewDAG(tasks)
	if err := d.Validate(); err != nil {
		return fmt.Errorf("validating DAG: %w", err)
	}
	o.emit(Event{Type: EventDAGResolved, Total: d.Size()})

	o.resetInterruptedTasks(tasksPath, tasks)
	o.resetAlwaysTasks(tasksPath, tasks)

	completed := passedTasks(tasks)
	if err := checkDAGIntegrity(tasks, completed); err != nil {
		return err
	}

	s := newSchedule(tasks, completed, d, tasksPath, anchorPath, &anchorState, o.runIdentity(project))
	s.generatedBy = generatedBy(tasksPath)
	if err := o.walkDAG(ctx, s); err != nil {
		return err
	}
	return o.finishRun(ctx, s)
}

// prepareSandbox brings the sandbox up (when one is configured) and returns the
// teardown the caller must defer. The teardown is a no-op without a sandbox.
func (o *Orchestrator) prepareSandbox(ctx context.Context) (func(), error) {
	if o.sandbox == nil {
		return func() {}, nil
	}
	o.emit(Event{Type: EventSandboxPrepare})
	if err := o.sandbox.Prepare(ctx); err != nil {
		return nil, fmt.Errorf("preparing sandbox: %w", err)
	}
	return func() {
		o.emit(Event{Type: EventSandboxCleanup})
		if cleanupErr := o.sandbox.Cleanup(context.Background()); cleanupErr != nil {
			charmbraceletlog.Warn("sandbox cleanup", "err", cleanupErr)
		}
	}, nil
}

// guardWorkingTree refuses to start on a dirty tree unless --force was passed,
// in which case the uncommitted changes are discarded.
func (o *Orchestrator) guardWorkingTree() error {
	o.emit(Event{Type: EventRecoveryCheck})
	recResult, err := o.recovery.Guard()
	if err != nil {
		charmbraceletlog.Warn("recovery guard failed", "err", err)
	}
	if recResult == nil {
		return nil
	}
	if recResult.Action != recovery.AbortDirty {
		o.emit(Event{Type: EventRecoveryResult, Message: recResult.Message})
		return nil
	}

	if o.opts.Force {
		// User opted in via --force: discard the dirty tree.
		if _, derr := o.recovery.Check(); derr != nil {
			return fmt.Errorf("--force: discarding dirty tree: %w", derr)
		}
		o.emit(Event{Type: EventRecoveryResult, Message: fmt.Sprintf("--force: discarded %d uncommitted change(s)", len(recResult.DirtyFiles))})
		return nil
	}

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

// planIfNeeded regenerates tasks.md when there is no plan yet, or when spec.md
// has drifted from the hash recorded in the anchor. A drift-triggered replan
// backs up the existing tasks.md first, and --no-replan refuses outright so
// manual edits survive.
func (o *Orchestrator) planIfNeeded(ctx context.Context, project, specPath, tasksPath, anchorPath string, anchorState types.AnchorState) error {
	plan, planErr := o.needsPlanning(specPath, tasksPath, anchorState)
	if planErr != nil {
		return fmt.Errorf("checking planning needs: %w", planErr)
	}
	if !plan {
		return nil
	}

	// If tasks.md already exists, this is a replan triggered by spec drift.
	// Honor --no-replan and refuse, or back up the existing file before the
	// planner overwrites it.
	_, tasksExist := os.Stat(tasksPath)
	isReplan := tasksExist == nil
	if isReplan && o.opts.NoReplan {
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
	return nil
}

// loadTasks parses tasks.md. A file that parses to zero tasks must never be
// reported as a successful empty run: it means planning failed to produce
// structured output (e.g. the model narrated instead of emitting the file).
// Fail loudly with a path so the user can inspect and re-plan.
func loadTasks(tasksPath, project string) ([]types.Task, error) {
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return nil, fmt.Errorf("parsing tasks: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks to run: %s parsed to zero tasks (planning may have produced prose instead of a tasks.md). Inspect the file and re-run `corvex plan %s`", tasksPath, project)
	}
	return tasks, nil
}

// resetInterruptedTasks rescues tasks left in RUNNING by a previous interrupted
// run. They would otherwise stay in limbo: not in `completed` (so their
// dependents block) but never re-picked by the scheduler either. Reset to
// PENDING so the dependency chain can resume.
func (o *Orchestrator) resetInterruptedTasks(tasksPath string, tasks []types.Task) {
	for i := range tasks {
		if tasks[i].Status != types.StatusRunning {
			continue
		}
		charmbraceletlog.Warn("task left RUNNING from a previous run; resetting to PENDING for re-execution", "task", tasks[i].ID)
		tasks[i].Status = types.StatusPending
		if err := o.book.SetStatus(tasksPath, tasks[i].ID, types.StatusPending); err != nil {
			charmbraceletlog.Warn("persisting RUNNING→PENDING reset", "task", tasks[i].ID, "err", err)
		}
	}
}

// resetAlwaysTasks puts every `always` step — and everything downstream of it —
// back to PENDING before the run starts.
//
// The cascade is the half that is easy to forget and impossible to live without:
// a probe that re-queries production while the human gate BELOW it stays PASSED
// would re-observe and then skip the screen that shows the observation. The
// gate's verdict was about yesterday's number.
//
// Measured on the second run of a probe recipe, before this existed: the query
// never ran, and the run went straight to a gate announcing that the probe had
// passed. A probe that reports success without probing is worse than no probe,
// because the hole looks covered.
func (o *Orchestrator) resetAlwaysTasks(tasksPath string, tasks []types.Task) {
	stale := make(map[string]bool)
	for _, t := range tasks {
		if t.Always {
			stale[t.ID] = true
		}
	}
	if len(stale) == 0 {
		return
	}
	// Downstream closure. The list is in dependency order already (the compiler
	// emits it that way), but a second pass costs nothing and does not rely on
	// that being true forever.
	for again := true; again; {
		again = false
		for _, t := range tasks {
			if stale[t.ID] {
				continue
			}
			for _, dep := range t.DependsOn {
				if stale[dep] {
					stale[t.ID] = true
					again = true
					break
				}
			}
		}
	}
	for i := range tasks {
		if !stale[tasks[i].ID] || tasks[i].Status == types.StatusPending {
			continue
		}
		charmbraceletlog.Info("re-executing an `always` step and what depends on it", "task", tasks[i].ID, "was", tasks[i].Status)
		tasks[i].Status = types.StatusPending
		if err := o.book.SetStatus(tasksPath, tasks[i].ID, types.StatusPending); err != nil {
			charmbraceletlog.Warn("persisting the always reset", "task", tasks[i].ID, "err", err)
		}
	}
}

// passedTasks seeds the completed set from the statuses already on disk.
func passedTasks(tasks []types.Task) map[string]bool {
	completed := make(map[string]bool)
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			completed[t.ID] = true
		}
	}
	return completed
}

// checkDAGIntegrity verifies that every PASSED task has all its dependencies
// also PASSED. Replans can change the DAG and leave previously-passed tasks
// with stale (now-unsatisfied) deps; running their dependents would compound
// the corruption invisibly.
func checkDAGIntegrity(tasks []types.Task, completed map[string]bool) error {
	for _, t := range tasks {
		if t.Status != types.StatusPassed {
			continue
		}
		for _, dep := range t.DependsOn {
			if completed[dep] {
				continue
			}
			return fmt.Errorf(
				"DAG integrity violation: task %s is PASSED but its dependency %s is not.\n"+
					"This usually means a replan changed the DAG. To fix, choose one:\n"+
					"  1) Reset %s to ⬜ PENDING in tasks.md (delete its artifacts first if they were written), so it re-validates under the new DAG.\n"+
					"  2) Manually run %s by marking it PENDING and re-running, then retry.",
				t.ID, dep, t.ID, dep,
			)
		}
	}
	return nil
}

// finishRun closes the run out: a partial failure summary, the Advisor's
// insights, the post-run hook and the terminal Done event.
func (o *Orchestrator) finishRun(ctx context.Context, s *schedule) error {
	if len(s.failed) > 0 {
		msg := fmt.Sprintf("run completed with failures: %d task(s) failed (%s)",
			len(s.failed), strings.Join(s.failed, ", "))
		if len(s.skipped) > 0 {
			msg += fmt.Sprintf("; %d dependent task(s) skipped (%s)",
				len(s.skipped), strings.Join(s.skipped, ", "))
		}
		// Preserve each task's specific failure reason so the summary stays
		// diagnosable (e.g. "reviewer never produced a verdict").
		msg += "\n  - " + strings.Join(s.failures, "\n  - ")
		o.runHook(ctx, hooks.PostRun, hooks.HookEnv{Project: o.cfg.Project.Name, Status: "partial"}, "")
		return fmt.Errorf("%s", msg)
	}

	if threshold := o.cfg.Execution.InsightThreshold; threshold != 0 && o.targetTask == "" && !o.singleTask {
		insights, err := o.advisor.Analyze(ctx, s.tasks, o.cfg.AgentRouting, threshold)
		if err != nil {
			charmbraceletlog.Warn("advisor analysis failed", "err", err)
		}
		for i := range insights {
			o.emit(Event{Type: EventInsight, Insight: &insights[i]})
		}
	}

	o.runHook(ctx, hooks.PostRun, hooks.HookEnv{Project: o.cfg.Project.Name, Status: "passed"}, "")
	o.emit(Event{Type: EventDone})
	return nil
}
