package ops

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// killFixture writes a record by hand so the pid can be one this test owns.
// Registry.Start would record os.Getpid(), and a test that signals its own
// process kills the test binary.
func killFixture(t *testing.T, pid int, status run.Status, started time.Time) (RunLister, string, string) {
	t.Helper()
	repo, home := t.TempDir(), t.TempDir()
	const id = "run_ab12"
	rec := run.Record{
		RunID: id, Repo: repo, Project: "alpha", PID: pid,
		Machine: "test-machine", Status: status,
		StartedAt: started, UpdatedAt: started,
	}
	if err := run.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	if err := run.AppendIndex(home, rec, started); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	now := started.Add(time.Second)
	resolver := run.Resolver{
		Home: home, Machine: "test-machine",
		Now:   func() time.Time { return now },
		Alive: func(int) bool { return true },
	}
	return RunLister{Resolver: resolver, Now: func() time.Time { return now }}, repo, id
}

// The whole point of `run kill`: it really signals, and the process really dies.
func TestKillRun_SignalsTheProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics differ on windows; the CLI path is unix-only today")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the victim process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	lister, _, id := killFixture(t, cmd.Process.Pid, run.StatusRunning, time.Now().UTC())
	res, err := lister.KillRun(id, KillOptions{Prove: false})
	if err != nil {
		t.Fatalf("KillRun: %v", err)
	}
	if res.PID != cmd.Process.Pid {
		t.Errorf("signalled pid %d, want %d", res.PID, cmd.Process.Pid)
	}
	if res.Proved {
		t.Error("Proved is true with Prove=false — the flag must be reported honestly")
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process was still alive 5s after SIGTERM")
	}
}

// A finished run is refused rather than signalled: its pid belongs to whoever
// inherited the number. This is the guard that keeps the F1 pid-reuse window
// from becoming destructive.
func TestKillRun_RefusesARunThatIsNotAlive(t *testing.T) {
	lister, _, id := killFixture(t, 999999, run.StatusDone, time.Now().UTC())
	_, err := lister.KillRun(id, KillOptions{})
	if err == nil {
		t.Fatal("KillRun on a finished run returned nil")
	}
	// The assertion names the guard's own sentence, not a word that also appears
	// in the stdlib's "file already finished". The first version asserted
	// "finished" alone and stayed green with the guard deleted — a ratchet that
	// held nothing, found by an audit that deleted the guard to check.
	if !strings.Contains(err.Error(), "there is no live process to signal") {
		t.Errorf("error does not come from the liveness guard: %v", err)
	}
}

// Prove=true waits for the run's own heartbeat. Nothing else can write it, so a
// beat is proof the pid still belongs to this run.
func TestKillRun_ProofWaitsForAHeartbeat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics differ on windows")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the victim process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	started := time.Now().UTC()
	lister, repo, id := killFixture(t, cmd.Process.Pid, run.StatusRunning, started)

	beat := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		rec, err := run.ReadRecord(repo, id)
		if err == nil {
			rec.UpdatedAt = started.Add(10 * time.Second)
			_ = run.WriteRecord(rec)
		}
		close(beat)
	}()

	res, err := lister.KillRun(id, KillOptions{Prove: true, Timeout: 3 * time.Second, Poll: 10 * time.Millisecond})
	<-beat
	if err != nil {
		t.Fatalf("KillRun with proof: %v", err)
	}
	if !res.Proved {
		t.Error("Proved is false after a heartbeat was observed")
	}
}

// The control: without a beat the command refuses and names the escape hatch.
// If this passed with a no-op waitForBeat, the proof would be theatre.
func TestKillRun_RefusesWhenNoHeartbeatArrives(t *testing.T) {
	lister, _, id := killFixture(t, 999999, run.StatusRunning, time.Now().UTC())
	_, err := lister.KillRun(id, KillOptions{Prove: true, Timeout: 120 * time.Millisecond, Poll: 20 * time.Millisecond})
	if err == nil {
		t.Fatal("KillRun signalled a pid it could not prove")
	}
	if !strings.Contains(err.Error(), "--now") {
		t.Errorf("error does not offer the escape hatch: %v", err)
	}
}

// A run that ends while we are proving is not signalled at all.
func TestKillRun_StopsWhenTheRunFinishesMidProof(t *testing.T) {
	started := time.Now().UTC()
	lister, repo, id := killFixture(t, 999999, run.StatusRunning, started)

	go func() {
		time.Sleep(30 * time.Millisecond)
		if rec, err := run.ReadRecord(repo, id); err == nil {
			rec.Status = run.StatusDone
			_ = run.WriteRecord(rec)
		}
	}()

	_, err := lister.KillRun(id, KillOptions{Prove: true, Timeout: 2 * time.Second, Poll: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("KillRun signalled a run that had already finished")
	}
	if !strings.Contains(err.Error(), "finished as done") {
		t.Errorf("error does not say what happened: %v", err)
	}
}
