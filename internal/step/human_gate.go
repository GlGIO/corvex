package step

import (
	"context"
	"fmt"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// DefaultGatePoll is how often a parked run re-reads its gate file.
//
// Two seconds, not a file watcher: the wait it is polling is measured in minutes
// or days, so 2s is imperceptible to the person deciding and free for the disk —
// and it costs no dependency and no platform-specific code path. The run is
// asleep in between, so this is not a busy loop.
const DefaultGatePoll = 2 * time.Second

// humanGate blocks the run until somebody decides, from another process.
//
// This replaces the pre-F2 behaviour, which was to abort the run with a message
// telling you to re-run it with --approve-gates. That was never a gate: it threw
// away the work in flight and made approval mean "start over, and this time say
// yes to everything". What a gate has to do is hold.
//
// The protocol is entirely on disk, because the decider is never this process:
//
//  1. write the gate file (prompt + evidence) with O_EXCL;
//  2. set the run record to `parked`, which also appends a snapshot to the
//     global index — that is what lets `corvex gate list` find this gate from
//     another repository without any new global state;
//  3. poll the gate file, with the heartbeat still beating so liveness reads
//     `alive` rather than `stale`: a parked run is up and waiting, and F1 had
//     already written that this is the right answer for `parked`;
//  4. on a decision, record it, return the record to `running`, and continue.
func (e *Executor) humanGate(ctx context.Context, r *Run, t *types.Task, g types.Gate, acc *evidenceSet) error {
	e.emit(event.Event{Type: event.HumanGate, TaskID: t.ID, Message: gateLabel(g, t.Title)})

	// --approve-gates stays the CI path. Without it a pipeline with a human
	// gate would block until it timed out, which is worse than a documented
	// auto-approval.
	if e.approveGates {
		charmbraceletlog.Info("human gate auto-approved (--approve-gates)", "task", t.ID, "gate", g.Describe())
		e.emit(event.Event{Type: event.GateDecided, TaskID: t.ID, Status: types.StatusPassed, Message: g.Describe()})
		return nil
	}

	if r.Identity.Repo == "" || r.Identity.RunID == "" {
		// Without an identity there is no addressable gate file, so nobody
		// could ever approve it. Refusing beats blocking forever.
		return Fatal(fmt.Errorf("task %s: human gate %s cannot be opened — this run has no identity on disk", t.ID, g.Describe()))
	}

	e.resolveDeclared(ctx, t, acc)
	pending, err := e.openGate(r, t, g, acc)
	if err != nil {
		return Fatal(fmt.Errorf("task %s: opening human gate: %w", t.ID, err))
	}

	e.emit(event.Event{Type: event.GatePending, TaskID: t.ID, Message: g.Describe()})
	e.setRunStatus(run.StatusParked)
	defer e.setRunStatus(run.StatusRunning)

	decided, err := e.awaitDecision(ctx, r, t, pending)
	if err != nil {
		return err
	}

	e.emit(event.Event{
		Type:    event.GateDecided,
		TaskID:  t.ID,
		Status:  verdictStatus(decided.Verdict),
		Message: g.Describe(),
	})
	if decided.Verdict == gate.Approved {
		return nil
	}
	reason := decided.Reason
	if reason == "" {
		reason = string(decided.Verdict)
	}
	return e.gateRefused(t, g, reason)
}

func verdictStatus(v gate.Verdict) types.TaskStatus {
	if v == gate.Approved {
		return types.StatusPassed
	}
	return types.StatusFailed
}

// openGate writes the pending gate, evidence and all.
func (e *Executor) openGate(r *Run, t *types.Task, g types.Gate, acc *evidenceSet) (gate.Pending, error) {
	p := gate.Pending{
		RunID:    r.Identity.RunID,
		StepID:   t.ID,
		Repo:     r.Identity.Repo,
		Project:  r.Identity.Project,
		Recipe:   r.Identity.Recipe,
		Nature:   types.GateHuman,
		Label:    gateLabel(g, t.Title),
		Prompt:   g.Prompt,
		Title:    t.Title,
		Evidence: gate.CapAll(acc.all()),
		OpenedAt: e.now(),
	}
	if g.ExpiresAfter != "" {
		if d, err := time.ParseDuration(g.ExpiresAfter); err == nil {
			deadline := p.OpenedAt.Add(d)
			p.ExpiresAt = &deadline
		}
	}
	return p, gate.Open(p)
}

// awaitDecision polls until somebody answers, the deadline passes, or the run is
// cancelled.
func (e *Executor) awaitDecision(ctx context.Context, r *Run, t *types.Task, p gate.Pending) (gate.Decision, error) {
	interval := e.gatePoll
	if interval <= 0 {
		interval = DefaultGatePoll
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		cur, err := gate.Read(r.Identity.Repo, r.Identity.RunID, t.ID)
		if err == nil {
			if cur.Decided() {
				return *cur.Decision, nil
			}
			if cur.Expired(e.now()) {
				// Expiry is a rejection with a reason, never an approval — an
				// unattended gate that lets the work through is a rubber stamp
				// on a timer.
				d := gate.Decision{Verdict: gate.Expired, DecidedAt: e.now(), Reason: "no decision before expires_after"}
				if _, derr := gate.Decide(r.Identity.Repo, r.Identity.RunID, t.ID, d); derr != nil {
					charmbraceletlog.Warn("recording gate expiry", "task", t.ID, "err", derr)
				}
				return d, nil
			}
		} else {
			// A gate file that vanished mid-wait is somebody cleaning scratch
			// under a live run. Nothing good can come of continuing to wait for
			// a file nobody will write.
			return gate.Decision{}, Fatal(fmt.Errorf("task %s: gate file became unreadable while waiting: %w", t.ID, err))
		}

		select {
		case <-ctx.Done():
			return gate.Decision{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// setRunStatus reports the run's own state to whatever is supervising it. A
// failure here is warned, never fatal: losing the `parked` bit costs a listing
// its accuracy, and refusing to hold the gate over it would be worse.
func (e *Executor) setRunStatus(s run.Status) {
	if e.setStatus == nil {
		return
	}
	if err := e.setStatus(s); err != nil {
		charmbraceletlog.Warn("updating run status", "status", s, "err", err)
	}
}

func (e *Executor) now() time.Time {
	if e.nowFn != nil {
		return e.nowFn()
	}
	return time.Now().UTC()
}
