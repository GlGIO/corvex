package run_test

import (
	"errors"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// TestHeartbeatBeatsOnInjectedTicks drives the loop from the test instead of
// from the clock: no sleeping for an interval, no timing-dependent assertion.
func TestHeartbeatBeatsOnInjectedTicks(t *testing.T) {
	ticks := make(chan time.Time)
	beat := make(chan struct{}, 4)
	hb := run.StartHeartbeat(run.HeartbeatOptions{
		Ticks: ticks,
		Beat:  func() error { beat <- struct{}{}; return nil },
	})

	for i := 0; i < 3; i++ {
		ticks <- time.Now()
		<-beat
	}
	// Stop returns only once the goroutine has exited, which is what makes the
	// absence of a leak an assertion rather than a hope.
	hb.Stop()
	if got := hb.Beats(); got != 3 {
		t.Errorf("beats = %d, want 3", got)
	}
	if err := hb.LastErr(); err != nil {
		t.Errorf("LastErr = %v, want nil", err)
	}
	hb.Stop() // idempotent: a deferred Stop next to an explicit one must not panic
}

func TestHeartbeatStopBeforeAnyTick(t *testing.T) {
	hb := run.StartHeartbeat(run.HeartbeatOptions{
		Ticks: make(chan time.Time),
		Beat:  func() error { t.Error("beat after no tick"); return nil },
	})
	hb.Stop()
	if got := hb.Beats(); got != 0 {
		t.Errorf("beats = %d, want 0", got)
	}
}

func TestHeartbeatClosedTickerEndsTheLoop(t *testing.T) {
	ticks := make(chan time.Time)
	hb := run.StartHeartbeat(run.HeartbeatOptions{Ticks: ticks, Beat: func() error { return nil }})
	close(ticks)
	hb.Stop() // returns only if the goroutine noticed the closed channel and exited
}

// TestHeartbeatUsesItsOwnTicker covers the production branch (a real
// time.Ticker) with a tiny interval, synchronising on beats rather than on a
// fixed sleep.
func TestHeartbeatUsesItsOwnTicker(t *testing.T) {
	beat := make(chan struct{}, 8)
	hb := run.StartHeartbeat(run.HeartbeatOptions{
		Interval: time.Millisecond,
		Beat: func() error {
			select {
			case beat <- struct{}{}:
			default:
			}
			return nil
		},
	})
	defer hb.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-beat:
		case <-time.After(10 * time.Second):
			t.Fatalf("no heartbeat after %d beats", i)
		}
	}
}

func TestHeartbeatKeepsBeatingAfterAnError(t *testing.T) {
	ticks := make(chan time.Time)
	done := make(chan struct{}, 2)
	boom := errors.New("disk full")
	hb := run.StartHeartbeat(run.HeartbeatOptions{
		Ticks: ticks,
		Beat:  func() error { done <- struct{}{}; return boom },
	})
	ticks <- time.Now()
	<-done
	ticks <- time.Now()
	<-done
	hb.Stop()

	if got := hb.Beats(); got != 2 {
		t.Errorf("beats = %d, want 2 — a failing heartbeat must not stop the loop", got)
	}
	if !errors.Is(hb.LastErr(), boom) {
		t.Errorf("LastErr = %v, want %v", hb.LastErr(), boom)
	}
}

func TestHeartbeatNilAndNilBeatAreSafe(t *testing.T) {
	var nilHB *run.Heartbeat
	nilHB.Stop() // callers defer this before knowing whether the run started
	if nilHB.Beats() != 0 || nilHB.LastErr() != nil {
		t.Error("nil Heartbeat should report zero state")
	}

	ticks := make(chan time.Time, 1)
	hb := run.StartHeartbeat(run.HeartbeatOptions{Ticks: ticks})
	ticks <- time.Now()
	// Nothing to synchronise on when Beat is nil, so just make sure Stop works.
	hb.Stop()
}

func TestHeartbeatDefaultInterval(t *testing.T) {
	if run.DefaultHeartbeatInterval != 10*time.Second {
		t.Errorf("DefaultHeartbeatInterval = %v, want 10s (documented in heartbeat.go)",
			run.DefaultHeartbeatInterval)
	}
}

// TestHandleHeartbeatRefreshesTheRecord is the end-to-end wiring: the ticker
// belongs to the caller, the record on disk is what changes.
func TestHandleHeartbeatRefreshesTheRecord(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	at := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	h, err := run.Registry{Repo: repo, Home: home, Now: fixedClock(at, time.Second),
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	beat := make(chan struct{}, 1)
	ticks := make(chan time.Time)
	hb := run.StartHeartbeat(run.HeartbeatOptions{Ticks: ticks, Beat: func() error {
		err := h.Touch()
		beat <- struct{}{}
		return err
	}})
	ticks <- time.Now()
	<-beat
	hb.Stop()

	rec, err := run.ReadRecord(repo, "run_8f21")
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if !rec.UpdatedAt.After(at) {
		t.Errorf("updated_at = %v, want later than %v", rec.UpdatedAt, at)
	}
	if !rec.StartedAt.Equal(at) {
		t.Errorf("started_at = %v, want it untouched at %v", rec.StartedAt, at)
	}
	if rec.Status != run.StatusRunning {
		t.Errorf("status = %q, want the heartbeat to leave it alone", rec.Status)
	}

	// The convenience wiring on Handle must also stop cleanly.
	live := h.StartHeartbeat(time.Millisecond)
	live.Stop()
}
