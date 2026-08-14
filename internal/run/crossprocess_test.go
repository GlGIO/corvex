package run_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/run"
)

// TestSecondProcessListsWhatIsAlive is the F1 acceptance test: "two runs of the
// same project are distinguishable; a second process can list what is alive".
//
// Four processes take part, and that is the point — a test calling List() in the
// same process that wrote the records would prove neither half of the claim:
//
//	this process  -> run A, still running, heartbeat fresh   => alive
//	child #1      -> run B, recorded done, exited            => finished
//	child #2      -> run C, SIGKILLed while running          => dead
//	child #3      -> the listing itself
//
// Run C is the case that makes status-only liveness a lie: its record still
// says "running" and always will, because SIGKILL leaves no chance to write
// anything else.
func TestSecondProcessListsWhatIsAlive(t *testing.T) {
	home := t.TempDir()
	t.Setenv(run.HomeEnv, home)
	repo := t.TempDir()

	mine, err := run.Registry{Repo: repo}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Heartbeat once so the local record is strictly fresher than the index
	// line, which is the overlay path Resolver.List depends on.
	if err := mine.Touch(); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	var finished run.Record
	runHelper(t, "start", &finished, helperRepo+"="+repo)

	killed := startAndKill(t, repo)

	var views []run.View
	runHelper(t, "list", &views)

	if len(views) != 3 {
		t.Fatalf("listing: got %d runs, want 3: %+v", len(views), views)
	}
	byID := map[string]run.View{}
	for _, v := range views {
		byID[v.Record.RunID] = v
	}
	ids := []string{mine.RunID(), finished.RunID, killed.RunID}
	if len(byID) != 3 {
		t.Fatalf("three runs of project demo collapsed into %d rows: %v", len(byID), ids)
	}
	for _, id := range ids {
		if !run.ValidID(id) {
			t.Errorf("id %q is not a well-formed run id", id)
		}
		if _, ok := byID[id]; !ok {
			t.Fatalf("run %s missing from the listing produced by another process", id)
		}
	}

	if got := byID[mine.RunID()]; got.Liveness != run.LivenessAlive || !got.Alive() {
		t.Errorf("my own run: liveness %q, want alive", got.Liveness)
	} else if got.Source != run.SourceRecord {
		t.Errorf("my own run: source %q, want %q (the heartbeat's record must win over the index line)",
			got.Source, run.SourceRecord)
	}

	if got := byID[finished.RunID]; got.Liveness != run.LivenessFinished || got.Record.Status != run.StatusDone {
		t.Errorf("run that finished: liveness %q status %q, want finished/done", got.Liveness, got.Record.Status)
	}

	got := byID[killed.RunID]
	if got.Liveness != run.LivenessDead {
		t.Errorf("SIGKILLed run: liveness %q, want dead", got.Liveness)
	}
	if got.Record.Status != run.StatusRunning {
		t.Errorf("SIGKILLed run: status %q, want %q — the record cannot know it died, which is why "+
			"liveness is not read off status", got.Record.Status, run.StatusRunning)
	}
	if got.Record.Repo != repo {
		t.Errorf("repo: got %q, want %q", got.Record.Repo, repo)
	}
	if got.Record.PID == os.Getpid() {
		t.Errorf("the child's run recorded the parent pid %d", got.Record.PID)
	}
}

// startAndKill spawns a child that registers a run and blocks, kills it, and
// returns the record it left behind.
func startAndKill(t *testing.T, repo string) run.Record {
	t.Helper()
	cmd, out := helperCmd(t, "hold", helperRepo+"="+repo)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting hold helper: %v", err)
	}
	waitForFile(t, out)
	var rec run.Record
	decodeFile(t, out, &rec)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing hold helper: %v", err)
	}
	// Wait reaps the child. Without it the pid lingers as a zombie, and a
	// zombie still answers signal 0 — the probe would report the run alive.
	_ = cmd.Wait()
	return rec
}

// TestRecordSurvivesTheProcessThatWroteIt checks the repository-scoped path
// (no global index involved): the record file is still there and still readable
// after its writer is gone.
func TestRecordSurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Setenv(run.HomeEnv, t.TempDir())
	repo := t.TempDir()

	var rec run.Record
	runHelper(t, "start", &rec, helperRepo+"="+repo)

	path, err := run.RecordPath(repo, rec.RunID)
	if err != nil {
		t.Fatalf("RecordPath: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("record %s did not survive its process: %v", path, err)
	}

	views, err := run.Resolver{}.ListRepo(repo)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("ListRepo: got %d runs, want 1", len(views))
	}
	if views[0].Record.RunID != rec.RunID {
		t.Errorf("run id: got %q, want %q", views[0].Record.RunID, rec.RunID)
	}
	if views[0].Liveness != run.LivenessFinished {
		t.Errorf("liveness: got %q, want finished", views[0].Liveness)
	}
	if views[0].Record.Host == "" {
		t.Error("host not recorded")
	}
}

// TestProcessAliveAgainstRealProcesses pins the probe against processes that
// actually exist, since this is the one place where a mocked probe proves
// nothing.
func TestProcessAliveAgainstRealProcesses(t *testing.T) {
	if !run.ProcessAlive(os.Getpid()) {
		t.Error("ProcessAlive(self) = false")
	}
	for _, pid := range []int{0, -1} {
		if run.ProcessAlive(pid) {
			t.Errorf("ProcessAlive(%d) = true", pid)
		}
	}
	// pid 1 exists and (unless the suite runs as root) is not ours: the EPERM
	// branch must still report "exists".
	if !run.ProcessAlive(1) {
		t.Error("ProcessAlive(1) = false; a pid we may not signal still exists")
	}

	cmd, _ := helperCmd(t, "sleep")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep helper: %v", err)
	}
	pid := cmd.Process.Pid
	if !run.ProcessAlive(pid) {
		t.Fatalf("ProcessAlive(%d) = false for a process we just spawned", pid)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()
	if run.ProcessAlive(pid) {
		t.Errorf("ProcessAlive(%d) = true after kill+reap", pid)
	}
}

// TestIndexSurvivesConcurrentProcesses hammers the append-only index from four
// separate processes. Each line must arrive whole: no interleaved halves, no
// lost run.
func TestIndexSurvivesConcurrentProcesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv(run.HomeEnv, home)

	const procs, perProc = 4, 25

	var started []func() error
	for i := 0; i < procs; i++ {
		cmd, _ := helperCmd(t, "append",
			fmt.Sprintf("%s=%d", helperCount, perProc),
			fmt.Sprintf("%s=%d", helperBase, i*perProc+1),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting append helper %d: %v", i, err)
		}
		started = append(started, cmd.Wait)
	}
	for i, wait := range started {
		if err := wait(); err != nil {
			t.Fatalf("append helper %d: %v", i, err)
		}
	}

	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != procs*perProc {
		t.Fatalf("index has %d parseable lines, want %d — a line was torn or lost",
			len(entries), procs*perProc)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.RunID] = true
	}
	if len(seen) != procs*perProc {
		t.Errorf("distinct run ids: got %d, want %d", len(seen), procs*perProc)
	}

	// The raw bytes must also be intact: every line whole JSON, none merged.
	data, err := os.ReadFile(filepath.Join(home, run.IndexFile))
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != procs*perProc {
		t.Errorf("index has %d newlines, want %d", lines, procs*perProc)
	}
}
