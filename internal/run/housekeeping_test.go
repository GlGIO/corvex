package run_test

// The edges of the two new mechanisms: what happens when housekeeping cannot run,
// and what happens to machine identity when the home is hostile.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// TestRotationFailureIsReportedNotFatal draws the line between the two kinds of
// failure this work introduced. Identity is a precondition: no id, no run. Index
// housekeeping is not: the file staying too big costs nothing anybody can see this
// minute, and refusing the run over it would be a self-inflicted outage. It is
// still reported, because "the index is never pruned" is exactly the kind of thing
// that is discovered a year late.
func TestRotationFailureIsReportedNotFatal(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)

	// Enough history to be over the threshold...
	for i := 0; i < 20; i++ {
		rec := run.Record{RunID: idAt(i), Repo: repo, Status: run.StatusDone,
			StartedAt: at.Add(-48 * time.Hour), UpdatedAt: at.Add(-48 * time.Hour)}
		if err := run.AppendIndex(home, rec, at.Add(-48*time.Hour)); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	// ...and a directory sitting exactly where the archive has to go, so the
	// rename cannot succeed.
	if err := os.MkdirAll(run.IndexArchivePath(home), 0o755); err != nil {
		t.Fatalf("mkdir archive blocker: %v", err)
	}

	h, err := run.Registry{Repo: repo, Home: home, IndexMaxBytes: 512,
		Retention: time.Hour, Now: func() time.Time { return at },
		NewID: func() (string, error) { return "run_beef", nil }}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start refused a run because housekeeping failed: %v", err)
	}
	if h.MaintenanceErr() == nil {
		t.Error("MaintenanceErr is nil though the index could not be rotated: the failure is invisible")
	} else if !strings.Contains(h.MaintenanceErr().Error(), "rotate") {
		t.Errorf("MaintenanceErr = %v, want it to name the rotation", h.MaintenanceErr())
	}
	// The run itself is intact: id, record, index line.
	if h.RunID() != "run_beef" {
		t.Errorf("run id = %q", h.RunID())
	}
	if _, err := run.ReadRecord(repo, "run_beef"); err != nil {
		t.Errorf("ReadRecord: %v", err)
	}
}

// TestRotationYieldsToAnotherProcess: rotation is the one operation here that
// rewrites shared state, so two processes must not do it at once. The lock is a
// file, and a lock left behind by a process that died is cleared after a while
// rather than stopping housekeeping forever.
func TestRotationYieldsToAnotherProcess(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	old := at.Add(-48 * time.Hour)
	for i := 0; i < 20; i++ {
		rec := run.Record{RunID: idAt(i), Repo: repo, Status: run.StatusDone,
			StartedAt: old, UpdatedAt: old}
		if err := run.AppendIndex(home, rec, old); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	lock := run.IndexPath(home) + ".rotating"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("writing lock: %v", err)
	}

	reg := run.Registry{Repo: repo, Home: home, IndexMaxBytes: 512, Retention: time.Hour,
		Now: func() time.Time { return at }, NewID: func() (string, error) { return "run_beef", nil }}
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(run.IndexArchivePath(home)); !os.IsNotExist(err) {
		t.Errorf("rotated while another process held the lock (err=%v)", err)
	}

	// A lock nobody will ever release. Its age is filesystem time, so the test
	// backdates the file rather than moving the injected clock.
	stale := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatalf("backdating the lock: %v", err)
	}
	reg.NewID = func() (string, error) { return "run_cafe", nil }
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start after the lock went stale: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("stale lock still there (err=%v)", err)
	}
	reg.NewID = func() (string, error) { return "run_f00d", nil }
	if _, err := reg.Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(run.IndexArchivePath(home)); err != nil {
		t.Errorf("housekeeping never recovered from an abandoned lock: %v", err)
	}
}

// idAt is a distinct, well-formed id per history entry.
func idAt(i int) string { return fmt.Sprintf("%s%04x", run.IDPrefix, i) }

// TestIndexMaxBytesOverride: the knob, and a malformed value falling back to the
// default rather than to zero (which would rotate on every append).
func TestIndexMaxBytesOverride(t *testing.T) {
	if run.DefaultIndexMaxBytes != 2<<20 {
		t.Errorf("DefaultIndexMaxBytes = %d, want 2 MiB", run.DefaultIndexMaxBytes)
	}
	home, repo := t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	t.Setenv(run.IndexMaxBytesEnv, "not-a-number")

	for i := 0; i < 5; i++ {
		rec := run.Record{RunID: idAt(i), Repo: repo, Status: run.StatusDone,
			StartedAt: at.Add(-48 * time.Hour), UpdatedAt: at.Add(-48 * time.Hour)}
		if err := run.AppendIndex(home, rec, at); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	if _, err := (run.Registry{Repo: repo, Home: home, Retention: time.Hour,
		Now: func() time.Time { return at }, NewID: func() (string, error) { return "run_beef", nil }}).
		Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(run.IndexArchivePath(home)); !os.IsNotExist(err) {
		t.Errorf("a malformed %s was read as zero and rotated a tiny index", run.IndexMaxBytesEnv)
	}

	t.Setenv(run.IndexMaxBytesEnv, "256")
	if _, err := (run.Registry{Repo: repo, Home: home, Retention: time.Hour,
		Now: func() time.Time { return at }, NewID: func() (string, error) { return "run_cafe", nil }}).
		Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(run.IndexArchivePath(home)); err != nil {
		t.Errorf("$%s=256 did not rotate: %v", run.IndexMaxBytesEnv, err)
	}
}

// TestRotationLeavesAnIndexItCannotShrinkAlone: an index over the size threshold
// whose every line is a distinct, still-live run has nothing to prune. Rotating it
// anyway would archive live runs — and then rewrite the same content on every
// subsequent run start, forever.
func TestRotationLeavesAnIndexItCannotShrinkAlone(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		rec := run.Record{RunID: idAt(i), Repo: repo, Status: run.StatusRunning,
			PID: 1000 + i, StartedAt: at, UpdatedAt: at}
		if err := run.AppendIndex(home, rec, at); err != nil {
			t.Fatalf("AppendIndex: %v", err)
		}
	}
	before := countLines(t, run.IndexPath(home))

	if _, err := (run.Registry{Repo: repo, Home: home, IndexMaxBytes: 512, Retention: time.Hour,
		Now: func() time.Time { return at }, NewID: func() (string, error) { return "run_beef", nil }}).
		Start(run.StartOptions{Project: "demo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := os.Stat(run.IndexArchivePath(home)); !os.IsNotExist(err) {
		t.Errorf("archived an index made entirely of live runs (err=%v)", err)
	}
	if got := countLines(t, run.IndexPath(home)); got != before+1 {
		t.Errorf("index holds %d lines, want %d: the live runs were rewritten", got, before+1)
	}
	// And every one of those runs is still addressable, which is the reason the
	// file could not shrink.
	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != before+1 {
		t.Errorf("readable entries = %d, want %d", len(entries), before+1)
	}
}

// TestAnAncientLeftoverClaimDoesNotHoldItsIDForever completes the pair with
// TestLeftoverClaimIsNotHandedOutAgain: a zero-length claim from a Start that died
// mid-way blocks its id (it might be a record about to be written), but not for
// all time — otherwise every crash permanently burns a slot of a 65,536-slot space.
func TestAnAncientLeftoverClaimDoesNotHoldItsIDForever(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(run.RecordsDir(repo), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	claim := filepath.Join(run.RecordsDir(repo), "run_aaaa.json")
	if err := os.WriteFile(claim, nil, 0o644); err != nil {
		t.Fatalf("write claim: %v", err)
	}
	// A claim has no timestamps inside it to read, so its age is the file's.
	ancient := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(claim, ancient, ancient); err != nil {
		t.Fatalf("backdating the claim: %v", err)
	}

	h, err := run.Registry{Repo: repo, Home: home, Retention: time.Hour,
		NewID: func() (string, error) { return "run_aaaa", nil }}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.RunID() != "run_aaaa" {
		t.Errorf("run id = %q, want the recycled run_aaaa", h.RunID())
	}
	if rec, err := run.ReadRecord(repo, "run_aaaa"); err != nil || rec.Status != run.StatusRunning {
		t.Errorf("record = %+v (%v), want the new run", rec, err)
	}
}

func TestMachineIDEdges(t *testing.T) {
	if _, err := run.MachineID(""); err == nil {
		t.Error("MachineID accepted an empty home")
	}

	// A home that cannot be created: a file sits where the directory belongs.
	blocked := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}
	if _, err := run.MachineID(blocked); err == nil {
		t.Error("MachineID invented an identity in a home it cannot create")
	}

	// A machine file holding garbage is replaced, not treated as fatal: one bad
	// file must not send every liveness decision back to the hostname forever.
	home := t.TempDir()
	path := filepath.Join(home, run.MachineFile)
	if err := os.WriteFile(path, []byte("not hex at all\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id, err := run.MachineID(home)
	if err != nil {
		t.Fatalf("MachineID over a garbage file: %v", err)
	}
	if id == "" {
		t.Fatal("MachineID returned empty")
	}
	again, err := run.MachineID(home)
	if err != nil || again != id {
		t.Errorf("identity not stable after the repair: %q then %q (%v)", id, again, err)
	}

	// An injected machine id wins, so a test never depends on the home at all.
	repo := t.TempDir()
	h, err := run.Registry{Repo: repo, Home: t.TempDir(), Machine: "abcdef",
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.Record().Machine != "abcdef" {
		t.Errorf("record machine = %q, want the injected abcdef", h.Record().Machine)
	}
}

// TestFutureSkewIsConfigurable pairs with StaleAfter: both ends of the freshness
// window are tunable, and neither is zero by default.
func TestFutureSkewIsConfigurable(t *testing.T) {
	if run.DefaultFutureSkew <= 0 {
		t.Fatalf("DefaultFutureSkew = %v: a zero tolerance would call every "+
			"sub-second disagreement suspect", run.DefaultFutureSkew)
	}
	now := time.Now()
	rec := run.Record{PID: 10, Host: "box", Status: run.StatusRunning, UpdatedAt: now.Add(time.Minute)}
	tight := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, Host: "box"}
	if got := tight.Liveness(rec); got != run.LivenessStale {
		t.Errorf("a minute in the future with the default tolerance = %q, want stale", got)
	}
	loose := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, Host: "box",
		FutureSkew: time.Hour}
	if got := loose.Liveness(rec); got != run.LivenessAlive {
		t.Errorf("with FutureSkew=1h: liveness = %q, want alive", got)
	}
}
