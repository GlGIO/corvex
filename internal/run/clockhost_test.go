package run_test

// Two ways a reader used to conclude "alive" when it had no business doing so:
// a clock that moved backwards, and a machine name the network rewrites.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// TestFreshnessFromTheFutureIsNotFreshness: the comparison used to be
// one-sided (`now - freshness > staleAfter`), so an updated_at in the future
// produced a negative age that never crossed the threshold. NTP correcting a
// drift, a VM snapshot or an RTC waking up wrong is enough, and the consequence
// is the one thing liveness.go promised could not happen: a permanent false
// `alive` on a recycled pid, for a run that died weeks ago.
func TestFreshnessFromTheFutureIsNotFreshness(t *testing.T) {
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	r := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, Host: "box"}

	future := run.Record{PID: 10, Host: "box", Status: run.StatusRunning, UpdatedAt: now.Add(time.Hour)}
	if got := r.Liveness(future); got == run.LivenessAlive {
		t.Errorf("updated_at one hour in the future reads as %q: a pid that got recycled is now "+
			"alive forever, and no amount of waiting fixes it", got)
	} else if got != run.LivenessStale {
		t.Errorf("liveness = %q, want stale — the pid does exist, so it is not dead; "+
			"the record's own idea of time is what cannot be trusted", got)
	}

	// Waiting does not help, which is what makes it permanent: the same record
	// read a day later is still not stale under the old comparison.
	later := run.Resolver{Now: func() time.Time { return now.Add(24 * time.Hour) },
		Alive: alwaysAlive, Host: "box"}
	if got := later.Liveness(future); got == run.LivenessAlive {
		t.Errorf("still %q a day later: the degradation to stale is not bounded by StaleAfter", got)
	}

	// And the tolerance is not zero: two processes on one machine can disagree by
	// a hair without either of them being wrong.
	hair := run.Record{PID: 10, Host: "box", Status: run.StatusRunning, UpdatedAt: now.Add(time.Second)}
	if got := r.Liveness(hair); got != run.LivenessAlive {
		t.Errorf("a one-second skew reads as %q; sub-second disagreement between two "+
			"processes is normal and must not cost freshness", got)
	}
}

// TestNoMachineIdentityIsNotALicenceToProbe: a record with no host at all used
// to sail past the guard and get its pid probed against this machine, so the
// auditor saw `host= -> alive` on a recycled pid. Absence of identity is not
// evidence of locality.
func TestNoMachineIdentityIsNotALicenceToProbe(t *testing.T) {
	now := time.Now()
	// A pid that certainly exists here: this very process. If the guard leaks,
	// the answer is a confident "alive".
	rec := run.Record{PID: os.Getpid(), Status: run.StatusRunning, UpdatedAt: now}
	r := run.Resolver{Now: func() time.Time { return now }, Host: "box", Home: t.TempDir()}
	if got := r.Liveness(rec); got != run.LivenessUnknown {
		t.Errorf("a record naming no machine reads as %q; want unknown — the pid was probed "+
			"locally on nothing but the absence of a host", got)
	}
}

// TestRenamingTheMachineDoesNotHideItsLiveRuns is the mac case: os.Hostname()
// returns the mDNS name, and joining a network with a name collision turns
// `box.local` into `box-2.local`. Under a hostname-keyed guard that single
// event makes every live run on the machine read as `unknown` — the whole
// listing empties out because the Wi-Fi changed.
func TestRenamingTheMachineDoesNotHideItsLiveRuns(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	h, err := run.Registry{Repo: repo, Home: home}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Same machine, same corvex home, same pid — only the name the network hands
	// out has changed.
	renamed := run.Resolver{Home: home, Host: "box-2.local"}
	v, ok, err := renamed.Get(h.RunID())
	if err != nil || !ok {
		t.Fatalf("Get(%s) = %v, %v", h.RunID(), ok, err)
	}
	if v.Liveness != run.LivenessAlive {
		t.Errorf("after a rename, this machine's own live run reads as %q; want alive — "+
			"machine identity must not hinge on a name DHCP and mDNS rewrite", v.Liveness)
	}

	// The identity that replaces the hostname is persisted, so it survives the
	// next reboot as well as the next rename.
	id, err := run.MachineID(home)
	if err != nil {
		t.Fatalf("MachineID: %v", err)
	}
	if id == "" {
		t.Fatal("MachineID is empty")
	}
	again, err := run.MachineID(home)
	if err != nil || again != id {
		t.Errorf("MachineID is not stable: %q then %q (%v)", id, again, err)
	}
	if _, err := os.Stat(filepath.Join(home, run.MachineFile)); err != nil {
		t.Errorf("machine identity not persisted in the corvex home: %v", err)
	}
}

// TestAForeignMachineIsStillForeign: the fix must not turn the guard off. A
// record carrying another machine's identity is not probed here, whatever its
// pid says and whatever the hostname happens to be today.
func TestAForeignMachineIsStillForeign(t *testing.T) {
	now := time.Now()
	rec := run.Record{
		PID: os.Getpid(), Machine: "0123456789abcdef", Host: "box",
		Status: run.StatusRunning, UpdatedAt: now,
	}
	r := run.Resolver{Now: func() time.Time { return now }, Machine: "fedcba9876543210", Host: "box"}
	if got := r.Liveness(rec); got != run.LivenessUnknown {
		t.Errorf("liveness = %q, want unknown: a pid from another machine means nothing here "+
			"even when both machines answer to the same name", got)
	}

	// A legacy record (no machine id) still falls back to the hostname, and the
	// mDNS suffix is not part of the name.
	legacy := run.Record{PID: os.Getpid(), Host: "Box.local", Status: run.StatusRunning, UpdatedAt: now}
	local := run.Resolver{Now: func() time.Time { return now }, Machine: "abc", Host: "box"}
	if got := local.Liveness(legacy); got != run.LivenessAlive {
		t.Errorf("legacy record with host %q against host %q = %q, want alive",
			legacy.Host, local.Host, got)
	}
	foreignLegacy := run.Record{PID: os.Getpid(), Host: "other-box", Status: run.StatusRunning, UpdatedAt: now}
	if got := local.Liveness(foreignLegacy); got != run.LivenessUnknown {
		t.Errorf("legacy record from %q = %q, want unknown", foreignLegacy.Host, got)
	}
}

// TestResolverDoesNotCreateTheMachineFile: Resolver reads state and mutates
// nothing — including nothing in the corvex home. A listing must not be able to
// invent this machine's identity, or a reader on a fresh machine would mint an
// id that the next `corvex run` then has to agree with.
func TestResolverDoesNotCreateTheMachineFile(t *testing.T) {
	home := t.TempDir()
	rec := run.Record{PID: os.Getpid(), Host: "box", Status: run.StatusRunning, UpdatedAt: time.Now()}
	if got := (run.Resolver{Home: home, Host: "box"}).Liveness(rec); got != run.LivenessAlive {
		t.Errorf("liveness = %q, want alive", got)
	}
	if _, err := os.Stat(filepath.Join(home, run.MachineFile)); !os.IsNotExist(err) {
		t.Errorf("a read created %s (err=%v)", run.MachineFile, err)
	}
}
