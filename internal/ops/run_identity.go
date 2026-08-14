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
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/run"
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
	// Empty when identity could not be registered — see IdentityErr.
	RunID string

	// Repo is the absolute git root the run was recorded against.
	Repo string

	// IdentityErr is why this run has no identity, or nil.
	//
	// Failing to register is deliberately NOT fatal. A read-only home or a
	// full disk must not refuse work the user is about to pay for, and the
	// degraded mode is one that already exists on disk: lines with no run_id,
	// exactly like every ledger written before F1. The caller reports this;
	// it never turns into an aborted run.
	IdentityErr error

	// StatusErr is why the terminal status could not be written, or nil. Same
	// policy: reported, never allowed to change the run's own outcome.
	StatusErr error

	handle   *run.Handle
	interval time.Duration
}

// Identity is what the ledger stamps on every line it writes for this run.
//
// Recipe is deliberately empty on the legacy `corvex run <project>` path: there
// is no recipe, and inventing a name for one (the project's name, or a sentinel
// like "implicit") would put a value in the field that no `recipe show` could
// ever resolve. Absence already means "no recipe", omitempty makes it free, and
// `recipe == ""` is precisely the discriminator F2 will need once recipes are
// real. The project is not lost: it is its own field on the record.
func (r *Runner) Identity() activity.Identity {
	if r == nil {
		return activity.Identity{}
	}
	return activity.Identity{RunID: r.RunID, Repo: r.Repo}
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
		// No identity (see IdentityErr): run anyway, record nothing.
		return body(ctx)
	}

	hb := r.handle.StartHeartbeat(r.interval)
	recorded := false
	defer func() {
		hb.Stop()
		if !recorded {
			_ = r.handle.SetStatus(run.StatusFailed)
		}
	}()

	err := body(ctx)

	hb.Stop()
	r.StatusErr = r.handle.SetStatus(finalStatus(ctx, err))
	recorded = true
	if r.StatusErr == nil {
		r.StatusErr = hb.LastErr()
	}
	return err
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
func finalStatus(ctx context.Context, err error) run.Status {
	if ctx != nil && ctx.Err() != nil {
		return run.StatusCanceled
	}
	if err != nil {
		return run.StatusFailed
	}
	return run.StatusDone
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
	// Project, not Recipe — see Runner.Identity for why the legacy path records
	// no recipe at all.
	return reg.Start(run.StartOptions{Project: req.Project})
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
