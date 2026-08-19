package orchestrator

import (
	"context"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/run"
)

// DefaultPausePoll is how often a paused run re-reads its control file.
//
// One second, and the choice is about a human at a keyboard: `run resume` is
// typed and then waited on, so anything slower reads as "it did not work" and
// anything faster buys nothing — the run is doing nothing while it polls, and a
// stat of one file per second costs nothing to anybody.
const DefaultPausePoll = time.Second

// waitWhilePaused holds the walk at the wave barrier for as long as the control
// file is there.
//
// # Why here and not inside a step
//
// This is the one point in the loop where no worker goroutine is alive (the same
// window fan-out expansion uses). Reading the control file anywhere else would
// mean pausing in the middle of a step — and a step is a provider call that has
// already been paid for, whose partial output is worth nothing. Pausing there
// would not save money, it would burn it. So the promise is deliberately weak
// and honest: `run pause` stops the NEXT wave, it never interrupts work in
// flight, and a wave of eight parallel steps runs to completion before the pause
// takes effect.
//
// # Why polling
//
// There is no daemon and no channel between the two processes (see
// internal/run/pause.go). The file is the whole mechanism, and a file has to be
// looked at.
func (o *Orchestrator) waitWhilePausedOnDisk(ctx context.Context) error {
	// No identity, no addressable control file: a run nobody can name is a run
	// nobody can pause. Same reasoning as the gate, which refuses rather than
	// blocking on something no second process could ever answer.
	if o.opts.Repo == "" || o.opts.Identity.RunID == "" {
		return nil
	}

	// held is NOT restored on the cancelled path, and that is the point: a
	// Ctrl-C on a paused run has already had `canceling` written to the record by
	// the identity watcher, and writing `running` back over it would resurrect
	// exactly the lie LivenessCanceling exists to prevent — a run on its way out
	// reading as one that is working.
	held := false
	release := func() {
		if held {
			o.reportRunStatus(run.StatusRunning)
			o.emit(Event{Type: EventError, Message: "resumed"})
		}
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, paused, err := run.PauseRequested(o.opts.Repo, o.opts.Identity.RunID)
		if err != nil {
			// A control file that cannot be read must not wedge the run: the
			// failure mode of stopping forever on an unreadable stat is worse
			// than the failure mode of missing a pause somebody can ask for
			// again. Reported, because "pause silently does nothing" is exactly
			// the kind of thing discovered a year late.
			charmbraceletlog.Warn("reading the run pause control file", "run", o.opts.Identity.RunID, "err", err)
			release()
			return nil
		}
		if !paused {
			release()
			return nil
		}
		if !held {
			held = true
			o.reportRunStatus(run.StatusPaused)
			o.emit(Event{Type: EventError, Message: "paused — corvex run resume " + o.opts.Identity.RunID})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.pausePoll()):
		}
	}
}

func (o *Orchestrator) pausePoll() time.Duration {
	if o.opts.PausePoll > 0 {
		return o.opts.PausePoll
	}
	return DefaultPausePoll
}

// reportRunStatus is best-effort by design: losing the ability to say `paused`
// degrades what a listing shows, it does not make the pause wrong — the run is
// held by the file either way.
func (o *Orchestrator) reportRunStatus(s run.Status) {
	if o.opts.SetRunStatus == nil {
		return
	}
	if err := o.opts.SetRunStatus(s); err != nil {
		charmbraceletlog.Warn("recording run status", "status", s, "err", err)
	}
}
