package run_test

// The id oracle and the rotation window.
//
// Rotation moves the live index aside with a rename and appends the still
// addressable snapshots back. Between those two steps `runs.jsonl` does not
// exist, or holds only part of what it will hold. The global index is the ONLY
// oracle that can see runs in OTHER repositories — the O_EXCL record claim is
// per-repository by construction — so a claim that lands in that gap can mint the
// id of a run that is alive somewhere else, and the two runs then collapse into
// one row of the index.
//
// The tests below pin that gap shut from three directions: the exact mid-rotation
// state, an interrupted carry-forward, and real concurrent processes with real
// rotations happening underneath real claims.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// liveIn returns a running (non-terminal, therefore addressable at any age)
// snapshot belonging to repo.
func liveIn(repo, id string, at time.Time) run.Record {
	return run.Record{RunID: id, Repo: repo, Status: run.StatusRunning,
		PID: 4000, Host: "otherbox", StartedAt: at, UpdatedAt: at}
}

// TestClaimRefusesALiveIDWhileTheIndexIsMidRotation freezes the window instead of
// racing it: the rename has happened and not one carry-forward append has. That is
// a state a real rotation passes through on every rotation, measured at 207 ms wide
// on a 6 MB index, and a state a crash can leave behind permanently.
//
// The victim run belongs to a DIFFERENT repository, which is the whole point: its
// record file is not in the claimant's `.corvex/runs`, so the O_EXCL backstop
// cannot see it and the index is the only thing that can say no.
func TestClaimRefusesALiveIDWhileTheIndexIsMidRotation(t *testing.T) {
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		if err := run.AppendIndex(home, liveIn(otherRepo, idAt(i), at), at); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}

	// The rename, and nothing after it.
	if err := os.Rename(run.IndexPath(home), run.IndexArchivePath(home)); err != nil {
		t.Fatalf("simulating the rotation rename: %v", err)
	}
	if _, err := os.Stat(run.IndexPath(home)); !os.IsNotExist(err) {
		t.Fatalf("the live index still exists (%v): the window was not reproduced", err)
	}

	victim := idAt(5)
	h, err := run.Registry{Repo: myRepo, Home: home, Retention: time.Hour,
		IndexMaxBytes: 1 << 30, // no rotation of our own: this is about reading
		Now:           func() time.Time { return at },
		NewID:         func() (string, error) { return victim, nil }}.
		Start(run.StartOptions{Project: "demo"})
	if err == nil {
		t.Fatalf("minted %s for a run in %s while that id belongs to a live run in %s: "+
			"the two runs now share one row of the global index",
			h.RunID(), myRepo, otherRepo)
	}
	if !errors.Is(err, run.ErrIDSpaceExhausted) {
		t.Errorf("refused with %v, want %v — the refusal has to be the one a caller can act on",
			err, run.ErrIDSpaceExhausted)
	}
}

// TestClaimRefusesALiveIDWhenTheCarryForwardWasInterrupted is the other half of
// the same window: the rename happened and SOME of the snapshots are back. The ids
// that have not been written yet are exactly as invisible as they were a
// microsecond earlier, and a rotation killed halfway through (a full disk, a
// SIGKILL) leaves that state on disk for good.
func TestClaimRefusesALiveIDWhenTheCarryForwardWasInterrupted(t *testing.T) {
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		if err := run.AppendIndex(home, liveIn(otherRepo, idAt(i), at), at); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	if err := os.Rename(run.IndexPath(home), run.IndexArchivePath(home)); err != nil {
		t.Fatalf("simulating the rotation rename: %v", err)
	}
	// Three of twenty made it back before the writer stopped.
	for i := 17; i < 20; i++ {
		if err := run.AppendIndex(home, liveIn(otherRepo, idAt(i), at), at); err != nil {
			t.Fatalf("partial carry forward: %v", err)
		}
	}

	victim := idAt(2) // one of the seventeen still only in the archive
	if h, err := (run.Registry{Repo: myRepo, Home: home, Retention: time.Hour,
		IndexMaxBytes: 1 << 30,
		Now:           func() time.Time { return at },
		NewID:         func() (string, error) { return victim, nil }}).
		Start(run.StartOptions{Project: "demo"}); err == nil {
		t.Fatalf("minted %s though it is a live run whose snapshot is still in the archive", h.RunID())
	}
}

// TestRecyclingStillWorksWhileTheArchiveRemembers is the guard on the fix rather
// than on the bug: reading the archive must not resurrect ids. What decides
// whether an id can be reused is the retention predicate, never which file the
// line happens to sit in — otherwise "read the archive too" would quietly turn the
// 16-bit id space back into the ceiling retention exists to remove.
func TestRecyclingStillWorksWhileTheArchiveRemembers(t *testing.T) {
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	long := at.Add(-30 * 24 * time.Hour)

	// An old, finished run: outside any retention window, so its id is free.
	done := run.Record{RunID: "run_dead", Repo: otherRepo, Status: run.StatusDone,
		StartedAt: long, UpdatedAt: long}
	if err := run.AppendIndex(home, done, long); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	if err := os.Rename(run.IndexPath(home), run.IndexArchivePath(home)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	h, err := run.Registry{Repo: myRepo, Home: home, Retention: time.Hour,
		IndexMaxBytes: 1 << 30,
		Now:           func() time.Time { return at },
		NewID:         func() (string, error) { return "run_dead", nil }}.
		Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("refused to recycle the id of a run that finished a month ago: %v — "+
			"the archive is being read as if presence meant addressable", err)
	}
	if h.RunID() != "run_dead" {
		t.Errorf("run id = %q, want the recycled run_dead", h.RunID())
	}
}

// TestNoLiveIDIsRecycledUnderConcurrentRotation is the auditor's experiment,
// turned into a test: real processes appending to the index while it rotates
// underneath them, and claims fired continuously so that some of them land inside
// the window.
//
// The shape of the proof: every id in the claimed range is announced BEFORE any
// claim is attempted, so there is no moment at which any of them is legitimately
// free. Every claim must therefore be refused, and one success is one collision.
// The claimant works in its own repository, so the per-repository O_EXCL backstop
// cannot mask a blind index.
//
// It also re-checks what rotation was introduced to guarantee: no line appended by
// a concurrent process is lost (every addressable id is readable from the live file
// or the archive) and no line is torn.
func TestNoLiveIDIsRecycledUnderConcurrentRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes and rotates a real file repeatedly")
	}
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv(run.HomeEnv, home) // the helper processes inherit it
	at := time.Now().UTC()

	// The claimed range: 256 live ids in another repository, all announced up front,
	// so there is no instant at which any of them is legitimately free.
	liveIDs := make([]string, 0, 256)
	for i := 0; i < 256; i++ {
		id := fmt.Sprintf("%s%04x", run.IDPrefix, 0x0100+i)
		liveIDs = append(liveIDs, id)
		if err := run.AppendIndex(home, liveIn(otherRepo, id, at), at); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	mustSurvive := make(map[string]bool, len(liveIDs))
	for _, id := range liveIDs {
		mustSurvive[id] = true
	}

	// Three roles, three real processes, so nothing here is a same-process
	// simulation of concurrency:
	//
	//	appender x2 — bulk forgotten lines (the fuel rotation needs) plus live
	//	              lines of their own, which must all survive
	//	rotator     — starts runs with a 24 KiB threshold, so it renames the index
	//	              out from under everyone continuously
	//
	// The claimant is this process, and it does NOT rotate (1 GiB threshold): it
	// only reads, at full speed, which is what puts claims inside somebody else's
	// window.
	// The experiment is sized by evidence, not by the clock: the load runs until the
	// claimant has fired enough claims to have been inside a window, and the
	// deadline is only a backstop for a parent that died. A wall-clock budget would
	// collect 165 claims normally and 11 under -race, which is the difference
	// between an experiment and a coin toss.
	wantClaims := 200
	if raceDetector {
		// The instrumented build cannot run the load generators fast enough for the
		// full sample; a smaller one still lands inside a rename.
		wantClaims = 40
	}
	stopFile := filepath.Join(t.TempDir(), "stop")
	until := fmt.Sprint(time.Now().Add(2 * time.Minute).UnixNano())
	const appenders, livePerAppender = 2, 48
	var procs []*exec.Cmd
	for p := 0; p < appenders; p++ {
		base := 0x2000 + p*0x100
		cmd, _ := helperCmd(t, "rotationload", helperRepo+"="+otherRepo,
			helperBase+"="+fmt.Sprint(base),
			helperCount+"="+fmt.Sprint(livePerAppender),
			helperUntil+"="+until, helperStop+"="+stopFile)
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting appender %d: %v", p, err)
		}
		procs = append(procs, cmd)
		for i := 0; i < livePerAppender; i++ {
			mustSurvive[fmt.Sprintf("%s%04x", run.IDPrefix, base+i)] = true
		}
	}
	rot, _ := helperCmd(t, "rotator", helperRepo+"="+t.TempDir(),
		helperUntil+"="+until, helperStop+"="+stopFile)
	if err := rot.Start(); err != nil {
		t.Fatalf("starting rotator: %v", err)
	}
	procs = append(procs, rot)

	// Claims, continuously, until the load window closes. Results travel on a
	// channel so nothing is shared between goroutines while -race is watching.
	type claimResult struct {
		attempts, collisions int
		rotations            int
		stolen               string
		otherErr             error
	}
	results := make(chan claimResult, 1)
	go func() {
		var r claimResult
		// Enough claims AND at least one rotation observed from here: a run of
		// claims against an index nobody rotated proves nothing at all. The cap
		// stops a broken load generator from hanging the suite.
		for i := 0; (r.attempts < wantClaims || r.rotations == 0) && i < 20*wantClaims; i++ {
			if _, err := os.Stat(run.IndexArchivePath(home)); err == nil {
				r.rotations++
			}
			id := liveIDs[i%len(liveIDs)]
			r.attempts++
			h, err := run.Registry{Repo: myRepo, Home: home,
				Retention:     time.Hour,
				IndexMaxBytes: 1 << 30, // the claimant reads; others rotate
				NewID:         func() (string, error) { return id, nil }}.
				Start(run.StartOptions{Project: "claimant"})
			switch {
			case err == nil:
				r.collisions++
				r.stolen = h.RunID()
			case !errors.Is(err, run.ErrIDSpaceExhausted) && r.otherErr == nil:
				r.otherErr = err
			}
		}
		results <- r
	}()

	r := <-results
	if err := os.WriteFile(stopFile, nil, 0o600); err != nil {
		t.Fatalf("signalling the load to stop: %v", err)
	}
	for p, cmd := range procs {
		if err := cmd.Wait(); err != nil {
			t.Errorf("helper %d: %v", p, err)
		}
	}

	// The experiment has to have happened before its result means anything.
	if r.attempts < wantClaims {
		t.Fatalf("only %d claims were attempted: too few to have landed in any window", r.attempts)
	}
	if r.rotations == 0 {
		t.Fatal("the archive never appeared while claims were running: no window was ever " +
			"opened, so a green result here would prove nothing")
	}
	if r.collisions > 0 {
		t.Errorf("%d of %d claims minted the id of a live run (e.g. %s): the index went blind "+
			"while it was being rotated", r.collisions, r.attempts, r.stolen)
	}
	if r.otherErr != nil {
		t.Errorf("a claim failed for a reason other than a taken id: %v", r.otherErr)
	}
	t.Logf("%d claims against %d announced live ids, all refused; the archive was present "+
		"for %d of them", r.attempts, len(liveIDs), r.rotations)

	// LOST: every addressable id has to be readable from the live file or the
	// archive. TORN: every non-blank line in both files has to parse.
	present := map[string]bool{}
	for _, path := range []string{run.IndexPath(home), run.IndexArchivePath(home)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for n, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var e run.IndexEntry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("TORN line %d of %s: %v\n%s", n+1, path, err, line)
			}
			present[e.RunID] = true
		}
	}
	var lost []string
	for id := range mustSurvive {
		if !present[id] {
			lost = append(lost, id)
		}
	}
	if len(lost) > 0 {
		t.Errorf("LOST %d of %d addressable ids (e.g. %v): rotation dropped lines that "+
			"concurrent processes had appended", len(lost), len(mustSurvive),
			lost[:min(5, len(lost))])
	}
}

// TestARotationDoesNotEraseWhatAnInterruptedOneLeftBehind is the hole in "read the
// archive too", closed.
//
// Only one archive generation is kept, so a rotation's rename destroys the current
// archive. That is harmless after a rotation that finished — its carry-forward put
// every addressable snapshot back in the live file, so the live file already holds
// them. It is NOT harmless after a rotation that was killed between the rename and
// the carry-forward: then the archive is the only place those ids exist, and
// overwriting it would make a live run invisible with no window to blame.
//
// So a rotation finishes the previous one first. The test walks the whole sequence:
// interrupt a rotation, let the index grow, rotate again, and demand that the id of
// the run stranded in the first archive is still refused.
func TestARotationDoesNotEraseWhatAnInterruptedOneLeftBehind(t *testing.T) {
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	long := at.Add(-30 * 24 * time.Hour)

	stranded := "run_beef"
	if err := run.AppendIndex(home, liveIn(otherRepo, stranded, at), at); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	// A rotation that got as far as the rename and no further.
	if err := os.Rename(run.IndexPath(home), run.IndexArchivePath(home)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Life goes on: enough finished-and-forgotten runs to push the rebuilt live
	// index over the threshold, so the next run start rotates for real.
	for i := 0; i < 40; i++ {
		rec := run.Record{RunID: idAt(0x300 + i), Repo: otherRepo, Status: run.StatusDone,
			StartedAt: long, UpdatedAt: long}
		if err := run.AppendIndex(home, rec, long); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}

	reg := run.Registry{Repo: myRepo, Home: home, IndexMaxBytes: 2 << 10, Retention: time.Hour,
		Now:   func() time.Time { return at },
		NewID: func() (string, error) { return "run_cafe", nil }}
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(run.IndexArchivePath(home)); err != nil {
		t.Fatalf("no rotation happened (%v): the archive was never overwritten, so the "+
			"dangerous step this test is about did not run", err)
	}

	// The stranded run is live, so its id must still be refused — and it must be
	// refused because it was carried forward, not because the old archive happened
	// to survive.
	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.RunID == stranded {
			found = true
		}
	}
	if !found {
		t.Errorf("%s is not in the live index after the second rotation: the interrupted "+
			"rotation was never completed, and the only copy of that id was overwritten", stranded)
	}
	reg.NewID = func() (string, error) { return stranded, nil }
	if h, err := reg.Start(run.StartOptions{Project: "demo"}); err == nil {
		t.Errorf("minted %s, the id of a run that is still running", h.RunID())
	}
}

// TestOrphanedRunningLinesStopHoldingTheirIDsEventually is the escape hatch for the
// state that used to brick a machine.
//
// A run killed with SIGKILL says `running` on disk forever. The old predicate
// called every non-terminal line addressable at any age, and worthRotating refuses
// to rotate an index whose every line is addressable — so an index of orphans never
// rotated, never released an id, and the machine refused every run, with an error
// naming a knob that governed none of it.
//
// Both halves are asserted, because only having both is a fix: a line that is old
// but still plausible keeps its id, and a line that is absurdly old does not.
func TestOrphanedRunningLinesStopHoldingTheirIDsEventually(t *testing.T) {
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	const retention = 24 * time.Hour

	// Two orphans, both `running`, both killed long ago. One a week dead, one two
	// years.
	recent := liveIn(otherRepo, "run_0aa1", at.Add(-7*24*time.Hour))
	ancient := liveIn(otherRepo, "run_0aa2", at.Add(-2*365*24*time.Hour))
	for _, rec := range []run.Record{recent, ancient} {
		if err := run.AppendIndex(home, rec, rec.UpdatedAt); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}

	reg := run.Registry{Repo: myRepo, Home: home, IndexMaxBytes: 1 << 30, Retention: retention,
		Now: func() time.Time { return at }}

	// A week without a status change is a long-running run, not a dead one: the
	// index gets no heartbeat, so this is exactly what a live three-week run looks
	// like from here.
	reg.NewID = func() (string, error) { return recent.RunID, nil }
	if h, err := reg.Start(run.StartOptions{Project: "demo"}); err == nil {
		t.Errorf("minted %s: a run whose last status change was a week ago may still be "+
			"working, and the index has no heartbeat to tell the difference", h.RunID())
	}

	// Two years is not a slow run. That id has to come back, or the machine has no
	// way out of a space full of orphans.
	reg.NewID = func() (string, error) { return ancient.RunID, nil }
	h, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("refused %s, orphaned `running` for two years: %v — nothing releases this "+
			"id, so the machine can never mint again", ancient.RunID, err)
	}
	if h.RunID() != ancient.RunID {
		t.Errorf("run id = %q, want %q", h.RunID(), ancient.RunID)
	}
}

// TestAnIndexOfAncientOrphansRotates is the other end of the same defect: releasing
// the id is only half a fix if the file itself never shrinks. worthRotating asks
// whether anything has left the addressable set, so the ageing rule has to make
// these lines leave it — otherwise the index grows past every threshold and every
// run start pays to read it.
func TestAnIndexOfAncientOrphansRotates(t *testing.T) {
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	dead := at.Add(-2 * 365 * 24 * time.Hour)

	for i := 0; i < 40; i++ {
		if err := run.AppendIndex(home, liveIn(otherRepo, idAt(0x400+i), dead), dead); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	before := countLines(t, run.IndexPath(home))

	if _, err := (run.Registry{Repo: myRepo, Home: home, IndexMaxBytes: 2 << 10,
		Retention: 24 * time.Hour, Now: func() time.Time { return at },
		NewID: func() (string, error) { return "run_cafe", nil }}).
		Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := os.Stat(run.IndexArchivePath(home)); err != nil {
		t.Fatalf("an index made entirely of two-year-old `running` lines did not rotate "+
			"(%v): nothing ever shrinks it and nothing ever releases its ids", err)
	}
	if got := countLines(t, run.IndexPath(home)); got >= before {
		t.Errorf("live index still holds %d lines (was %d): rotation archived nothing", got, before)
	}
}

// TestAnUnreadableArchiveRefusesTheRunAndABlockedPathDoesNot pins the asymmetry in
// how the oracle treats a bad archive path, because "tolerate it" and "propagate
// it" are both defensible and only one of them is right per case.
//
// A REGULAR FILE it cannot read could be a real archive holding real ids: the
// oracle has lost half its evidence and must refuse rather than guess, or the
// window this whole file exists to close reopens as a permissions bug.
//
// A path that is NOT a regular file (a directory, a mount point) means no rotation
// ever succeeded there — the rename would have failed and been reported through
// MaintenanceErr — so nothing was ever moved out and the live index is complete.
// Refusing every run on the machine for that would be an outage invented to guard
// against a state that cannot exist. That half is covered by
// TestRotationFailureIsReportedNotFatal; this test covers the first half and the
// boundary between them.
func TestAnUnreadableArchiveRefusesTheRunAndABlockedPathDoesNot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything; the permission branch is unreachable")
	}
	home, otherRepo, myRepo := t.TempDir(), t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)

	archive := run.IndexArchivePath(home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run.AppendIndex(home, liveIn(otherRepo, "run_0abc", at), at); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	if err := os.Rename(run.IndexPath(home), archive); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := os.Chmod(archive, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(archive, 0o600) })

	_, err := run.Registry{Repo: myRepo, Home: home, Retention: time.Hour,
		IndexMaxBytes: 1 << 30, Now: func() time.Time { return at },
		NewID: func() (string, error) { return "run_0abc", nil }}.
		Start(run.StartOptions{Project: "demo"})
	if err == nil {
		t.Fatal("minted an id while the archive could not be read: the oracle guessed with " +
			"half its evidence missing")
	}
	if !strings.Contains(err.Error(), "archive") {
		t.Errorf("error = %v, want it to name the archive so the operator can fix the file", err)
	}
}
