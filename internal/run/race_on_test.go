//go:build race

package run_test

// raceDetector reports whether this test binary is instrumented.
//
// It exists for one test: TestNoLiveIDIsRecycledUnderConcurrentRotation drives
// several real processes hard enough to keep a rotation permanently in flight, and
// the instrumentation slows every one of them by an order of magnitude — the load
// generators no longer keep the index small, so the claim loop reads a file that
// grows faster than the rotator can shrink it. The experiment is about a rename
// racing a read ACROSS PROCESSES, which the race detector cannot observe in any
// case (it instruments one process, and the contended object is a file), so the
// instrumented build runs a smaller sample of the same experiment instead of a
// slower one that would time out.
const raceDetector = true
