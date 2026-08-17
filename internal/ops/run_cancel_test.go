package ops

// Cancellation, measured the way the auditor measured it: a real process, a real
// SIGINT, and a real orphaned grandchild holding the stdout pipe so the body
// never returns.
//
// The frozen teardown bug (kill only the direct child) is reproduced HERE, in
// test code, on purpose: it is not being fixed, and it is precisely what makes
// the on-disk record the only thing that can tell an operator the truth between
// the Ctrl-C and the close. Before the fix the record said `running`, updated_at
// kept advancing, and a second process read `alive` for as long as anyone cared
// to look.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/run"
)

const (
	opsHelperEnv     = "CORVEX_OPS_TEST_HELPER"
	opsHelperHome    = "CORVEX_OPS_TEST_HOME"
	opsHelperRepo    = "CORVEX_OPS_TEST_REPO"
	opsHelperOut     = "CORVEX_OPS_TEST_OUT"
	opsHelperPidFile = "CORVEX_OPS_TEST_PIDFILE"
)

// opsHelperMain is the child side of the protocol. TestMain dispatches here and
// exits, so no test runs in the child and the child prints no testing output.
func opsHelperMain(mode string) int {
	switch mode {
	case "cancelhold":
		return helperCancelHold()
	default:
		fmt.Fprintln(os.Stderr, "unknown ops helper mode:", mode)
		return 2
	}
}

// helperCancelHold starts a real run, then blocks in its body the way a run with
// an orphaned grandchild blocks: forever. It closes nothing on SIGINT — the
// signal only cancels the context, which is all the product does.
func helperCancelHold() int {
	home, repo := os.Getenv(opsHelperHome), os.Getenv(opsHelperRepo)
	out, pidFile := os.Getenv(opsHelperOut), os.Getenv(opsHelperPidFile)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	r, err := NewRunner(RunRequest{
		Config:  config.Default(),
		Project: "demo",
		WorkDir: repo,
		Repo:    repo,
		// A fast heartbeat, so the record is visibly being refreshed while the
		// run is winding down: the new state has to win over a fresh heartbeat,
		// not merely over a stale one.
		HeartbeatInterval: 50 * time.Millisecond,
		Registry:          &run.Registry{Home: home, Repo: repo},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper NewRunner:", err)
		return 2
	}
	// Readiness is announced from inside the body, once the grandchild exists.
	// Announcing it earlier lets the parent's SIGINT land before there is anything
	// holding the pipe, and then the run unwinds normally and the test proves
	// nothing — measured, not imagined.
	ready := func() error {
		buf, err := json.Marshal(r.Record())
		if err != nil {
			return err
		}
		return os.WriteFile(out, buf, 0o644)
	}

	_ = r.Execute(ctx, func(ctx context.Context) error { return holdPastCancellation(ctx, pidFile, ready) })
	return 0
}

// holdPastCancellation is the auditor's scenario as a body: a child process
// holding the run's stdout pipe, plus a grandchild that survives it. On
// cancellation only the direct child is signalled — the frozen bug — so the
// grandchild keeps the write end open and the read below never finishes.
func holdPastCancellation(ctx context.Context, pidFile string, ready func() error) error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pr.Close()

	child := exec.Command("/bin/sh", "-c",
		`sleep 20 & echo $! > "$1"; exec sleep 20`, "sh", pidFile)
	child.Stdout = pw
	if err := child.Start(); err != nil {
		pw.Close()
		return err
	}
	pw.Close()

	// Wait for the grandchild to have been forked and to have recorded its pid:
	// only then is the pipe held by something the teardown will not kill.
	for i := 0; i < 500; i++ {
		if data, err := os.ReadFile(pidFile); err == nil && len(data) > 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("cancelled before the scenario was set up")
	}
	if err := ready(); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = child.Process.Signal(os.Interrupt) // only the direct child: LEI 1
	}()
	_ = child.Wait()

	// The orphan still holds the write end, so this blocks exactly as the run the
	// auditor measured blocked.
	_, _ = io.Copy(io.Discard, pr)
	return nil
}

// TestSIGINTReachesDiskBeforeTheRunUnwinds is the regression test for the run
// that read `alive` indefinitely after Ctrl-C.
func TestSIGINTReachesDiskBeforeTheRunUnwinds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals and /bin/sh")
	}
	home, repo := t.TempDir(), t.TempDir()
	scratch := t.TempDir()
	out := filepath.Join(scratch, "record.json")
	pidFile := filepath.Join(scratch, "grandchild.pid")

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	logFile, err := os.Create(filepath.Join(scratch, "helper.log"))
	if err != nil {
		t.Fatalf("helper log: %v", err)
	}
	defer logFile.Close()

	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(),
		opsHelperEnv+"=cancelhold",
		opsHelperHome+"="+home,
		opsHelperRepo+"="+repo,
		opsHelperOut+"="+out,
		opsHelperPidFile+"="+pidFile,
	)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		killRecordedPID(pidFile)
		if data, err := os.ReadFile(filepath.Join(scratch, "helper.log")); err == nil && len(data) > 0 {
			t.Logf("helper output: %s", data)
		}
	})

	rec := waitForRecord(t, out)
	if rec.Status != run.StatusRunning {
		t.Fatalf("the helper's run starts as %q, want running", rec.Status)
	}
	reader := run.Resolver{Home: home}
	if v, ok, err := reader.Get(rec.RunID); err != nil || !ok || v.Liveness != run.LivenessAlive {
		t.Fatalf("before the signal: liveness = %q (found=%v, err=%v), want alive — "+
			"the test has to start from a run a second process agrees is up", v.Liveness, ok, err)
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}

	// A supervisor asks again. It must stop being told `alive` promptly, without
	// waiting for the run to unwind — which, thanks to the orphan, never happens.
	deadline := time.Now().Add(5 * time.Second)
	var last run.View
	for time.Now().Before(deadline) {
		v, ok, err := reader.Get(rec.RunID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if ok {
			last = v
			if v.Liveness != run.LivenessAlive {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The whole point: the process is STILL THERE. If it had exited, the pid
	// probe would have answered the question and the record would prove nothing.
	if !run.ProcessAlive(cmd.Process.Pid) {
		t.Fatalf("the helper exited on its own: the scenario did not reproduce, "+
			"liveness=%q status=%q", last.Liveness, last.Record.Status)
	}
	if last.Liveness == run.LivenessAlive {
		t.Errorf("5s after SIGINT a second process still reads %q for a run that was asked to stop "+
			"(status %q, updated_at %s): between the Ctrl-C and the close there is no way to tell "+
			"'running' from 'already asked to stop'",
			last.Liveness, last.Record.Status, last.Record.UpdatedAt.Format(time.RFC3339))
	}
	if last.Alive() {
		t.Error("View.Alive() is true for a run that is winding down")
	}
	// The on-disk contract, not the constant: this is the string a second process
	// parses out of JSON, and it is what has to be right.
	if got := string(last.Record.Status); got != "canceling" {
		t.Errorf("status on disk = %q, want canceling — the state must say a stop was requested, "+
			"and it must say so at the moment the signal arrived, not when the body returns", got)
	}
	if !last.Record.UpdatedAt.After(rec.UpdatedAt) {
		t.Errorf("updated_at did not move (%s): the state change never reached disk",
			last.Record.UpdatedAt.Format(time.RFC3339Nano))
	}

	// And the heartbeat does not undo it. It is still beating (50ms interval), so
	// a state that merely looked stale would flip back to alive here.
	time.Sleep(300 * time.Millisecond)
	after, ok, err := reader.Get(rec.RunID)
	if err != nil || !ok {
		t.Fatalf("second read: %v, %v", ok, err)
	}
	if after.Liveness == run.LivenessAlive {
		t.Errorf("liveness went back to %q: the heartbeat is overwriting the state instead of "+
			"the state winning the resolution", after.Liveness)
	}
	if !after.Record.UpdatedAt.After(last.Record.UpdatedAt) {
		t.Logf("note: updated_at did not advance (%s -> %s); the heartbeat may have stopped, "+
			"which is the other acceptable answer",
			last.Record.UpdatedAt.Format(time.RFC3339Nano), after.Record.UpdatedAt.Format(time.RFC3339Nano))
	}
}

func waitForRecord(t *testing.T, path string) run.Record {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			var rec run.Record
			if err := json.Unmarshal(data, &rec); err != nil {
				t.Fatalf("decoding %s: %v (%s)", path, err, data)
			}
			return rec
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for the helper's record at %s", path)
	return run.Record{}
}

// killRecordedPID reaps the orphaned grandchild so the suite leaves no process
// behind. It would exit on its own in 20s; a test that litters the machine for
// 20s at a time is still litter.
func killRecordedPID(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// ---------------------------------------------------------------------------
// The cancellation watcher, guarded in-process.
//
// TestExecuteLeavesNoHeartbeatBehind does not guard it. It passes
// context.Background(), whose Done() is nil, so recordCancellation returns the
// no-op branch and the goroutine under test is never created. Proven by mutation:
// with the body of stopWatching replaced by a no-op — never closes quit, never
// waits — `go test ./internal/ops/` stayed green.
//
// A context that CAN be cancelled is what makes the goroutine exist. From there
// two things are worth asserting, and the second is the sharper of the two:
// the goroutine ends, and it ends BEFORE Execute returns.
// ---------------------------------------------------------------------------

// TestExecuteStopsTheCancellationWatcherOnASuccessfulRun: the watcher is created
// for every cancellable context, including the overwhelming majority of runs where
// no cancellation ever arrives. Nothing else will ever wake it, so if Execute does
// not close its quit channel the goroutine is parked on a select for the lifetime
// of the process — one leak per run in a long-lived supervisor.
func TestExecuteStopsTheCancellationWatcherOnASuccessfulRun(t *testing.T) {
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	before := goroutineCount()
	if err := r.Execute(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// goroutineCount settles the scheduler, so a goroutine on its way out is not
	// miscounted; anything still there is parked, not finishing.
	if leaked := goroutineCount() - before; leaked > 0 {
		t.Errorf("%d goroutine(s) still running after Execute returned on a run that was "+
			"never cancelled: the cancellation watcher is never told to stop", leaked)
	}
}

// TestExecuteStopsTheWatcherBeforeWritingTheTerminalStatus is the assertion with
// teeth, and it needs no timing window to catch anything.
//
// The context is cancelled AFTER Execute has returned. If the watcher were still
// alive at that point it would wake up and write `canceling` over the terminal
// status of a run that finished cleanly — a run that is done, on disk, reported as
// winding down, forever. So this test fails for a watcher that outlives Execute
// even by a millisecond, and it fails deterministically rather than in the
// nanoseconds-wide window a same-goroutine ordering test would depend on.
func TestExecuteStopsTheWatcherBeforeWritingTheTerminalStatus(t *testing.T) {
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Execute(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rec, err := run.ReadRecord(repo, r.RunID); err != nil {
		t.Fatalf("ReadRecord: %v", err)
	} else if rec.Status != run.StatusDone {
		t.Fatalf("status = %q, want done", rec.Status)
	}

	// The operator's Ctrl-C, one moment too late.
	cancel()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec, err := run.ReadRecord(repo, r.RunID)
		if err != nil {
			t.Fatalf("ReadRecord: %v", err)
		}
		if rec.Status != run.StatusDone {
			t.Fatalf("status became %q after Execute returned and the context was cancelled: "+
				"the watcher outlived the run and wrote over its terminal status", rec.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestExecuteRecordsCanceledNotCancelingWhenTheBodyObservesTheCancellation closes
// the pair from the other side: the watcher's write must be visible while the run is
// winding down, and must NOT be what the run ends on. `canceling` means "a stop was
// requested"; a run that has finished unwinding has to say `canceled`, or every
// reader is left unable to tell a run that is still going from one that stopped.
func TestExecuteRecordsCanceledNotCancelingWhenTheBodyObservesTheCancellation(t *testing.T) {
	repo := t.TempDir()
	r, err := NewRunner(identityRequest(t, repo, "alpha"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	before := goroutineCount()
	saw := ""
	err = r.Execute(ctx, func(c context.Context) error {
		cancel()
		<-c.Done()
		// The watcher races the body here on purpose: whatever it wrote, it wrote
		// while the run was still unwinding.
		for i := 0; i < 100; i++ {
			if rec, rerr := run.ReadRecord(repo, r.RunID); rerr == nil &&
				rec.Status == run.StatusCanceling {
				saw = string(rec.Status)
				break
			}
			time.Sleep(time.Millisecond)
		}
		return c.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Execute returned %v, want the context's own error", err)
	}
	if saw != "canceling" {
		t.Errorf("the record never said canceling while the run was winding down (saw %q): "+
			"between the Ctrl-C and the close there is nothing on disk to read", saw)
	}
	rec, err := run.ReadRecord(repo, r.RunID)
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if rec.Status != run.StatusCanceled {
		t.Errorf("final status = %q, want canceled — `canceling` landed on top of the "+
			"terminal write, which is the ordering Execute exists to guarantee", rec.Status)
	}
	if leaked := goroutineCount() - before; leaked > 0 {
		t.Errorf("%d goroutine(s) outlived a cancelled Execute", leaked)
	}
}
