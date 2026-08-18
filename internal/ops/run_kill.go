package ops

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// KillOptions tunes how hard `run kill` tries to be sure before it signals.
type KillOptions struct {
	// Prove waits for the run's own heartbeat to advance before signalling.
	Prove bool
	// Timeout bounds that wait; zero uses DefaultKillProof.
	Timeout time.Duration
	// Poll is how often the record is re-read; zero uses DefaultKillPoll.
	Poll time.Duration
}

// KillResult reports what was signalled, so the caller can say it out loud.
type KillResult struct {
	RunID  string `json:"run_id"`
	Repo   string `json:"repo"`
	PID    int    `json:"pid"`
	Proved bool   `json:"proved"`
}

const (
	// DefaultKillProof is a little over one heartbeat interval (10s), which is
	// the shortest wait that can observe a beat.
	DefaultKillProof = 13 * time.Second
	DefaultKillPoll  = 500 * time.Millisecond
)

// KillRun signals a live run to stop.
//
// # Why this waits by default
//
// F1 wrote down the pid-reuse window and justified accepting it with a precise
// claim: *"nothing destructive hangs off this bit (we never kill or rewrite
// based on it)"*. This function is the first thing that does — so it does not
// get to inherit that acceptance, it has to close the window.
//
// The close is cheap and portable: the only process that refreshes a run's
// heartbeat is that run. So before signalling, wait for `updated_at` to move. A
// pid inherited by an unrelated process cannot make that happen, and no
// platform-specific process-start-time lookup is needed — which is the fix F1
// considered and rejected as too costly for a listing. Here the cost is one
// heartbeat of latency on a command a human types rarely, and what it buys is
// that `corvex run kill` cannot SIGTERM somebody else's process.
//
// `--now` (Prove=false) skips it, for the case where the user is watching the
// run and does not want to wait 13 seconds to stop it.
func (l RunLister) KillRun(runID string, opts KillOptions) (KillResult, error) {
	row, err := l.FindRun(runID)
	if err != nil {
		return KillResult{}, err
	}
	if row.Liveness != run.LivenessAlive {
		return KillResult{}, fmt.Errorf("run %s is %s — there is no live process to signal", runID, row.Liveness)
	}
	rec, err := run.ReadRecord(row.Repo, runID)
	if err != nil {
		return KillResult{}, fmt.Errorf("reading the run record: %w", err)
	}
	if rec.PID <= 0 {
		return KillResult{}, fmt.Errorf("run %s has no pid on record", runID)
	}

	res := KillResult{RunID: runID, Repo: row.Repo, PID: rec.PID}
	if opts.Prove {
		if err := waitForBeat(row.Repo, runID, rec.Freshness(), opts); err != nil {
			return KillResult{}, err
		}
		res.Proved = true
	}

	proc, err := os.FindProcess(rec.PID)
	if err != nil {
		return KillResult{}, fmt.Errorf("pid %d: %w", rec.PID, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return KillResult{}, fmt.Errorf("signalling pid %d: %w", rec.PID, err)
	}
	return res, nil
}

// waitForBeat returns nil as soon as the run writes a fresher record than the
// one we read, and an actionable error if it never does.
func waitForBeat(repo, runID string, since time.Time, opts KillOptions) error {
	timeout, poll := opts.Timeout, opts.Poll
	if timeout <= 0 {
		timeout = DefaultKillProof
	}
	if poll <= 0 {
		poll = DefaultKillPoll
	}
	deadline := time.Now().Add(timeout)
	for {
		if rec, err := run.ReadRecord(repo, runID); err == nil {
			if rec.Freshness().After(since) {
				return nil
			}
			if rec.Status.IsTerminal() {
				return fmt.Errorf("run %s finished as %s while we were checking — nothing to signal", runID, rec.Status)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("run %s did not write a heartbeat in %s, so its pid cannot be proven to be its own "+
				"(a recycled pid looks alive for up to %s); re-run with --now to signal anyway",
				runID, timeout, run.DefaultStaleAfter)
		}
		time.Sleep(poll)
	}
}
