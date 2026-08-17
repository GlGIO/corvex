package run_test

import (
	"os"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

func TestLiveness(t *testing.T) {
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	fresh := now.Add(-5 * time.Second)
	old := now.Add(-run.DefaultStaleAfter - time.Second)

	cases := []struct {
		name  string
		rec   run.Record
		probe run.ProcessProbe
		host  string
		// noHost keeps the record's host empty. Every other case gets this
		// machine's name filled in, because a record that names no machine at all
		// is not attributable to any pid — see the case that says so.
		noHost bool
		want   run.Liveness
	}{
		{
			name:  "running with a live pid and a fresh heartbeat",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, StartedAt: fresh, UpdatedAt: fresh},
			probe: alwaysAlive,
			want:  run.LivenessAlive,
		},
		{
			name:  "parked on a human gate is still alive",
			rec:   run.Record{PID: 10, Status: run.StatusParked, StartedAt: fresh, UpdatedAt: fresh},
			probe: alwaysAlive,
			want:  run.LivenessAlive,
		},
		{
			name:  "no heartbeat yet falls back to started_at",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, StartedAt: fresh},
			probe: alwaysAlive,
			want:  run.LivenessAlive,
		},
		{
			name:  "SIGKILL: status still running, pid gone",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, StartedAt: fresh, UpdatedAt: fresh},
			probe: neverAlive,
			want:  run.LivenessDead,
		},
		{
			name:  "pid alive but heartbeat older than the threshold",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, StartedAt: old, UpdatedAt: old},
			probe: alwaysAlive,
			want:  run.LivenessStale,
		},
		{
			name:  "exactly at the threshold is not yet stale",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, UpdatedAt: now.Add(-run.DefaultStaleAfter)},
			probe: alwaysAlive,
			want:  run.LivenessAlive,
		},
		{
			name:  "terminal status wins over a recycled pid",
			rec:   run.Record{PID: 10, Status: run.StatusDone, StartedAt: old, UpdatedAt: old},
			probe: alwaysAlive,
			want:  run.LivenessFinished,
		},
		{
			name:  "failed is finished, not dead",
			rec:   run.Record{PID: 10, Status: run.StatusFailed, UpdatedAt: old},
			probe: neverAlive,
			want:  run.LivenessFinished,
		},
		{
			name:  "canceled is finished",
			rec:   run.Record{PID: 10, Status: run.StatusCanceled, UpdatedAt: fresh},
			probe: neverAlive,
			want:  run.LivenessFinished,
		},
		{
			name:  "a status this binary does not know still gets probed",
			rec:   run.Record{PID: 10, Status: "gating", StartedAt: fresh, UpdatedAt: fresh},
			probe: neverAlive,
			want:  run.LivenessDead,
		},
		{
			name:  "no pid recorded",
			rec:   run.Record{Status: run.StatusRunning, UpdatedAt: fresh},
			probe: alwaysAlive,
			want:  run.LivenessUnknown,
		},
		{
			name:  "negative pid",
			rec:   run.Record{PID: -1, Status: run.StatusRunning, UpdatedAt: fresh},
			probe: alwaysAlive,
			want:  run.LivenessUnknown,
		},
		{
			name:  "a pid from another host means nothing here",
			rec:   run.Record{PID: 10, Host: "other-box", Status: run.StatusRunning, UpdatedAt: fresh},
			probe: alwaysAlive,
			host:  "this-box",
			want:  run.LivenessUnknown,
		},
		{
			name:  "same host is probed normally",
			rec:   run.Record{PID: 10, Host: "this-box", Status: run.StatusRunning, UpdatedAt: fresh},
			probe: alwaysAlive,
			host:  "this-box",
			want:  run.LivenessAlive,
		},
		{
			// Was "not assumed foreign", i.e. probed locally. A record that names
			// no machine gives a reader nothing to key a pid on, and pids are only
			// meaningful per machine: the honest answer is "not decidable here".
			name:   "a record naming no machine is not attributable",
			rec:    run.Record{PID: 10, Status: run.StatusRunning, UpdatedAt: fresh},
			probe:  alwaysAlive,
			host:   "this-box",
			noHost: true,
			want:   run.LivenessUnknown,
		},
		{
			// Was "clock skew is not stale", which is how a false alive became
			// permanent: a negative age never crosses a positive threshold.
			name:  "a heartbeat from the future is suspect, not fresh",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, UpdatedAt: now.Add(time.Hour)},
			probe: alwaysAlive,
			want:  run.LivenessStale,
		},
		{
			name:  "a stop was requested and the process is still there",
			rec:   run.Record{PID: 10, Status: run.StatusCanceling, UpdatedAt: fresh},
			probe: alwaysAlive,
			want:  run.LivenessCanceling,
		},
		{
			// The pid still answers the question when the run never closes: a
			// process that died while winding down is dead, not "canceling".
			name:  "a stop was requested and the process is gone",
			rec:   run.Record{PID: 10, Status: run.StatusCanceling, UpdatedAt: fresh},
			probe: neverAlive,
			want:  run.LivenessDead,
		},
		{
			// And the state outlives the heartbeat: "somebody asked this to stop"
			// does not stop being true when updated_at ages.
			name:  "a stop was requested and the heartbeat has gone quiet",
			rec:   run.Record{PID: 10, Status: run.StatusCanceling, UpdatedAt: old},
			probe: alwaysAlive,
			want:  run.LivenessCanceling,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host
			if host == "" {
				host = "this-box"
			}
			rec := tc.rec
			if rec.Host == "" && !tc.noHost {
				rec.Host = "this-box"
			}
			r := run.Resolver{Now: func() time.Time { return now }, Alive: tc.probe, Host: host}
			if got := r.Liveness(rec); got != tc.want {
				t.Errorf("liveness = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStaleAfterIsConfigurableAndDerivedFromTheInterval(t *testing.T) {
	if run.DefaultStaleAfter != 4*run.DefaultHeartbeatInterval {
		t.Errorf("DefaultStaleAfter = %v, want 4 x the heartbeat interval (%v)",
			run.DefaultStaleAfter, run.DefaultHeartbeatInterval)
	}
	now := time.Now()
	rec := run.Record{PID: 10, Host: "box", Status: run.StatusRunning, UpdatedAt: now.Add(-3 * time.Second)}
	tight := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, StaleAfter: time.Second, Host: "box"}
	if got := tight.Liveness(rec); got != run.LivenessStale {
		t.Errorf("with StaleAfter=1s: liveness = %q, want stale", got)
	}
	loose := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, StaleAfter: time.Minute, Host: "box"}
	if got := loose.Liveness(rec); got != run.LivenessAlive {
		t.Errorf("with StaleAfter=1m: liveness = %q, want alive", got)
	}
}

// TestCancelingIsItsOwnStateOnDisk pins the strings, because the strings are the
// contract: a second process learns what happened by parsing JSON, not by
// importing this package.
func TestCancelingIsItsOwnStateOnDisk(t *testing.T) {
	if run.StatusCanceling != "canceling" || run.LivenessCanceling != "canceling" {
		t.Errorf("status %q / liveness %q, want canceling", run.StatusCanceling, run.LivenessCanceling)
	}
	if run.StatusCanceling.IsTerminal() {
		t.Error("canceling counts as terminal: the run has not ended, it has been asked to — " +
			"and pid + heartbeat must keep deciding what happens if it never closes")
	}
	if run.StatusCanceling == run.StatusParked {
		t.Error("canceling and parked are the same value")
	}
	// How it shows up in a listing: not alive. A supervisor must not count it as
	// capacity, and must not ask it to stop again.
	if (run.View{Liveness: run.LivenessCanceling}).Alive() {
		t.Error("View.Alive() is true for a run that was asked to stop")
	}
}

// TestResolverDefaultsAreUsable makes sure the zero Resolver works: real clock,
// real probe, real hostname. Callers in ops should not have to wire anything.
func TestResolverDefaultsAreUsable(t *testing.T) {
	// The record has to name this machine, and the zero Resolver has to work out
	// which machine that is on its own.
	host, err := os.Hostname()
	if err != nil {
		t.Skipf("no hostname on this machine: %v", err)
	}
	self := run.Record{PID: 1, Host: host, Status: run.StatusRunning, UpdatedAt: time.Now()}
	if got := (run.Resolver{}).Liveness(self); got != run.LivenessAlive {
		t.Errorf("zero Resolver on a live pid: liveness = %q, want alive", got)
	}
	view := run.View{Liveness: run.LivenessAlive}
	if !view.Alive() {
		t.Error("View.Alive() = false for an alive view")
	}
	stale := run.View{Liveness: run.LivenessStale}
	if stale.Alive() {
		t.Error("View.Alive() = true for a stale view")
	}
}
