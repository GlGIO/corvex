package run

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// Liveness is what a reader can honestly conclude about a run it did not start.
type Liveness string

const (
	// LivenessAlive: process exists and the heartbeat is fresh.
	LivenessAlive Liveness = "alive"
	// LivenessStale: the process exists but its heartbeat cannot be trusted —
	// older than StaleAfter, or dated in the future beyond FutureSkew. Either the
	// run is wedged, the machine was suspended, the clock moved, or the pid now
	// belongs to somebody else. Deliberately not "alive" and not "dead".
	LivenessStale Liveness = "stale"
	// LivenessCanceling: a stop was requested and the process is still there.
	//
	// How it appears to a reader, and why this is the least misleading answer: it
	// is NOT alive, because the run is on its way out and counting it as capacity
	// or asking it to stop again is wrong; it is NOT dead, because the pid is
	// still there and the work may still be unwinding (killing it, or reusing
	// whatever it holds, would be acting on a false conclusion). It also wins
	// over freshness on purpose: "somebody asked this to stop" does not stop
	// being true when the heartbeat ages, and reporting `stale` instead would
	// throw away the one fact a supervisor needs while adding nothing — the age
	// of updated_at is right there in the record for anyone who wants it.
	LivenessCanceling Liveness = "canceling"
	// LivenessDead: no such process, and the run never recorded an end. This is
	// the SIGKILL case — status stays "running" on disk forever.
	LivenessDead Liveness = "dead"
	// LivenessFinished: the run recorded a terminal status itself.
	LivenessFinished Liveness = "finished"
	// LivenessUnknown: not decidable here — no pid, or a record from another
	// host whose pids mean nothing on this machine.
	LivenessUnknown Liveness = "unknown"
)

// ProcessProbe reports whether a pid currently exists. Injectable so tests can
// describe a dead or a live process without spawning one.
type ProcessProbe func(pid int) bool

// ProcessAlive is the production probe: signal 0 tests for existence without
// delivering anything.
//
// EPERM means the pid exists but belongs to another user, which is still
// "exists" — reporting dead there would be a lie. Anything else (ESRCH, or a
// platform that refuses signal 0) reports dead.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	return true
}

// Resolver answers "what is alive" for a second process: it owns no run and
// mutates nothing.
//
// Reporting a run dead is explicitly not the same as fixing it. This type never
// writes, never rewrites another process's record and never kills a pid — a
// reader that "repairs" state it does not own turns a wrong guess about liveness
// into permanent damage.
type Resolver struct {
	// Home overrides the corvex home; empty resolves via Home().
	Home string
	// Now defaults to time.Now.
	Now func() time.Time
	// Alive defaults to ProcessAlive.
	Alive ProcessProbe
	// StaleAfter defaults to DefaultStaleAfter.
	StaleAfter time.Duration
	// FutureSkew defaults to DefaultFutureSkew.
	FutureSkew time.Duration
	// Host defaults to the machine hostname.
	Host string
	// Machine defaults to the machine id stored in Home. Empty (here or in the
	// record) falls back to comparing hostnames.
	Machine string
}

// Liveness resolves one record.
//
// Order matters: a run that recorded its own end is finished regardless of what
// pid it used to have (pids get recycled, and a finished run must not come back
// as "alive" because some unrelated process inherited its number).
//
// The rest of the order is the same argument applied downwards: a pid that is
// gone answers the question on its own (`dead`), a run that was asked to stop is
// reported as such for as long as its process is still there (`canceling`, which
// deliberately outranks freshness — see LivenessCanceling), and only a run nobody
// has interfered with is judged on its heartbeat.
//
// # Why both pid and heartbeat
//
// Neither signal alone is enough, and they fail in opposite directions:
//
//   - status alone is blind to SIGKILL — the run never gets to write "failed",
//     so `running` sticks forever;
//   - the pid alone is blind to pid reuse — after enough process churn or a
//     reboot, somebody else's pid answers signal 0 and a long-dead run looks
//     alive;
//   - the heartbeat alone is slow — a run killed one second ago still has a
//     fresh updated_at for a full StaleAfter window.
//
// Requiring *both* (pid exists AND heartbeat fresh) makes each cover the other:
// death is detected within one probe, and a recycled pid can only fake "alive"
// while the heartbeat is also fresh — but the only thing that refreshes that
// heartbeat is the run's own process, which by definition is gone.
//
// # Why freshness is clamped on both sides
//
// "Older than StaleAfter" is only half a test, and the missing half made this
// file's own guarantee false. A one-sided `now - freshness > staleAfter` treats a
// timestamp in the FUTURE as maximally fresh: the difference is negative, the
// threshold is never crossed, and a record whose updated_at reads an hour from
// now is `alive` for as long as its pid keeps answering — permanently, on a
// recycled pid, for a run that died weeks ago. It takes no exotic failure to get
// there: NTP correcting a drift, a VM snapshot resumed, an RTC waking up wrong.
//
// So a freshness more than FutureSkew ahead of now is treated as suspect, not as
// fresh, and lands in LivenessStale — the pid does exist, so `dead` would be a
// lie; what cannot be trusted is the record's idea of time. The tolerance is not
// zero because it does not have to be: writer and reader are normally two
// processes on one machine reading one clock, so legitimate disagreement is
// sub-second, and a foreign machine's record never reaches this comparison (the
// machine guard rejects it first). With the clamp, the promise below holds in both
// directions: a recycled pid degrades to LivenessStale within StaleAfter, and
// there is no permanent false "alive".
//
// # Residual risk, accepted knowingly
//
// Inside the StaleAfter window (40s by default) a pid recycled onto an
// unrelated process is indistinguishable from the original run: last heartbeat
// still recent, pid still answering. Cheap fixes were considered and rejected:
// comparing started_at against the process's own start time needs sysctl on
// darwin and /proc parsing on linux — platform-specific code for a 40-second
// window on a single-user machine — and pid churn fast enough to wrap within
// 40s while landing exactly on a recorded run is not a scenario worth that
// code. Consequence accepted: a run may read "alive" for up to StaleAfter after
// it actually died. Nothing destructive hangs off this bit (we never kill or
// rewrite based on it), so the worst outcome is a listing that is 40s late.
// If the F7 supervisor ever acts on liveness automatically, revisit this.
func (r Resolver) Liveness(rec Record) Liveness {
	if rec.Status.IsTerminal() {
		return LivenessFinished
	}
	if rec.PID <= 0 {
		return LivenessUnknown
	}
	if !r.sameMachine(rec) {
		return LivenessUnknown
	}
	if !r.probe()(rec.PID) {
		return LivenessDead
	}
	if rec.Status == StatusCanceling {
		return LivenessCanceling
	}
	if age := r.now().Sub(rec.Freshness()); age > r.staleAfter() || age < -r.futureSkew() {
		return LivenessStale
	}
	return LivenessAlive
}

// sameMachine reports whether rec's pid can be interpreted here at all.
//
// The machine id decides when both sides have one; the hostname is the fallback
// for records written before machine ids existed, normalised so that the parts of
// a name the network rewrites do not count (see normalizeHost).
//
// No comparable identity on either side means "not decidable", not "local". A
// record with an empty host used to sail straight past this guard and get its pid
// probed against this machine — the audit saw `host= -> alive` on a recycled pid.
// Absence of identity is not evidence of locality, and the cost of the honest
// answer is one row reading `unknown`.
func (r Resolver) sameMachine(rec Record) bool {
	// Only worth reading our own machine id when the record has one to compare
	// against: this runs once per row of a listing.
	if rec.Machine != "" {
		if me := r.machine(); me != "" {
			return rec.Machine == me
		}
	}
	recHost, myHost := normalizeHost(rec.Host), normalizeHost(r.host())
	if recHost != "" && myHost != "" {
		return recHost == myHost
	}
	return false
}

func (r Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r Resolver) probe() ProcessProbe {
	if r.Alive != nil {
		return r.Alive
	}
	return ProcessAlive
}

func (r Resolver) staleAfter() time.Duration {
	if r.StaleAfter > 0 {
		return r.StaleAfter
	}
	return DefaultStaleAfter
}

func (r Resolver) futureSkew() time.Duration {
	if r.FutureSkew > 0 {
		return r.FutureSkew
	}
	return DefaultFutureSkew
}

func (r Resolver) host() string {
	if r.Host != "" {
		return r.Host
	}
	return hostname()
}

// machine reads this machine's id without creating it: a Resolver mutates
// nothing, including nothing in the corvex home.
func (r Resolver) machine() string {
	if r.Machine != "" {
		return r.Machine
	}
	home := r.Home
	if home == "" {
		resolved, err := Home()
		if err != nil {
			return ""
		}
		home = resolved
	}
	return readMachineID(home)
}

var (
	hostOnce sync.Once
	hostName string
)

// hostname is cached: it is read once per process and never changes, and
// Liveness is called once per record in a listing.
func hostname() string {
	hostOnce.Do(func() {
		if h, err := os.Hostname(); err == nil {
			hostName = h
		}
	})
	return hostName
}
