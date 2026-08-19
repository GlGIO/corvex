package run_test

// The control file, on its own terms: it is written by one process and read by
// another, it never pretends to be a run record, and it does not survive the run
// it was written for.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// TestPauseControlFileCrossesProcesses is the claim the whole mechanism rests
// on: the process that asks for a pause is never the process that pauses. A
// helper writes the file and exits; this process — which never saw it written —
// reads it back.
func TestPauseControlFileCrossesProcesses(t *testing.T) {
	repo := t.TempDir()
	const id = "run_be11"

	if _, paused, err := run.PauseRequested(repo, id); err != nil || paused {
		t.Fatalf("PauseRequested before anybody paused = %v, %v; want false, nil", paused, err)
	}

	at := time.Now().UTC().Truncate(time.Second)
	runHelper(t, "pause", nil, helperRepo+"="+repo, helperRunID+"="+id)

	req, paused, err := run.PauseRequested(repo, id)
	if err != nil {
		t.Fatalf("PauseRequested: %v", err)
	}
	if !paused {
		t.Fatal("a pause written by another process is invisible here — the file is the only channel there is")
	}
	if req.RunID != id {
		t.Errorf("run_id in the control file = %q, want %q", req.RunID, id)
	}
	if req.PausedAt.Before(at.Add(-time.Minute)) || req.PausedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("paused_at = %s, which is not around now", req.PausedAt)
	}

	if err := run.ClearPause(repo, id); err != nil {
		t.Fatalf("ClearPause: %v", err)
	}
	if _, paused, _ := run.PauseRequested(repo, id); paused {
		t.Error("still paused after ClearPause")
	}
	// Idempotent: both `run resume` and the run's own teardown call it, and
	// neither can know whether the other got there first.
	if err := run.ClearPause(repo, id); err != nil {
		t.Errorf("second ClearPause: %v", err)
	}
}

// TestPauseControlFileIsNotARecord is the negative control for where the file
// lives. It sits in RecordsDir next to the run record, and ReadRecords reads
// every *.json in there — so a control file named like a record would show up as
// a second row for the same run in every listing on the machine.
func TestPauseControlFileIsNotARecord(t *testing.T) {
	repo := t.TempDir()
	const id = "run_be12"
	rec := run.Record{RunID: id, Repo: repo, Project: "demo", Status: run.StatusRunning,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := run.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	if err := run.RequestPause(repo, id, time.Now()); err != nil {
		t.Fatalf("RequestPause: %v", err)
	}

	path, err := run.PausePath(repo, id)
	if err != nil {
		t.Fatalf("PausePath: %v", err)
	}
	if strings.HasSuffix(path, ".json") {
		t.Fatalf("PausePath %s ends in .json: ReadRecords would parse it as a record", path)
	}
	if dir := filepath.Dir(path); dir != run.RecordsDir(repo) {
		t.Errorf("control file lives in %s, want %s — it is per-machine scratch and the `*` gitignore is there",
			dir, run.RecordsDir(repo))
	}

	recs, err := run.ReadRecords(repo)
	if err != nil {
		t.Fatalf("ReadRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("ReadRecords returned %d rows for one run: the control file leaked into the listing", len(recs))
	}
}

// TestPauseHoldsEvenWhenTheFileIsGarbage: presence decides, content does not. A
// run that ignored a pause because the JSON was truncated by a crash would fail
// in the direction that keeps spending money.
func TestPauseHoldsEvenWhenTheFileIsGarbage(t *testing.T) {
	repo := t.TempDir()
	const id = "run_be13"
	if err := run.RequestPause(repo, id, time.Now()); err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	path, _ := run.PausePath(repo, id)
	if err := os.WriteFile(path, []byte(`{"run_id":`), 0o644); err != nil {
		t.Fatalf("truncating the control file: %v", err)
	}
	req, paused, err := run.PauseRequested(repo, id)
	if err != nil {
		t.Fatalf("PauseRequested on a torn file: %v", err)
	}
	if !paused {
		t.Fatal("a torn control file read as `not paused`: the run would walk on")
	}
	if req.RunID != "" {
		t.Errorf("a torn file produced a request %+v; the zero value is the honest answer", req)
	}
}

// TestPausePathRefusesAnIdItCannotSpell: the path is built from a run id that
// arrives over HTTP, so it gets the same guard RecordPath has — a malformed id
// must not build a path that escapes RecordsDir.
func TestPausePathRefusesAnIdItCannotSpell(t *testing.T) {
	for _, id := range []string{"", "../../etc/passwd", "run_zzzz", "nope"} {
		if _, err := run.PausePath(t.TempDir(), id); err == nil {
			t.Errorf("PausePath accepted %q", id)
		}
	}
	if _, err := run.PausePath("", "run_be14"); err == nil {
		t.Error("PausePath accepted an empty repo")
	}
}

// TestClaimingAnIdClearsAnOrphanedPause is the housekeeping half, and it is the
// case a stale control file is actually dangerous in: a run SIGKILLed while
// paused never reaches its own teardown, its record ages out, and the id is
// recyclable. Without this, the next run to draw that id would stop at its first
// wave on an order written for a run that died weeks ago.
func TestClaimingAnIdClearsAnOrphanedPause(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	const id = "run_be15"

	// A pause left behind by a run that is long gone.
	if err := run.RequestPause(repo, id, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if _, paused, _ := run.PauseRequested(repo, id); !paused {
		t.Fatal("fixture: the orphan was not written")
	}

	h, err := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return id, nil }}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.RunID() != id {
		t.Fatalf("fixture: the new run took id %q, not %q", h.RunID(), id)
	}
	if _, paused, _ := run.PauseRequested(repo, id); paused {
		t.Error("a run inherited the pause control file of the dead run whose id it recycled")
	}
}

// TestClaimingAnIdLeavesALivePauseAlone is the negative control for the one
// above: housekeeping that cleared any control file it found would silently
// cancel the pause of the run that is actually using the id.
func TestClaimingAnIdLeavesALivePauseAlone(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	const held, fresh = "run_be16", "run_be17"

	h, err := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return held, nil }}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := run.RequestPause(repo, h.RunID(), time.Now()); err != nil {
		t.Fatalf("RequestPause: %v", err)
	}

	// A second run starts in the same repository and takes a different id.
	if _, err := (run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return fresh, nil }}).Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start (second run): %v", err)
	}

	if _, paused, _ := run.PauseRequested(repo, held); !paused {
		t.Errorf("starting run %s cleared the standing pause of the live run %s", fresh, held)
	}
}
