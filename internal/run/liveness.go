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
	// LivenessStale: process exists but the heartbeat is older than StaleAfter.
	// Either the run is wedged, the machine was suspended, or the pid now
	// belongs to somebody else. Deliberately not "alive" and not "dead".
	LivenessStale Liveness = "stale"
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
	// Host defaults to the machine hostname.
	Host string
}

// Liveness resolves one record.
//
// Order matters: a run that recorded its own end is finished regardless of what
// pid it used to have (pids get recycled, and a finished run must not come back
// as "alive" because some unrelated process inherited its number).
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
// heartbeat is the run's own process, which by definition is gone. So a
// recycled pid degrades to LivenessStale after at most StaleAfter, never to a
// permanent false "alive".
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
	if host := r.host(); host != "" && rec.Host != "" && rec.Host != host {
		return LivenessUnknown
	}
	if !r.probe()(rec.PID) {
		return LivenessDead
	}
	if r.now().Sub(rec.Freshness()) > r.staleAfter() {
		return LivenessStale
	}
	return LivenessAlive
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

func (r Resolver) host() string {
	if r.Host != "" {
		return r.Host
	}
	return hostname()
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
