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
	"os"
	"os/exec"
	"strings"
	"sync"
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

	// parkedMu/parkedGates count how many human gates are holding this run.
	//
	// `parked` on the record is a scalar and gates are not: with
	// execution.parallel, two steps of the same wave can be waiting at once,
	// and the first one to be decided used to write `running` while the second
	// was still blocked — which took the second gate out of `corvex gate list`
	// and out of the UI inbox while its run sat there forever, waiting for a
	// decision nobody could see was owed. Counting fixes it because the status
	// answers "is anyone waiting", not "is this gate waiting".
	parkedMu    sync.Mutex
	parkedGates int

	// baseSHA is the commit the run started from; see runBase.
	baseOnce sync.Once
	baseSHA  string
}

// enterGate reports the run as parked, and returns the function that leaves the
// gate. Only the 0→1 and 1→0 transitions touch the record: a status write per
// gate would be the same bug with more syscalls.
func (e *Executor) enterGate() func() {
	e.parkedMu.Lock()
	e.parkedGates++
	first := e.parkedGates == 1
	e.parkedMu.Unlock()
	if first {
		e.setRunStatus(run.StatusParked)
	}
	return func() {
		e.parkedMu.Lock()
		e.parkedGates--
		last := e.parkedGates == 0
		e.parkedMu.Unlock()
		if last {
			e.setRunStatus(run.StatusRunning)
		}
	}
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
		e.markGateFailure(r, t, err)
		return err
	}

	switch {
	case t.Kind == types.LegacyKindHumanGate:
		// The gate was the whole step, and it passed.
		// The gate WAS the step, so its completion is gate spend, not validate:
		// nothing deterministic ran.
		e.markStagePassed(r, t, "human gate approved: "+t.Title, 0, event.PhaseGate)
		return nil
	case types.NormalizeKind(t.Kind).IsComputational():
		return e.runComputationalStage(ctx, r, t, acc)
	default:
		return e.runAITask(ctx, r, t, acc)
	}
}

// markGateFailure records a step that a gate refused before or after its work.
// Task-level, so the scheduler's existing cascade skips only what depended on
// it and the independent branches of the DAG keep running.
func (e *Executor) markGateFailure(r *Run, t *types.Task, cause error) {
	if err := e.book.SetStatus(r.TasksPath, t.ID, types.StatusFailed); err != nil {
		charmbraceletlog.Warn("updating gated task status to failed", "task", t.ID, "err", err)
	}
	// The step's terminal line says WHY, because it is the last line a reader
	// sees and `refused by gate` sends them looking for a sentence that used to
	// live only in a terminal somebody has since closed.
	message := "refused by gate"
	if cause != nil {
		message = cause.Error()
	}
	e.emit(event.Event{Type: event.TaskComplete, TaskID: t.ID, Phase: event.PhaseGate, Status: types.StatusFailed, Message: message})
}

// runShell runs a shell command in the workDir and returns combined output.
//
// Every shell a recipe can reach goes through here — a stage's `command:`, a
// loop's `until:`, a computational gate's check, an evidence item's `from:` —
// which is why the run's environment is assembled in one place: a variable a
// stage could read and its gate could not would be worse than no variable,
// because the recipe would look correct and derive two different answers.
// taskDir is where a task executes: its own worktree when it is an isolated
// fan-out item, and the run's checkout for everything else.
//
// One function, so the four places that need it — the agent's cwd, a command
// stage's shell, a gate's shell, an evidence command — cannot disagree about
// which tree a step belongs to. A step whose evidence came from a different
// checkout than its work would be evidence about somebody else's tree.
func (e *Executor) taskDir(t *types.Task) string {
	if t != nil && t.WorkDir != "" {
		return t.WorkDir
	}
	return e.workDir
}

// checkpointer is the recovery manager for the tree a task writes into.
//
// auto_commit commits what the step produced, and an isolated fan-out item
// produces it on its own branch in its own worktree. Committing that from the
// run's checkout would commit NOTHING (the tree is untouched there) while the
// item's work sat uncommitted — and the merge back would then have nothing to
// merge. The run's own manager is reused when the task has no tree of its own,
// so the common path keeps its memoised state.
func (e *Executor) checkpointer(t *types.Task) *recovery.Manager {
	dir := e.taskDir(t)
	if dir == e.workDir {
		return e.recovery
	}
	return recovery.NewManager(dir)
}

func (e *Executor) runShell(ctx context.Context, r *Run, command string) (string, error) {
	return e.runShellIn(ctx, r, e.workDir, command)
}

// runShellForTask runs a command in the task's own checkout.
func (e *Executor) runShellForTask(ctx context.Context, r *Run, t *types.Task, command string) (string, error) {
	return e.runShellIn(ctx, r, e.taskDir(t), command)
}

// runShellForStep is runShellForTask plus the reply to this step's question
// gate, exported as $CORVEX_GATE_ANSWER.
//
// It exists because a question that the work cannot read is a note. The type's
// contract says approving a question means "continue, and here is the answer";
// measured before this existed, a recipe asked "which branch should receive this
// PR?", a person answered `release/1.8.0`, and the step then ran exactly the
// command it would have run without asking.
//
// Empty when the step had no question gate, which is every step that did not ask
// — so a recipe reading `${CORVEX_GATE_ANSWER:-}` gets the honest thing.
func (e *Executor) runShellForStep(ctx context.Context, r *Run, t *types.Task, acc *evidenceSet, command string) (string, error) {
	return e.runShellIn(ctx, r, e.taskDir(t), command, "CORVEX_GATE_ANSWER="+acc.gateAnswer())
}

func (e *Executor) runShellIn(ctx context.Context, r *Run, dir, command string, extraEnv ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"CORVEX_RUN_BASE="+e.runBase(ctx),
		"CORVEX_RUN_ID="+r.runID(),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runID is this run's own name, exported to every shell as $CORVEX_RUN_ID.
//
// It exists because $CORVEX_RUN_BASE — a commit — is not an identity. A recipe
// that has to carry state from one step to the next (the id of a work item
// created in S02 and read in S03) has nowhere to put it, so it derives a
// directory from the run's INPUTS instead, and two concurrent runs with the
// same inputs then share it: one run's S02 overwrites the other's state and its
// S03 walks the wrong item to the end. Derived-from-inputs is not a bad habit,
// it is the only thing a recipe author could do, because the runner knew the id
// and never said it.
//
// # The name
//
// `CORVEX_RUN_ID`, because the CLI already calls it that: `corvex run list`
// prints these ids, `corvex run show <id>` and `corvex gate approve <id>` take
// one. A recipe author who has used the CLI needs no second vocabulary, and the
// CORVEX_RUN_* prefix pairs it with CORVEX_RUN_BASE — both answer "what run is
// this", one by name and one by anchor.
//
// The prefix is shared with two knobs corvex READS from the operator
// ($CORVEX_RUN_RETENTION, $CORVEX_RUN_INDEX_MAX_BYTES), which is a wrinkle and
// not a collision: measured, this name has no reader anywhere in the tree, so a
// stage that invokes corvex recursively inherits it and nothing acts on it.
//
// # What is NOT exported
//
// Only the id. Not the repository path, not the project, not the recipe name:
// the id is already unique per run, so each extra variable would be a fact
// about the machine leaking into whatever the recipe writes with it — paths,
// logs, commit messages, a comment on someone's tracker. Nothing here needs
// them; when something does, it can argue for itself.
//
// # Same value on both sides
//
// The id is read off Run.Identity, the same field the gate file is keyed by, so
// a stage and its gate cannot disagree — they are one read of one struct, not
// two derivations. That is load-bearing for the pattern this exists to fix,
// where the gate re-runs the stage's own call in dry-run mode and has to land
// on the same state directory.
//
// A run with no identity yields the empty string rather than an error, matching
// runBase: a step must degrade to less information, never to a refused step.
// Real runs always have one — ops refuses to start a run it cannot register.
func (r *Run) runID() string {
	if r == nil {
		return ""
	}
	return r.Identity.RunID
}

// runBase is the commit this run started from, memoised for its lifetime.
//
// It exists because of a dogfood run whose gate required reading a diff that
// did not contain the change being approved. The recipe said
// `from: "git show --stat HEAD"`, which is the obvious thing to write — but
// auto_commit checkpoints EVERY step, so by the time a later step's gate opens,
// HEAD is a bookkeeping commit and the work is one or more commits back. The
// approver was asked to acknowledge reading a diff of corvex's own paperwork.
//
// That is the roadmap's risk #1 ("gate que vira carimbo") arriving through a
// mechanism nobody predicted: not laziness, but evidence whose anchor drifts
// under it. `$CORVEX_RUN_BASE` is an anchor that does not move, so a recipe can
// say `git diff --stat $CORVEX_RUN_BASE -- README.md` and mean "everything this
// run changed".
//
// A repository with no commits (or no git at all) yields an empty string rather
// than an error: evidence must degrade to less information, never to a refused
// step.
func (e *Executor) runBase(ctx context.Context) string {
	e.baseOnce.Do(func() {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = e.workDir
		out, err := cmd.Output()
		if err != nil {
			return
		}
		e.baseSHA = strings.TrimSpace(string(out))
	})
	return e.baseSHA
}

func (e *Executor) runHook(ctx context.Context, name string, env hooks.HookEnv, taskID string) {
	if _, err := e.hooks.Run(ctx, name, env); err != nil {
		charmbraceletlog.Warn("hook failed", "hook", name, "task", taskID, "err", err)
	}
}
