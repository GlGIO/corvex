package ops

// Run identity, from birth to death.
//
// This is the only place in the product where a run id is minted, and the only
// place where a run's terminal status is written. Both halves live in ops rather
// than in cmd/ because the HTTP surface (F7) has to get the same lifecycle for
// free: a run started from the UI must appear in the same index, refresh the
// same heartbeat and close out the same way as one started from a terminal.
//
// Nothing here is a global. The id travels as a field of Runner and as an
// activity.Identity value handed to the orchestrator — the F0 clean-up removed a
// package-level allowlist for exactly this reason, and a package-level "current
// run" would be the same mistake with a worse blast radius (two runs in one
// process, which `corvex ui` will do).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// Runner is one assembled run: the scheduler, plus the identity that outlives
// the process that started it.
type Runner struct {
	// Orchestrator is the scheduler this run drives. Exported because the TUI
	// has to hand it to bubbletea; the identity lifecycle is not the caller's
	// business (see Execute).
	Orchestrator *orchestrator.Orchestrator

	// Project is the single source of truth for which project runs. Callers
	// should pass this to Orchestrator.Run rather than their own copy, so the
	// recorded project and the executed project cannot drift apart.
	Project string

	// RunID identifies this run everywhere it is observable: every ledger line,
	// the record in `<repo>/.corvex/runs/`, and the line in the global index.
	// Never empty: a Runner exists only if its identity was registered.
	RunID string

	// Repo is the absolute git root the run was recorded against.
	Repo string

	// Recipe is the workflow this run executes, or "" when there is none.
	Recipe string

	// StatusErr is why the terminal status could not be written, or nil, plus any
	// non-fatal problem recording state along the way (a failing heartbeat, index
	// housekeeping that could not run). Reported, never allowed to change the
	// run's own outcome — unlike registration, which is a precondition.
	//
	// Registration used to be in this category too, reported through an
	// IdentityErr field, on the argument that a read-only home must not refuse
	// work the user is about to pay for. The F1 audit measured what that buys: at
	// 94% id-space occupancy, 13 of 20 runs quietly reverted to writing ledger
	// lines with no run_id — the exact shape F1 exists to eliminate, appearing
	// without a word at the moment identity matters most. A refused run costs one
	// error message; an unattributable ledger costs the premise of the phase.
	StatusErr error

	// Environment is what this run needed standing before its first step
	// (F6). `simple` is the default and costs nothing; `stack` brings up the
	// `validate:` stack and takes it down again.
	Environment Environment

	handle   *run.Handle
	interval time.Duration
	env      *runEnvironment
	stackUp  StackUpFn
}

// Identity is what the ledger stamps on every line it writes for this run.
//
// r.Repo is deliberately NOT handed over. The ledger is the one corvex artifact
// that reaches the user's git history, and r.Repo is an absolute path of this
// machine; it stays in the record and the global index, neither of which is ever
// committed, and the run id on the line is what joins a ledger line back to them.
// See activity.Identity for the full argument.
//
// Recipe stays empty on the legacy `corvex run <project>` path: there is no
// recipe, and inventing a name for one (the project's name, or a sentinel like
// "implicit") would put a value in the field that no `recipe show` could ever
// resolve. Absence already means "no recipe", omitempty makes it free, and
// `recipe == ""` is precisely the discriminator this field exists to give. The
// project is not lost: it is its own field on the record.
//
// When a recipe *did* produce the run, F2 fills it in — and does so without
// inventing storage. `recipe.Compile` already stamps the tasks.md frontmatter
// with `generated_by: corvex-recipe:<name>`, so the name is on disk already and
// recipeFromTasks reads it back. A field that is derived from an existing fact
// cannot drift away from it.
func (r *Runner) Identity() activity.Identity {
	if r == nil {
		return activity.Identity{}
	}
	return activity.Identity{RunID: r.RunID, Recipe: r.Recipe}
}

// Execute runs body under this run's identity.
//
// The heartbeat is up for the whole of body and the terminal status is written
// on the way out — on success, on failure, and on a SIGINT that cancelled ctx.
// That last case is the point: a run stopped with Ctrl-C must not stay `running`
// on disk forever. A run stopped with SIGKILL never reaches here at all, which
// is what the pid + heartbeat probe in internal/run exists to cover; this is the
// graceful half, and the graceful half has to close explicitly.
//
// Ordering is deliberate:
//
//   - the cancellation watcher is stopped (and waited for) BEFORE the terminal
//     status is written, so a `canceling` write can never land on top of it;
//   - the heartbeat is stopped BEFORE the status is written, so no beat lands
//     after `done` and refreshes updated_at on a finished run;
//   - Stop waits for its goroutine, so nothing outlives Execute — and it only
//     ever waits for one select to notice a closed channel, so it cannot hold
//     the process open;
//   - the deferred Stop is the panic path: without it a panicking body would
//     leave a live heartbeat behind and a record that says `running` for a
//     process that is unwinding.
func (r *Runner) Execute(ctx context.Context, body func(context.Context) error) error {
	if r == nil {
		return fmt.Errorf("ops: Execute on a nil runner")
	}
	if body == nil {
		return fmt.Errorf("ops: Execute needs a body")
	}
	if r.handle == nil {
		// Only reachable for a Runner assembled by hand (a test): NewRunner never
		// produces one without a handle.
		return body(ctx)
	}

	hb := r.handle.StartHeartbeat(r.interval)
	// A stop request has to reach disk when it ARRIVES, not when body finally
	// unwinds — see recordCancellation.
	stopWatching := r.recordCancellation(ctx)
	recorded := false
	defer func() {
		// The environment comes down on every path, panic included: a container
		// left running outlives the process that started it and is the one piece
		// of a failed run that keeps costing something.
		r.env.teardown()
		hb.Stop()
		_ = stopWatching()
		if !recorded {
			_ = r.handle.SetStatus(run.StatusFailed)
		}
		// The pause control file is scratch that outlives its run if nobody
		// removes it, and a run id is recyclable — so a run that ended while
		// paused would hand the next run wearing that id a stop order written
		// for somebody else. Cleared on every path, panic included, for the same
		// reason the environment is. claimID clears it again on the way in,
		// because this path is not reached at all under SIGKILL.
		r.clearPauseControl()
	}()

	// The environment is brought up here rather than in NewRunner because
	// assembling a run must not start containers: `--dry-run`, a rejected flag
	// or a missing project all pass through NewRunner, and none of them should
	// leave a database behind. Failure to bring it up ends the run before a
	// single token is spent, and the record says `failed` with the reason —
	// running a suite that needs Postgres without Postgres blames the tests.
	if envErr := r.env.up(ctx, r.stackUp); envErr != nil {
		return envErr
	}

	err := body(ctx)

	// Down before the terminal status is written, so a reader that sees `done`
	// is not still holding a container.
	r.env.teardown()
	hb.Stop()
	// Before the terminal write, and it waits for the watcher: that ordering is
	// what guarantees a `canceling` write can never land on top of `canceled`.
	// It is a separate statement for the same reason — as an argument below it
	// would be evaluated after the SetStatus call, not before it.
	cancelErr := stopWatching()
	r.StatusErr = firstErr(
		r.handle.SetStatus(r.finalStatus(ctx, err)),
		cancelErr,
		hb.LastErr(),
		r.handle.MaintenanceErr(),
	)
	recorded = true
	return err
}

// firstErr is "report the most important problem, keep the rest quiet": the
// terminal write first, because it is the one that changes what a reader sees.
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// recordCancellation writes the "stopping" state the moment the context is
// cancelled, and returns a function that stops watching and waits for it.
//
// Why a watcher instead of the status write Execute already does on the way out:
// Execute only reaches that write when body returns, and body is exactly what a
// cancelled run cannot be relied on to finish. The audit's run stayed `running`
// with an advancing updated_at through +2s, +5s, +10s, +20s, and only closed when
// the operator manually killed the orphaned grandchild holding its stdout pipe.
// That teardown bug is frozen and is not being fixed here; what is fixed is that
// the record no longer waits for it. Between the Ctrl-C and the close, the state
// on disk says a stop was requested.
//
// The returned function stops watching, waits for the goroutine to exit and
// reports what the write cost, if anything. Waiting is what orders the caller's
// terminal write after any write made here, and it is why no goroutine outlives
// Execute. It is idempotent, so the deferred path can call it too.
func (r *Runner) recordCancellation(ctx context.Context) func() error {
	var cancelErr error
	if ctx == nil || ctx.Done() == nil {
		return func() error { return nil }
	}
	quit, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			// Not a terminal status: the run has not ended, it has been asked to.
			// The pid and the heartbeat still decide what happens if it never
			// closes.
			cancelErr = r.handle.SetStatus(run.StatusCanceling)
		case <-quit:
		}
	}()
	return func() error {
		once.Do(func() { close(quit) })
		<-done
		return cancelErr
	}
}

// clearPauseControl removes this run's pause control file, quietly: by the time
// it runs the run is over, and a failure to unlink scratch is not something the
// caller can act on. claimID is the second guard.
func (r *Runner) clearPauseControl() {
	if r == nil || r.Repo == "" || r.RunID == "" {
		return
	}
	_ = run.ClearPause(r.Repo, r.RunID)
}

// Record returns this run's current on-disk snapshot. Zero Record when the run
// has no identity.
func (r *Runner) Record() run.Record {
	if r == nil || r.handle == nil {
		return run.Record{}
	}
	return r.handle.Record()
}

// finalStatus maps the way a run ended onto the status that goes on disk.
//
// ctx comes first because a SIGINT cancels it and the orchestrator then returns
// whatever error the interrupted step happened to produce: recording that as
// `failed` would blame the work for the operator's Ctrl-C, and the difference
// matters to anyone deciding whether to resume.
func (r *Runner) finalStatus(ctx context.Context, err error) run.Status {
	if ctx != nil && ctx.Err() != nil {
		return run.StatusCanceled
	}
	if err != nil {
		return run.StatusFailed
	}
	// Nothing failed — but did everything RUN? A pinned run (`--task S03`), a
	// `--single`, or a resume that only had two of nine steps left all reach
	// here having done exactly what they were asked and left the recipe
	// unfinished. Calling that `done` is the screen saying something rosier
	// than the state, which this tool has spent a lot of commits not doing.
	if r != nil && r.pendingTasks() > 0 {
		return run.StatusPartial
	}
	return run.StatusDone
}

// pendingTasks counts the steps this run left unexecuted. A tasks file that
// cannot be read reports zero: the status is a summary, and a summary must not
// invent a worse answer than it can support.
func (r *Runner) pendingTasks() int {
	view, err := ReadProject(r.Repo, r.Project)
	if err != nil || view == nil {
		return 0
	}
	pending := 0
	for _, t := range view.Tasks {
		if t.Status == types.StatusPending {
			pending++
		}
	}
	return pending
}

// startIdentity registers the run and returns its writable handle.
func startIdentity(req RunRequest) (*run.Handle, error) {
	repo, err := runRepo(req)
	if err != nil {
		return nil, err
	}
	reg := run.Registry{}
	if req.Registry != nil {
		reg = *req.Registry
	}
	if reg.Repo == "" {
		reg.Repo = repo
	}
	// Project always; Recipe only when the tasks on disk say a recipe produced
	// them — see Runner.Identity.
	env, err := ParseEnvironment(req.Environment)
	if err != nil {
		return nil, err
	}
	if err := refuseIfAlreadyRunning(reg, repo, req.Project); err != nil {
		return nil, err
	}
	return reg.Start(run.StartOptions{
		Project:     req.Project,
		Recipe:      recipeFromTasks(req.WorkDir, req.Project),
		Environment: string(env),
	})
}

// refuseIfAlreadyRunning is the door in front of the one collision the runner
// cannot survive: two runs of the SAME project, in the SAME repository, at the
// same time.
//
// MEASURED, and it is worse than "both run". The second process reads a tasks.md
// whose step the FIRST one is executing, sees it as RUNNING, and — correctly for
// the case that rescue was written for — treats it as the leftover of a run that
// died: it resets the step to PENDING and executes it again. So two agents write
// one checkout, two processes rewrite one tasks.md, and the final state is
// whichever wrote last. Nothing reports it.
//
// The rescue cannot tell "died" from "running right now", and it does not have
// to: the run index does, with the same pid probe `corvex run list` prints. So
// the check happens HERE, before a second run exists at all.
//
// The refusal names the run that holds the project and the two ways forward,
// because "already running" without an id is a message that sends somebody
// hunting through `run list` for something the tool already knew.
func refuseIfAlreadyRunning(reg run.Registry, repo, project string) error {
	resolver := run.Resolver{Home: reg.Home, Machine: reg.Machine}
	views, err := resolver.ListRepo(repo)
	if err != nil {
		// A listing that cannot be read is not a reason to refuse work: the
		// index is machine-local scratch, and a run refused because of it would
		// be a tool that stops working when its own bookkeeping is damaged.
		return nil
	}
	for _, v := range views {
		if v.Record.Project != project {
			continue
		}
		if v.Liveness != run.LivenessAlive && v.Liveness != run.LivenessCanceling {
			continue
		}
		return fmt.Errorf("run %s is already running %q in this repository (pid %d).\n"+
			"Two runs of one project share its tasks.md and its checkout: the second would re-execute the step the first is running.\n"+
			"  → wait for it, or stop it:  corvex run kill %s",
			v.Record.RunID, project, v.Record.PID, v.Record.RunID)
	}
	return nil
}

// recipeName is the frontmatter prefix recipe.Compile stamps on generated_by.
const recipeName = "corvex-recipe:"

// recipeFromTasks reports which recipe produced a project's tasks.md, or "".
//
// Best-effort by design: this runs before the planner on a path where tasks.md
// may not exist yet, and a run must never be refused because its recipe name
// could not be read. The failure mode is the pre-F2 one (an empty field), which
// the format already tolerates.
func recipeFromTasks(workDir, project string) string {
	_, dag, err := task.ParseTasksFile(filepath.Join(ProjectDir(workDir, project), "tasks.md"))
	if err != nil {
		return ""
	}
	// HasPrefix, not a bare TrimPrefix: the planner stamps its own value here,
	// and TrimPrefix would hand back "corvex-planner" as if it were a recipe.
	if !strings.HasPrefix(dag.GeneratedBy, recipeName) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(dag.GeneratedBy, recipeName))
}

// runRepo resolves the repository a run is recorded against: the git root, not
// the working directory, because a run started from a subdirectory still belongs
// to the repository and the repo path is what a second process joins on.
//
// Outside a git repository there is nothing to key on, so the working directory
// is the honest fallback — `corvex run` outside a repo already works today and
// F1 is not the phase that starts refusing it.
func runRepo(req RunRequest) (string, error) {
	repo := req.Repo
	if repo == "" {
		if root, rootErr := FindGitRoot(req.WorkDir); rootErr == nil {
			repo = root
		} else {
			repo = req.WorkDir
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolving run repo %q: %w", repo, err)
	}
	return abs, nil
}
