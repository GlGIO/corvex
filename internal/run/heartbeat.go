package run

import (
	"sync"
	"time"
)

const (
	// DefaultHeartbeatInterval is how often a live run refreshes updated_at.
	//
	// Ten seconds: one ~300-byte atomic rewrite of a single file per run per
	// 10s is free in I/O terms, and it bounds how long a UI polling the index
	// can show a stale "last seen". Shorter buys nothing a human can perceive;
	// longer widens the window in which a dead run still looks recent.
	DefaultHeartbeatInterval = 10 * time.Second

	// DefaultStaleAfter is how old a heartbeat has to be before a run stops
	// counting as alive: four intervals, i.e. three consecutive missed beats.
	//
	// Derived from the interval on purpose. One missed beat is a GC pause, a
	// busy disk or a loaded machine; declaring a healthy run dead because a
	// laptop stalled for 11 seconds would be worse than reporting a dead one
	// late. Four also keeps the false-alive window bounded, which is what makes
	// the pid-reuse argument in liveness.go hold.
	DefaultStaleAfter = 4 * DefaultHeartbeatInterval

	// DefaultFutureSkew is how far ahead of now a record's freshness may be
	// before it stops counting as freshness at all.
	//
	// Five seconds, and the size barely matters as long as it is finite: the
	// writer and the reader are two processes on one machine reading one clock, so
	// honest disagreement is sub-second, and a record from another machine never
	// reaches the comparison. What matters is that the bound EXISTS — without it, a
	// timestamp from the future reads as maximally fresh forever. See
	// Resolver.Liveness.
	DefaultFutureSkew = 5 * time.Second
)

// HeartbeatOptions configures StartHeartbeat.
type HeartbeatOptions struct {
	// Beat is the work done on each tick — normally Handle.Touch.
	Beat func() error

	// Interval defaults to DefaultHeartbeatInterval. Ignored when Ticks is set.
	Interval time.Duration

	// Ticks replaces the internal ticker. Tests drive beats by sending on their
	// own channel, so no test has to sleep for a real interval and no test
	// outcome depends on wall-clock timing.
	Ticks <-chan time.Time
}

// Heartbeat is a goroutine calling Beat on every tick until Stop.
type Heartbeat struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once

	mu      sync.Mutex
	beats   int
	lastErr error
}

// StartHeartbeat launches the beat loop. The returned Heartbeat must be
// stopped; Stop is the only way the goroutine ends, and it waits for it.
func StartHeartbeat(opts HeartbeatOptions) *Heartbeat {
	hb := &Heartbeat{stop: make(chan struct{}), done: make(chan struct{})}

	ticks := opts.Ticks
	var ticker *time.Ticker
	if ticks == nil {
		interval := opts.Interval
		if interval <= 0 {
			interval = DefaultHeartbeatInterval
		}
		ticker = time.NewTicker(interval)
		ticks = ticker.C
	}

	go func() {
		defer close(hb.done)
		if ticker != nil {
			defer ticker.Stop()
		}
		for {
			// Stop wins over a pending tick: a run that has just recorded its
			// terminal status must not have updated_at refreshed afterwards.
			select {
			case <-hb.stop:
				return
			default:
			}
			select {
			case <-hb.stop:
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
				hb.beat(opts.Beat)
			}
		}
	}()
	return hb
}

// Stop ends the loop and waits for the goroutine to exit. Idempotent, and safe
// on a nil Heartbeat so callers can defer it unconditionally.
func (hb *Heartbeat) Stop() {
	if hb == nil {
		return
	}
	hb.once.Do(func() { close(hb.stop) })
	<-hb.done
}

// Beats is how many times Beat has been called.
func (hb *Heartbeat) Beats() int {
	if hb == nil {
		return 0
	}
	hb.mu.Lock()
	defer hb.mu.Unlock()
	return hb.beats
}

// LastErr is the most recent error returned by Beat, if any.
//
// A failing heartbeat is not fatal to the run — losing the ability to refresh
// updated_at degrades the run to "looks stale", it does not make the work
// wrong — so errors are collected for the caller to surface instead of being
// printed (this package has no output) or panicked on.
func (hb *Heartbeat) LastErr() error {
	if hb == nil {
		return nil
	}
	hb.mu.Lock()
	defer hb.mu.Unlock()
	return hb.lastErr
}

func (hb *Heartbeat) beat(fn func() error) {
	var err error
	if fn != nil {
		err = fn()
	}
	hb.mu.Lock()
	hb.beats++
	if err != nil {
		hb.lastErr = err
	}
	hb.mu.Unlock()
}
