package run_test

import (
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
		want  run.Liveness
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
			name:  "a record without a host is not assumed foreign",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, UpdatedAt: fresh},
			probe: alwaysAlive,
			host:  "this-box",
			want:  run.LivenessAlive,
		},
		{
			name:  "a heartbeat from the future (clock skew) is not stale",
			rec:   run.Record{PID: 10, Status: run.StatusRunning, UpdatedAt: now.Add(time.Hour)},
			probe: alwaysAlive,
			want:  run.LivenessAlive,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := run.Resolver{Now: func() time.Time { return now }, Alive: tc.probe, Host: tc.host}
			if got := r.Liveness(tc.rec); got != tc.want {
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
	rec := run.Record{PID: 10, Status: run.StatusRunning, UpdatedAt: now.Add(-3 * time.Second)}
	tight := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, StaleAfter: time.Second}
	if got := tight.Liveness(rec); got != run.LivenessStale {
		t.Errorf("with StaleAfter=1s: liveness = %q, want stale", got)
	}
	loose := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, StaleAfter: time.Minute}
	if got := loose.Liveness(rec); got != run.LivenessAlive {
		t.Errorf("with StaleAfter=1m: liveness = %q, want alive", got)
	}
}

// TestResolverDefaultsAreUsable makes sure the zero Resolver works: real clock,
// real probe, real hostname. Callers in ops should not have to wire anything.
func TestResolverDefaultsAreUsable(t *testing.T) {
	self := run.Record{PID: 1, Status: run.StatusRunning, UpdatedAt: time.Now()}
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
