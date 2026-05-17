package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

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
		skip:       make(map[string]bool),
	}
}

// Run executes the full orchestration loop for the given project.
func (o *Orchestrator) Run(ctx context.Context, project string) error {
	specPath, tasksPath, anchorPath := o.projectPaths(project)

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
	recResult, err := o.recovery.Check()
	if err != nil {
		charmbraceletlog.Warn("recovery check failed", "err", err)
	}
	if recResult != nil {
		o.emit(Event{Type: EventRecoveryResult, Message: recResult.Message})
	}

	anchorState, err := anchor.Load(anchorPath)
	if err != nil {
		charmbraceletlog.Warn("loading anchor", "err", err)
	}

	plan, planErr := o.needsPlanning(specPath, tasksPath, anchorState)
	if planErr != nil {
		return fmt.Errorf("checking planning needs: %w", planErr)
	}
	if plan {
		o.emit(Event{Type: EventPlanStart})
		if err := o.planner.Plan(ctx, specPath, anchorPath, tasksPath); err != nil {
			return fmt.Errorf("planning: %w", err)
		}
		o.emit(Event{Type: EventPlanComplete})
	}

	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}

	d := dag.NewDAG(tasks)
	if err := d.Validate(); err != nil {
		return fmt.Errorf("validating DAG: %w", err)
	}
	o.emit(Event{Type: EventDAGResolved, Total: d.Size()})

	completed := make(map[string]bool)
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			completed[t.ID] = true
		}
	}

	for {
		ready := d.NextReady(completed)
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

		for _, taskID := range ready {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			o.drainCommands(ctx, tasksPath, tasks, completed)
			if err := o.waitWhilePaused(ctx, tasksPath, tasks, completed); err != nil {
				return err
			}

			if o.skip[taskID] {
				if err := task.UpdateTaskStatus(tasksPath, taskID, types.StatusSkipped); err != nil {
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
				if err := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusPassed); err != nil {
					charmbraceletlog.Warn("updating task status to passed after a/b", "task", t.ID, "err", err)
				}
			} else if err := o.executeTask(ctx, t, tasksPath, anchorPath, &anchorState, completed, d); err != nil {
				return err
			}
		}

		if o.targetTask != "" || o.singleTask {
			break
		}
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
) error {
	maxRetries := o.cfg.Execution.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	diagnosis := ""
	categoryCounts := make(map[string]int)
	originalWorkerModel := o.worker.model
	defer func() { o.worker.model = originalWorkerModel }()

	for attempt := 0; attempt <= maxRetries; attempt++ {
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

		if err := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusRunning); err != nil {
			charmbraceletlog.Warn("updating task status to running", "task", t.ID, "err", err)
		}
		o.emit(Event{Type: EventTaskStart, TaskID: t.ID, Attempt: attempt})

		contextDocs := loadContextDocs(o.workDir, o.cfg.Context.AlwaysInclude)
		agentPrompt := loadAgentPrompt(o.workDir, o.cfg.AgentRouting, t.Type)
		anchorCtx := anchor.GenerateContext(*anchorState, t.ID)

		result, err := o.worker.Execute(ctx, t, anchorCtx, contextDocs, agentPrompt, diagnosis)
		if err != nil {
			if attempt == maxRetries {
				if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
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
		if reviewErr != nil {
			if attempt == maxRetries {
				if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
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
			hookEnv.Status = "passed"
			o.runHook(ctx, hooks.OnSuccess, hookEnv, t.ID)
			o.runHook(ctx, hooks.PostTask, hookEnv, t.ID)

			// D8: git commit FIRST, then update tasks.md and anchor.
			// Crash before commit = retry from clean state.
			// Crash after commit = consistent state, just re-read statuses.
			if o.cfg.Execution.AutoCommit {
				if err := o.recovery.MarkCheckpoint(t.ID); err != nil {
					charmbraceletlog.Warn("marking checkpoint", "task", t.ID, "err", err)
				}
			}

			if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusPassed); statusErr != nil {
				charmbraceletlog.Warn("updating task status to passed", "task", t.ID, "err", statusErr)
			}

			nextCompleted := make(map[string]bool, len(completed)+1)
			for k, v := range completed {
				nextCompleted[k] = v
			}
			nextCompleted[t.ID] = true

			nextReady := d.NextReady(nextCompleted)
			nextTask := ""
			if len(nextReady) > 0 {
				nextTask = nextReady[0]
			}

			*anchorState = anchor.Update(*anchorState, anchor.TaskResult{
				Completed: types.CompletedTask{
					ID:            t.ID,
					Title:         t.Title,
					Summary:       reviewResult.Summary,
					FilesCreated:  t.Files.Create,
					FilesModified: t.Files.Modify,
				},
				NextTask:   nextTask,
				TotalTasks: d.Size(),
			})
			if err := anchor.Save(anchorPath, *anchorState); err != nil {
				charmbraceletlog.Warn("saving anchor", "task", t.ID, "err", err)
			}

			completed[t.ID] = true
			o.emit(Event{Type: EventCheckpoint, TaskID: t.ID})
			o.emit(Event{
				Type:       EventTaskComplete,
				TaskID:     t.ID,
				Status:     types.StatusPassed,
				CostUSD:    result.CostUSD + reviewResult.CostUSD,
				TokensIn:   result.TokensIn + reviewResult.TokensIn,
				TokensOut:  result.TokensOut + reviewResult.TokensOut,
				DurationMs: result.DurationMs + reviewResult.DurationMs,
			})
			return nil
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
				if decision.UpgradeTo != "" && decision.UpgradeTo != o.worker.model {
					charmbraceletlog.Info("escalation: upgrading worker model",
						"task", t.ID, "category", cat, "from", o.worker.model, "to", decision.UpgradeTo)
					o.worker.model = decision.UpgradeTo
				}
			case ActionHumanPrompt:
				path, err := writeHumanEscalation(o.workDir, o.cfg.Project.Name, t.ID, cat, reviewResult.Summary)
				if err != nil {
					charmbraceletlog.Warn("writing human escalation", "task", t.ID, "err", err)
				} else {
					charmbraceletlog.Warn("escalation: human review requested",
						"task", t.ID, "category", cat, "file", path)
				}
				if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
					charmbraceletlog.Warn("updating task status to failed after escalation", "task", t.ID, "err", statusErr)
				}
				return fmt.Errorf("task %s escalated to human review (category %s); see %s", t.ID, cat, path)
			case ActionSpawnInvestigation:
				// Not yet implemented: emits a warning and falls through to
				// the standard retry. The diagnosis already carries the
				// reviewer summary so the next attempt has the context it
				// needs.
				charmbraceletlog.Warn("escalation: spawn-investigation is not yet implemented; falling back to retry",
					"task", t.ID, "category", cat)
			}
		}
	}

	if statusErr := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusFailed); statusErr != nil {
		charmbraceletlog.Warn("updating task status to failed", "task", t.ID, "err", statusErr)
	}
	return fmt.Errorf("task %s failed review after %d attempts", t.ID, maxRetries+1)
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
	if o.events == nil {
		return
	}
	ev.Timestamp = time.Now()
	select {
	case o.events <- ev:
	default:
	}
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
				if err := task.UpdateTaskStatus(tasksPath, cmd.TaskID, types.StatusPending); err != nil {
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
