// Package step executes one task of a run: the AI worker attempt, the review
// that judges it, the deterministic command stage, the human gate, the A/B
// model comparison, and the escalation that decides what a rejection costs.
//
// The scheduler that decides *which* task runs next — the wave loop, pause,
// skip, command draining — lives in internal/orchestrator. Everything that
// happens *inside* one task lives here. The two sides communicate through a
// shared Bookkeeper (serialised state writes) and an event Emitter.
package step

import (
	"context"
	"os/exec"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/recovery"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

// Options configures an Executor. Every collaborator is injected because the
// scheduler owns their lifetime: the hooks Runner, the recovery Manager and the
// Bookkeeper are shared with it, not duplicated per step.
type Options struct {
	Config   *config.Config
	Provider provider.Provider
	Worker   *Worker
	Reviewer *Reviewer
	Hooks    *hooks.Runner
	Recovery *recovery.Manager
	Sandbox  sandbox.Sandbox
	WorkDir  string
	// InvestigationModel is the model the spawn-investigation escalation
	// runs on (the Advisor's model, reused).
	InvestigationModel string
	// ApproveGates auto-approves recipe "human-gate" stages.
	ApproveGates bool
	// Emit publishes an event to the ledger and the UI.
	Emit event.Emitter
	// Book serialises the state writes shared with the scheduler.
	Book *Bookkeeper
}

// Executor runs a single task to a terminal outcome.
type Executor struct {
	cfg                *config.Config
	provider           provider.Provider
	worker             *Worker
	reviewer           *Reviewer
	hooks              *hooks.Runner
	recovery           *recovery.Manager
	sandbox            sandbox.Sandbox
	workDir            string
	investigationModel string
	approveGates       bool
	emit               event.Emitter
	book               *Bookkeeper
}

// NewExecutor creates an Executor from the given options.
func NewExecutor(opts Options) *Executor {
	return &Executor{
		cfg:                opts.Config,
		provider:           opts.Provider,
		worker:             opts.Worker,
		reviewer:           opts.Reviewer,
		hooks:              opts.Hooks,
		recovery:           opts.Recovery,
		sandbox:            opts.Sandbox,
		workDir:            opts.WorkDir,
		investigationModel: opts.InvestigationModel,
		approveGates:       opts.ApproveGates,
		emit:               opts.Emit,
		book:               opts.Book,
	}
}

// Run is the per-run state a single step reads and updates. The scheduler
// builds one and passes the same value to every step, so the pointers
// (Anchor, TotalCostUSD) and the maps stay shared.
type Run struct {
	TasksPath    string
	AnchorPath   string
	Anchor       *types.AnchorState
	Completed    map[string]bool
	DAG          *dag.DAG
	TotalCostUSD *float64
}

// Execute runs one task to a terminal outcome and returns nil when it passed.
// A returned error is a task-level failure unless IsFatal reports true, in
// which case the whole run must abort.
//
// Concurrency-safe: each call clones the Worker and routes every shared write
// through the Bookkeeper, so the scheduler can run a whole DAG level in
// parallel.
func (e *Executor) Execute(ctx context.Context, r *Run, t *types.Task) error {
	// Command stages run a shell step instead of an AI worker — no LLM, no
	// reviewer, no TASK-REPORT. Dispatch early so the AI path stays clean.
	if t.Kind == "command" {
		return e.runCommandStage(ctx, r, t)
	}
	if t.Kind == "human-gate" {
		return e.runHumanGate(r, t)
	}
	return e.runAITask(ctx, r, t)
}

// runShell runs a shell command in the workDir and returns combined output.
func (e *Executor) runShell(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = e.workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *Executor) runHook(ctx context.Context, name string, env hooks.HookEnv, taskID string) {
	if _, err := e.hooks.Run(ctx, name, env); err != nil {
		charmbraceletlog.Warn("hook failed", "hook", name, "task", taskID, "err", err)
	}
}
