package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/planning"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/recovery"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/step"
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

// Orchestrator schedules a run: it plans, resolves the DAG, walks it wave by
// wave and publishes events. Each individual task is handed to the step
// executor.
type Orchestrator struct {
	// opts is the caller's configuration, read directly rather than mirrored
	// into fields (NoReplan, Force and ApproveGates used to be copies).
	opts       Options
	cfg        *config.Config
	hooks      *hooks.Runner
	recovery   *recovery.Manager
	planner    *planning.Planner
	advisor    *planning.Advisor
	exec       *step.Executor
	sandbox    sandbox.Sandbox
	events     chan<- Event
	workDir    string
	targetTask string
	singleTask bool
	abModels   []string
	commands   <-chan Command
	skip       map[string]bool // task IDs skipped by the user at runtime
	paused     bool            // toggled by Cmd{Pause,Resume}
	ledger     *activity.Ledger
	runCtx     context.Context // set at the start of Run; lets emit() abort on cancel
	// book guards the state shared with the step executor (completed set,
	// cumulative cost, anchorState) and serialises tasks.md/anchor.yaml
	// writes when tasks run in parallel.
	book step.Bookkeeper
	// emitMu serialises ledger appends so parallel tasks don't interleave
	// bytes in activity.jsonl. The events channel send is already goroutine-safe.
	emitMu sync.Mutex
}

// New creates an Orchestrator from the given options.
func New(opts Options) *Orchestrator {
	o := &Orchestrator{
		opts:       opts,
		cfg:        opts.Config,
		hooks:      hooks.NewRunner(opts.WorkDir, 0),
		recovery:   recovery.NewManager(opts.WorkDir),
		planner:    planning.NewPlanner(opts.Provider, opts.Config.Provider.Models.Planner, opts.WorkDir, opts.Config.AgentRouting, opts.Config.Plan.ContextCommand),
		advisor:    planning.NewAdvisor(opts.Provider, opts.Config.Provider.Models.Planner, opts.WorkDir),
		sandbox:    opts.Sandbox,
		events:     opts.Events,
		workDir:    opts.WorkDir,
		targetTask: opts.TargetTask,
		singleTask: opts.SingleTask,
		abModels:   opts.ABModels,
		commands:   opts.Commands,
		skip:       make(map[string]bool),
	}
	o.exec = step.NewExecutor(step.Options{
		Config:             opts.Config,
		Provider:           opts.Provider,
		Worker:             step.NewWorker(opts.Provider, opts.Config.Provider.Models.Worker, opts.WorkDir, opts.Sandbox, opts.Config.SkillRouting),
		Reviewer:           step.NewReviewer(opts.Provider, opts.Config.Provider.Models.Reviewer, opts.WorkDir, opts.Config.SkillRouting["review"]),
		Hooks:              o.hooks,
		Recovery:           o.recovery,
		Sandbox:            opts.Sandbox,
		WorkDir:            opts.WorkDir,
		InvestigationModel: o.advisor.Model(),
		ApproveGates:       opts.ApproveGates,
		Emit:               o.emit,
		Book:               &o.book,
	})
	return o
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

// openLedger opens the activity ledger early so every emitted event gets
// persisted. Failure to open (e.g. project not yet planned) is non-fatal —
// emit() no-ops when ledger is nil.
func (o *Orchestrator) openLedger(project string) {
	if l, lerr := activity.New(o.workDir, project); lerr == nil {
		o.ledger = l
	} else {
		charmbraceletlog.Warn("activity ledger unavailable", "err", lerr)
	}
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
