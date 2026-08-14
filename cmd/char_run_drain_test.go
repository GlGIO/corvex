package cmd

// The drain guard: the one test in this net whose job is to fail if a refactor
// SILENCES the `corvex run` renderer.
//
// ─────────────────────────────────────────────────────────────────────────────
// WHY THIS FILE EXISTS
// ─────────────────────────────────────────────────────────────────────────────
//
// cmd/run.go:171 starts the renderer with `go renderer.Drain(events)` and never
// closes `events` nor waits for the goroutine. Whether the queued lines reach
// stdout is decided by the Go scheduler. That anomaly is FROZEN on purpose
// (LEI 1 — see .corvex/tasks/rebrand/f-1-anomalias.md, entry "drain nao
// aguardado"); fixing it is post-F0 work.
//
// The consequence for the net is what this file repairs: because the lines may
// or may not arrive, runAssertRendererLines (char_run_test.go) treats "nothing
// arrived" as a t.Logf and passes. So a change in the TEXT or ORDER of renderer
// lines is caught, but a regression that silences the renderer completely would
// slip through green — and F0 refactors exactly the Run/executeTask loop and the
// event emission, which makes "renderer went quiet" a plausible F0 regression.
//
// ─────────────────────────────────────────────────────────────────────────────
// WHY THIS IS NOT SOLVED BY REPETITION ALONE
// ─────────────────────────────────────────────────────────────────────────────
//
// The obvious fix is "run it N times, fail if no iteration ever printed", sized
// so that (1-p)^N is astronomically small. That reasoning needs p to be a
// property of the code. It is not: p is a property of how much CPU the host has
// to spare. Measured on this machine (10 CPUs, 200 samples per row, one `corvex
// run --plain --yes` per sample, counting samples where ANY renderer line
// reached stdout):
//
//	scheduling                       lines reached stdout    p
//	GOMAXPROCS=1,  idle                        2 / 200      0.010
//	GOMAXPROCS=1,  8 CPU hogs                  0 / 200      0.000
//	GOMAXPROCS=2,  idle                      199 / 200      0.995
//	GOMAXPROCS=2,  8 CPU hogs                  2 / 200      0.010
//	GOMAXPROCS=2,  32 CPU hogs                 1 / 200      0.005
//	GOMAXPROCS=4,  32 CPU hogs                 1 / 200      0.005
//	GOMAXPROCS=10, idle                  199-200 / 200      ~1.000
//
// At p=0 no N works, and at p=0.005 even N=200 leaves a 37% chance of a vacuous
// pass — which is the flaky test we were told not to write. Repetition is
// therefore the SECOND line of defence here, not the first.
//
// ─────────────────────────────────────────────────────────────────────────────
// WHAT MAKES IT DETERMINISTIC
// ─────────────────────────────────────────────────────────────────────────────
//
// runCLIAwait (characterize_test.go) waits, with a timeout, for stdout to carry
// the expected number of lines BEFORE it closes the capture pipe. The default
// harness closes that pipe the instant runRun returns, and that close is what
// truncates the drain; parking the closing goroutine for a moment is all the
// drain ever needed. This is a test-side wait: zero production files touched,
// zero goldens affected (renderer stdout is never in a golden — see
// runRendererPlaceholder). Same 200-sample measurement, with the await:
//
//	GOMAXPROCS=1,  idle                      200 / 200
//	GOMAXPROCS=1,  8 CPU hogs                200 / 200
//	GOMAXPROCS=2,  32 CPU hogs               200 / 200
//	GOMAXPROCS=10, idle                      200 / 200
//
// 800/800 across configurations where the unawaited path scored 0/200. The await
// does not paper over the production bug — the bug is that nobody waits, and the
// user still doesn't; this test simply stops the HARNESS from being the thing
// that drops the output.
//
// ─────────────────────────────────────────────────────────────────────────────
// CHOICE OF N
// ─────────────────────────────────────────────────────────────────────────────
//
// N is sized so the guard stays valid even if the await above is one day removed
// or broken, leaving nothing but the ambient scheduler. Using the measured rate
// on a healthy multi-P host, p = 0.995 (199/200 at GOMAXPROCS=2 idle, 200/200 at
// 10):
//
//	drainGuardIterations   = 64 → (1-0.995)^64 = 5e-148 chance of a vacuous pass
//	drainGuardRichIters    =  6 → (1-0.995)^6  = 1.6e-14
//
// The test also pins GOMAXPROCS to at least 2 for its duration, because the
// table above shows a single P is near-deterministic death for the drain and no
// N can rescue that — so on a 1-CPU CI box this guard would otherwise be exactly
// as weak as it is strong here.
//
// COST on this machine: the cheap scenario is ~18ms per iteration (64 → ~1.2s),
// the rich one ~390ms (6 → ~2.3s), so about 3.5s added to a ~33s package run.
// The cheap scenario is the cheapest `run` that emits anything at all: tasks.md
// with everything already PASSED, no spec.md (so the planner is never consulted,
// no anchor needed) and no git repo (so no git subprocess, only a WARN).
//
// ─────────────────────────────────────────────────────────────────────────────
// DO NOT "FIX" runAssertRendererLines
// ─────────────────────────────────────────────────────────────────────────────
//
// runAssertRendererLines keeps its t.Logf-on-empty branch DELIBERATELY. Turning
// it into t.Errorf would make ~15 call sites flake in proportion to CI load
// (rows above), and a suite that goes red at random teaches a refactorer to
// ignore red — a worse outcome than the gap it closes. Those call sites keep
// their cheap "text and order did not change" assert; the "nothing came out at
// all" question is answered here, once, with the await + N above.

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// drainGuardTimeout is how long runCLIAwait waits for the expected lines before
// giving up and reporting whatever arrived. Generous on purpose: it is only ever
// reached when the renderer really did go quiet, i.e. when this test is about to
// fail anyway, so paying it costs nothing on a green run (the wait exits as soon
// as the lines land, ~ms).
const drainGuardTimeout = 3 * time.Second

const (
	drainGuardIterations = 64
	drainGuardRichIters  = 6

	// drainGuardBailAfter stops the loop early once this many CONSECUTIVE
	// iterations came back with an empty stdout. Purely about the cost of a red
	// run: with the renderer silenced every iteration pays the full
	// drainGuardTimeout, so the unbailed guard takes 215s to fail instead of
	// 3.5s to pass — slow enough that someone would "just skip it". The margin is
	// unchanged in practice: 8 consecutive empties at the measured fallback rate
	// p=0.995 is (1-0.995)^8 = 3.9e-19, the same astronomical order as the full
	// N. The bail only ever triggers on the way to a failure.
	drainGuardBailAfter = 8
)

// drainGuardCheck compares the renderer lines that arrived against want and
// reports how many arrived. Text/order mismatches are hard failures (those are
// deterministic — the scheduler can truncate output, it cannot rewrite it); a
// short read is reported, not judged, and the caller decides.
func drainGuardCheck(t *testing.T, iter int, stdout string, want []string) int {
	t.Helper()

	got := strings.Split(stdout, "\n")
	if len(got) > 0 && got[len(got)-1] == "" {
		got = got[:len(got)-1]
	}
	if len(got) > len(want) {
		t.Errorf("iteration %d: renderer printed %d line(s), want at most %d\ngot:  %q\nwant: %q",
			iter, len(got), len(want), got, want)
		return len(got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("iteration %d: renderer line %d = %q, want %q\ngot:  %q\nwant: %q",
				iter, i+1, got[i], want[i], got, want)
			return len(got)
		}
	}
	return len(got)
}

// TestCharacterizeRunRendererNotSilenced is the guard described at the top of
// this file: it fails when `corvex run` stops printing through the renderer.
//
// Two scenarios, because they cover different emitters:
//
//   - the cheap one only proves the lifecycle events survive (plan ready, done),
//     which are emitted around the scheduler;
//   - the rich one proves the PER-TASK events survive (task start, task
//     complete), which is where F0's executeTask refactor lands. Without it, a
//     refactor that silences only per-task progress would keep this test green.
//
// Two distinct failure modes are reported separately: "no line at all in any
// iteration" (the renderer is silent) and "never once the complete set" (the
// tail is always missing), because they point at different regressions.
func TestCharacterizeRunRendererNotSilenced(t *testing.T) {
	// See "CHOICE OF N": one P is near-deterministic death for the unawaited
	// drain, so the fallback line of defence needs at least two. Raising
	// GOMAXPROCS above NumCPU is legal and still gives the drain a P to run on,
	// which is why this also holds on a 1-CPU CI runner.
	if prev := runtime.GOMAXPROCS(0); prev < 2 {
		defer runtime.GOMAXPROCS(prev)
		runtime.GOMAXPROCS(2)
	}

	cheapWant := []string{"+ plan ready (1 tasks)", "+ done"}
	richWant := []string{
		"+ plan ready (1 tasks)",
		"> S01  First Task",
		"+ S01  passed . 2s . $0.04",
		"+ done",
	}

	// ── cheap scenario: the lifecycle events ─────────────────────────────────
	cheap := drainGuardLoop(t, "lifecycle", drainGuardIterations, cheapWant, func(t *testing.T) string {
		stubClaude(t, runStubRefuse)
		f := newFixture(t).AddProject("alpha", "", runTasksAllPassedMD)

		stdout, _, err := runCLIAwait(t, f.Dir, len(cheapWant), drainGuardTimeout,
			"run", "alpha", "--plain", "--yes")
		if err != nil {
			t.Fatalf("run must succeed on an all-PASSED project, got %v", err)
		}
		return stdout
	})
	switch {
	case cheap.nonEmpty == 0:
		t.Errorf("the PlainRenderer was SILENCED: %d iterations of `run alpha --plain --yes` produced not a single line on stdout; expected %q",
			cheap.ran, cheapWant)
	case cheap.complete == 0:
		t.Errorf("the PlainRenderer never printed its full output: in %d iterations no run produced all %d line(s) %q (a prefix arrived, the tail never did)",
			cheap.ran, len(cheapWant), cheapWant)
	}

	// ── rich scenario: the PER-TASK events ───────────────────────────────────
	rich := drainGuardLoop(t, "per-task", drainGuardRichIters, richWant, func(t *testing.T) string {
		stubClaude(t, runStubPass)
		f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
		runSeedAnchor(f, "alpha", fixtureSpecMD)

		stdout, _, err := runCLIAwait(t, f.Dir, len(richWant), drainGuardTimeout,
			"run", "alpha", "--plain", "--yes")
		if err != nil {
			t.Fatalf("the fake provider must pass the task, got %v", err)
		}
		return stdout
	})
	switch {
	case rich.nonEmpty == 0:
		t.Errorf("the PlainRenderer was SILENCED on the task path: %d iterations produced not a single line; expected %q",
			rich.ran, richWant)
	case rich.complete == 0:
		t.Errorf("the PlainRenderer never printed the per-task lines: in %d iterations no run produced all %d line(s) %q — a refactor that stops emitting EventTaskStart/EventTaskComplete looks exactly like this",
			rich.ran, len(richWant), richWant)
	}
}

// drainGuardTally is what one repetition phase learned.
type drainGuardTally struct {
	ran      int // iterations actually executed (< n when the bail fired)
	nonEmpty int // iterations where at least one renderer line arrived
	complete int // iterations where the whole expected set arrived
}

// drainGuardLoop runs invoke up to n times, asserting text and order every time
// and tallying how much output arrived. It stops early after
// drainGuardBailAfter consecutive empty iterations — see that constant.
func drainGuardLoop(t *testing.T, label string, n int, want []string, invoke func(*testing.T) string) drainGuardTally {
	t.Helper()

	var tally drainGuardTally
	consecutiveEmpty := 0
	for i := 0; i < n; i++ {
		tally.ran++
		switch lines := drainGuardCheck(t, i, invoke(t), want); {
		case lines == len(want):
			tally.complete++
			tally.nonEmpty++
			consecutiveEmpty = 0
		case lines > 0:
			tally.nonEmpty++
			consecutiveEmpty = 0
		default:
			consecutiveEmpty++
		}
		if consecutiveEmpty >= drainGuardBailAfter {
			t.Logf("%s: bailing out after %d consecutive empty iterations (of %d planned) — the renderer is silent, no point paying the timeout %d more times",
				label, consecutiveEmpty, n, n-tally.ran)
			break
		}
	}
	t.Logf("%s events: %d/%d iterations printed at least one line, %d/%d printed all %d",
		label, tally.nonEmpty, tally.ran, tally.complete, tally.ran, len(want))
	return tally
}
