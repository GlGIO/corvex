package run_test

// The id space, and why it is not a ceiling any more.
//
// `run_` + 4 hex digits is 65,536 slots. The audit measured what happens as the
// space fills: 13 of 20 real runs lost their identity ("8 attempts exhausted")
// and the ledger silently went back to the pre-F1 shape — lines with no run_id.
// Two things had to change, and both are asserted here:
//
//   - the failure is HARD. A run that cannot be identified does not start.
//   - the oracle is scoped to the runs that can still be ADDRESSED, and the
//     index is rotated, so an id whose run closed long ago comes back.
//
// The width of the id is deliberately not part of the fix (it is UI surface,
// owned by F3), so these tests inject a tiny space instead: proving the
// behaviour at 4 digits would mean minting 65k runs, which is exactly the
// 15-minute experiment the auditor had to run.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// hexCycle hands out every id of a `digits`-wide hex space in order, then wraps.
func hexCycle(digits int) run.IDFunc {
	n := 0
	size := 1
	for i := 0; i < digits; i++ {
		size *= 16
	}
	return func() (string, error) {
		id := fmt.Sprintf("%s%0*x", run.IDPrefix, digits, n%size)
		n++
		return id, nil
	}
}

// movingClock is a clock the test advances by hand: retention is a question
// about elapsed time, and a test that slept for it would take a fortnight.
type movingClock struct{ at time.Time }

func (c *movingClock) now() time.Time      { return c.at }
func (c *movingClock) add(d time.Duration) { c.at = c.at.Add(d) }

// TestIDSpaceExhaustionRefusesToStartTheRun pins the shape of the failure: when
// no id can be minted, Start fails loudly. Never a run without identity — a
// ledger line nobody can attribute is worse than no run at all, and the audit
// caught the system producing exactly that under stress.
func TestIDSpaceExhaustionRefusesToStartTheRun(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	clock := &movingClock{at: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)}
	reg := run.Registry{Repo: repo, Home: home, NewID: hexCycle(1), Now: clock.now}

	const space = 16 // one hex digit
	for i := 0; i < space; i++ {
		if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
			t.Fatalf("Start %d of %d: %v", i+1, space, err)
		}
	}

	_, err := reg.Start(run.StartOptions{Project: "demo"})
	if err == nil {
		t.Fatal("Start succeeded with every id in the space already taken: the run has no identity")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error = %v, want it to name the exhausted id space", err)
	}

	// And the failure left nothing half-registered behind.
	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.RunID] = true
	}
	if len(seen) != space {
		t.Errorf("index announces %d distinct runs, want %d", len(seen), space)
	}
	recs, err := run.ReadRecords(repo)
	if err != nil {
		t.Fatalf("ReadRecords: %v", err)
	}
	if len(recs) != space {
		t.Errorf("repo holds %d records, want %d — a failed Start left a claim behind", len(recs), space)
	}
}

// TestRetentionRecyclesIDsSoTheSpaceCannotFillUp is the defect: with the oracle
// scoped to every id the machine ever announced, run 65,537 can never start. It
// has to be scoped to the ids that can still be ADDRESSED instead — so an id
// whose run closed longer ago than the retention window comes back.
//
// The recycling is a deliberate trade, not a happy accident: `run show` on an
// ancient id may find a newer run, or nothing. The alternative is a tool that
// stops working, having first lied about identity for the last few thousand runs.
func TestRetentionRecyclesIDsSoTheSpaceCannotFillUp(t *testing.T) {
	t.Setenv("CORVEX_RUN_RETENTION", "1h")
	home, repo := t.TempDir(), t.TempDir()
	clock := &movingClock{at: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)}
	reg := run.Registry{Repo: repo, Home: home, NewID: hexCycle(1), Now: clock.now}

	const space = 16
	var closed []string
	for i := 0; i < space; i++ {
		h, err := reg.Start(run.StartOptions{Project: "demo"})
		if err != nil {
			t.Fatalf("Start %d of %d: %v", i+1, space, err)
		}
		if err := h.SetStatus(run.StatusDone); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
		closed = append(closed, h.RunID())
		clock.add(time.Minute)
	}

	// Every one of those runs closed minutes ago: they are all still addressable,
	// so no id may be recycled yet.
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err == nil {
		t.Fatal("an id was recycled while the run that used it had only just closed")
	}

	// Past the retention window, the space is free again.
	clock.add(3 * time.Hour)
	h, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("no id available after the retention window passed: %v\n"+
			"this is the 65,537th run on a real machine: it must not be refused", err)
	}
	if !run.ValidID(h.RunID()) {
		t.Fatalf("recycled id %q is malformed", h.RunID())
	}
	recycled := false
	for _, id := range closed {
		if id == h.RunID() {
			recycled = true
		}
	}
	if !recycled {
		t.Errorf("id %q came from outside a space of %d that was full: %v", h.RunID(), space, closed)
	}

	// The record on disk is the new run, not the corpse of the old one.
	rec, err := run.ReadRecord(repo, h.RunID())
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if rec.Status != run.StatusRunning || !rec.StartedAt.Equal(clock.now()) {
		t.Errorf("record = %+v, want the new run (running, started %v)", rec, clock.now())
	}
	// And a reader addressing the recycled id gets the new run: last line wins.
	v, ok, err := run.Resolver{Home: home, Alive: alwaysAlive, Now: clock.now}.Get(h.RunID())
	if err != nil || !ok {
		t.Fatalf("Get(%s) = %v, %v", h.RunID(), ok, err)
	}
	if v.Record.Status != run.StatusRunning {
		t.Errorf("reader sees status %q for the recycled id, want running", v.Record.Status)
	}
}

// TestAnUnfinishedRunKeepsItsIDFarBeyondRetention: age is not the whole predicate.
// A run that never recorded an end (SIGKILL, a crash, or simply a week-long run)
// keeps its id long past the window that would have released a finished one,
// because the index gets no heartbeat and its line would otherwise look ancient
// while the process is still working.
//
// "Far beyond", not "forever", and the difference is deliberate. A line that never
// ages out has no way back: a machine that accumulated orphaned `running` lines was
// measured refusing every run with an index that would not rotate, and the error it
// printed named a knob that governed none of it. The ceiling is
// staleNonTerminalFactor windows — a week here, so eight weeks — and the complement
// of this test is TestOrphanedRunningLinesStopHoldingTheirIDsEventually.
func TestAnUnfinishedRunKeepsItsIDFarBeyondRetention(t *testing.T) {
	t.Setenv("CORVEX_RUN_RETENTION", "168h") // a week; a month is well inside the ceiling
	home, repo := t.TempDir(), t.TempDir()
	clock := &movingClock{at: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)}
	reg := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return "run_aaaa", nil }, Now: clock.now}

	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clock.add(30 * 24 * time.Hour) // a month later, still no terminal status

	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err == nil {
		t.Fatal("the id of a run that never closed was handed out again: two live runs would " +
			"collapse into one row of the index")
	}
}

// TestIndexRotationBoundsTheFileWithoutLosingARun: pruning is what keeps the
// oracle small and the file finite. Rotation (rename, then carry the still
// addressable snapshots forward) is chosen over rewriting in place because a
// rewrite has a window in which a line another process is appending is lost —
// and append atomicity is the whole reason 8 concurrent runs can share the file.
func TestIndexRotationBoundsTheFileWithoutLosingARun(t *testing.T) {
	t.Setenv("CORVEX_RUN_RETENTION", "1h")
	t.Setenv("CORVEX_RUN_INDEX_MAX_BYTES", "1024")
	home, repo := t.TempDir(), t.TempDir()
	clock := &movingClock{at: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)}

	// A long history: 30 closed runs from yesterday, plus one that never closed.
	old := clock.at.Add(-24 * time.Hour)
	for i := 0; i < 30; i++ {
		rec := run.Record{
			RunID: fmt.Sprintf("run_%02x", i), Repo: repo, Project: "demo", PID: 1000 + i,
			Status: run.StatusDone, StartedAt: old, UpdatedAt: old,
		}
		if err := run.AppendIndex(home, rec, old); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	// The unfinished run's line is older than the retention window (which is what
	// makes the assertion below mean something: a finished run of this age is
	// archived) and well inside the ceiling past which a non-terminal line stops
	// being believed. `old` itself is 24 h, which with a 1 h window is 24 windows —
	// past the ceiling, and a run nobody should still call live.
	stillWorking := clock.at.Add(-2 * time.Hour)
	unfinished := run.Record{
		RunID: "run_ffff", Repo: repo, Project: "demo", PID: 2000,
		Status: run.StatusRunning, StartedAt: stillWorking, UpdatedAt: stillWorking,
	}
	if err := run.AppendIndex(home, unfinished, stillWorking); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}

	index := run.IndexPath(home)
	before, err := os.Stat(index)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	if before.Size() <= 1024 {
		t.Fatalf("index is only %d bytes: the test cannot exercise rotation", before.Size())
	}
	linesBefore := countLines(t, index)

	// One more run: this is when housekeeping happens.
	reg := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return "run_beef", nil }, Now: clock.now}
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	after, err := os.Stat(index)
	if err != nil {
		t.Fatalf("stat index after: %v", err)
	}
	if after.Size() >= before.Size() {
		t.Errorf("index grew from %d to %d bytes: it is never pruned", before.Size(), after.Size())
	}

	archive := index + ".1"
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("no archive at %s: rotation dropped history instead of moving it: %v", archive, err)
	}
	if got, want := countLines(t, index)+countLines(t, archive), linesBefore+1; got < want {
		t.Errorf("%d lines across index+archive, want at least %d: rotation lost a run", got, want)
	}

	// What survives in the live file is exactly what can still be addressed: the
	// run that never closed, and the new one. The 30 closed-yesterday runs are
	// archived, and their ids are free again.
	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	live := map[string]bool{}
	for _, e := range entries {
		live[e.RunID] = true
	}
	if !live["run_ffff"] {
		t.Error("the run that never closed was pruned: a live run would vanish from every listing")
	}
	if !live["run_beef"] {
		t.Error("the run that just started is missing from the index")
	}
	if live["run_00"] {
		t.Error("a run closed a day ago is still in the live index: nothing was pruned")
	}
	if len(live) != 2 {
		t.Errorf("live index holds %d runs (%v), want 2", len(live), live)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

// TestRetentionDefaultAndOverride pins the knob: a default long enough that
// "the run from last week" still resolves, and an override for anyone whose
// volume or memory differs.
func TestRetentionDefaultAndOverride(t *testing.T) {
	if run.DefaultRetention != 14*24*time.Hour {
		t.Errorf("DefaultRetention = %v, want 14 days", run.DefaultRetention)
	}
	if run.RetentionEnv != "CORVEX_RUN_RETENTION" || run.IndexMaxBytesEnv != "CORVEX_RUN_INDEX_MAX_BYTES" {
		t.Errorf("env names = %q / %q", run.RetentionEnv, run.IndexMaxBytesEnv)
	}

	home, repo := t.TempDir(), t.TempDir()
	clock := &movingClock{at: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)}
	reg := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return "run_aaaa", nil }, Now: clock.now,
		Retention: time.Minute}

	h, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.SetStatus(run.StatusDone); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	// The field wins over the env var, and over the default: two minutes later
	// the id is free.
	t.Setenv("CORVEX_RUN_RETENTION", "720h")
	clock.add(2 * time.Minute)
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start with Retention=1m did not recycle after 2m: %v", err)
	}

	// A malformed env value is ignored rather than turning into a zero window
	// (which would recycle ids of runs that closed a millisecond ago).
	t.Setenv("CORVEX_RUN_RETENTION", "banana")
	fresh := run.Registry{Repo: t.TempDir(), Home: t.TempDir(),
		NewID: func() (string, error) { return "run_cccc", nil }, Now: clock.now}
	h2, err := fresh.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start with a malformed retention env: %v", err)
	}
	if err := h2.SetStatus(run.StatusDone); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	clock.add(time.Hour)
	if _, err := fresh.Start(run.StartOptions{Project: "demo"}); err == nil {
		t.Error("a malformed retention env recycled an id after an hour: it was read as zero")
	}
}

// TestRecycledRecordFileIsReplacedNotMerged: the second oracle is an O_EXCL
// claim on `<repo>/.corvex/runs/<id>.json`, and it has to be scoped the same
// way. A record whose freshness is older than the window is dead weight: the
// heartbeat that would have refreshed it stopped a fortnight ago.
func TestRecycledRecordFileIsReplacedNotMerged(t *testing.T) {
	t.Setenv("CORVEX_RUN_RETENTION", "1h")
	home, repo := t.TempDir(), t.TempDir()
	clock := &movingClock{at: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)}

	// A record left by a run that was SIGKILLed a day ago: status still says
	// running, and nothing will ever close it.
	stale := run.Record{
		RunID: "run_dead", Repo: repo, Project: "demo", PID: 4242,
		Status: run.StatusRunning, StartedAt: clock.at.Add(-24 * time.Hour),
		UpdatedAt: clock.at.Add(-24 * time.Hour),
	}
	if err := run.WriteRecord(stale); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}

	reg := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return "run_dead", nil }, Now: clock.now}
	h, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v — a record whose heartbeat died a day ago must not hold its id forever", err)
	}
	if h.RunID() != "run_dead" {
		t.Fatalf("run id = %q, want run_dead", h.RunID())
	}
	rec, err := run.ReadRecord(repo, "run_dead")
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if !rec.StartedAt.Equal(clock.now()) || rec.PID == 4242 {
		t.Errorf("record = %+v, want the new run, not a merge with the old one", rec)
	}
	if n := len(filesIn(t, run.RecordsDir(repo), ".json")); n != 1 {
		t.Errorf("%d record files, want 1: the recycled claim was not reused", n)
	}
}

func filesIn(t *testing.T, dir, suffix string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), suffix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
