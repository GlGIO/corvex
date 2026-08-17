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
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/recovery"
	"github.com/giovannialves/corvex/internal/run"
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
	// ApproveGates auto-approves human gates. It is the CI path: without it a
	// pipeline that reaches a gate would block until it timed out.
	ApproveGates bool
	// Emit publishes an event to the ledger and the UI.
	Emit event.Emitter
	// Book serialises the state writes shared with the scheduler.
	Book *Bookkeeper

	// SetRunStatus reports the run's own state (parked while a human gate
	// holds, running again afterwards). Injected rather than reached for,
	// because the run record is owned by ops and this package must not learn
	// how identity is registered. Nil disables the reporting.
	SetRunStatus func(run.Status) error
	// GatePoll overrides how often a parked run re-reads its gate file.
	// 0 uses DefaultGatePoll.
	GatePoll time.Duration
	// Now and Branch are injected clocks and git access, so gate expiry and
	// branch_not are testable without a clock or a repository.
	Now    func() time.Time
	Branch func(context.Context) (string, error)
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
	setStatus          func(run.Status) error
	gatePoll           time.Duration
	nowFn              func() time.Time
	branchFn           func(context.Context) (string, error)
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
		setStatus:          opts.SetRunStatus,
		gatePoll:           opts.GatePoll,
		nowFn:              opts.Now,
		branchFn:           opts.Branch,
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
	// Identity addresses this run on disk. A human gate needs it because the
	// gate file is keyed by (repo, run id, step id) — that tuple is the whole
	// cross-process contract, and a run that cannot name itself cannot open a
	// gate anybody could answer.
	Identity RunIdentity
}

// RunIdentity is the run's on-disk address, handed down from ops.
type RunIdentity struct {
	RunID   string
	Repo    string
	Project string
	Recipe  string
}

// Execute runs one task to a terminal outcome and returns nil when it passed.
// A returned error is a task-level failure unless IsFatal reports true, in
// which case the whole run must abort.
//
// Concurrency-safe: each call clones the Worker and routes every shared write
// through the Bookkeeper, so the scheduler can run a whole DAG level in
// parallel.
func (e *Executor) Execute(ctx context.Context, r *Run, t *types.Task) error {
	acc := newEvidenceSet()

	// Before-gates guard the action: they decide whether the work happens at
	// all. A human consenting to a migration and a policy refusing a merge into
	// main both belong here, and the difference from an after-gate is whether
	// the side effect already exists when you say no.
	if err := e.runGates(ctx, r, t, types.GateBefore, acc); err != nil {
		e.markGateFailure(r, t)
		return err
	}

	switch {
	case t.Kind == types.LegacyKindHumanGate:
		// The gate was the whole step, and it passed.
		e.markStagePassed(r, t, "human gate approved: "+t.Title, 0)
		return nil
	case types.NormalizeKind(t.Kind).IsComputational():
		return e.runComputationalStage(ctx, r, t, acc)
	default:
		return e.runAITaskGated(ctx, r, t, acc)
	}
}

// markGateFailure records a step that a gate refused before or after its work.
// Task-level, so the scheduler's existing cascade skips only what depended on
// it and the independent branches of the DAG keep running.
func (e *Executor) markGateFailure(r *Run, t *types.Task) {
	if err := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); err != nil {
		charmbraceletlog.Warn("updating gated task status to failed", "task", t.ID, "err", err)
	}
	e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Status: types.StatusFailed, Message: "refused by gate"})
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
