package ops

import (
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// runFixture registers runs in scratch repositories against one scratch index,
// with every clock, probe and machine id injected — the same discipline the gate
// fixture uses, for the same reason.
type runFixture struct {
	home   string
	now    time.Time
	lister RunLister
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	home := t.TempDir()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	resolver := run.Resolver{
		Home:    home,
		Now:     func() time.Time { return now },
		Alive:   func(int) bool { return true },
		Machine: "test-machine",
	}
	return &runFixture{
		home:   home,
		now:    now,
		lister: RunLister{Resolver: resolver, Now: func() time.Time { return now }},
	}
}

// add registers one run in its own repository, started `ago` before now.
func (f *runFixture) add(t *testing.T, id, project string, ago time.Duration, status run.Status) string {
	t.Helper()
	repo := t.TempDir()
	reg := run.Registry{
		Repo:    repo,
		Home:    f.home,
		NewID:   func() (string, error) { return id, nil },
		Now:     func() time.Time { return f.now.Add(-ago) },
		Machine: "test-machine",
	}
	h, err := reg.Start(run.StartOptions{Project: project})
	if err != nil {
		t.Fatalf("registering %s: %v", id, err)
	}
	if err := h.SetStatus(status); err != nil {
		t.Fatalf("SetStatus %s: %v", id, err)
	}
	return repo
}

func TestListRuns_IsCrossRepositoryAndNewestFirst(t *testing.T) {
	f := newRunFixture(t)
	f.add(t, "run_0001", "alpha", 3*time.Hour, run.StatusDone)
	f.add(t, "run_0002", "beta", 1*time.Hour, run.StatusDone)

	rows, err := f.lister.ListRuns(RunListOptions{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (the listing must cross repositories)", len(rows))
	}
	if rows[0].RunID != "run_0002" {
		t.Errorf("first row = %s, want run_0002: newest first", rows[0].RunID)
	}
	if rows[0].Age != time.Hour {
		t.Errorf("age = %s, want 1h", rows[0].Age)
	}
}

func TestListRuns_SinceDropsOlderRuns(t *testing.T) {
	f := newRunFixture(t)
	f.add(t, "run_0001", "alpha", 8*24*time.Hour, run.StatusDone)
	f.add(t, "run_0002", "beta", 2*time.Hour, run.StatusDone)

	rows, err := f.lister.ListRuns(RunListOptions{Since: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 1 || rows[0].RunID != "run_0002" {
		t.Fatalf("--since kept %+v, want only run_0002", rows)
	}
}

func TestListRuns_RepoFilterKeepsOneRepository(t *testing.T) {
	f := newRunFixture(t)
	f.add(t, "run_0001", "alpha", time.Hour, run.StatusDone)
	repoB := f.add(t, "run_0002", "beta", time.Hour, run.StatusDone)

	rows, err := f.lister.ListRuns(RunListOptions{Repo: repoB})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 1 || rows[0].RunID != "run_0002" {
		t.Fatalf("--repo kept %+v, want only run_0002", rows)
	}
}

// The listing must not care how the repository path was spelled. On darwin every
// temp dir is reached through a symlink, so a naive string compare would drop
// every row exactly where the tests live.
func TestListRuns_RepoFilterSurvivesSymlinkedPaths(t *testing.T) {
	f := newRunFixture(t)
	repo := f.add(t, "run_0001", "alpha", time.Hour, run.StatusDone)

	rows, err := f.lister.ListRuns(RunListOptions{Repo: repo + "/."})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows for the same repository spelled differently, want 1", len(rows))
	}
}

func TestFindRun_UnknownIDNamesTheRecycling(t *testing.T) {
	f := newRunFixture(t)
	_, err := f.lister.FindRun("run_dead")
	if err == nil {
		t.Fatal("FindRun on an unknown id returned nil")
	}
	var unknown *UnknownRunError
	if !asUnknownRun(err, &unknown) {
		t.Fatalf("error is %T, want *UnknownRunError so callers can branch", err)
	}
}

func TestLastRunOf_PicksTheMostRecent(t *testing.T) {
	f := newRunFixture(t)
	repo := t.TempDir()
	for _, seed := range []struct {
		id  string
		ago time.Duration
	}{{"run_00aa", 3 * time.Hour}, {"run_00bb", time.Hour}} {
		reg := run.Registry{
			Repo: repo, Home: f.home,
			NewID:   func() (string, error) { return seed.id, nil },
			Now:     func() time.Time { return f.now.Add(-seed.ago) },
			Machine: "test-machine",
		}
		h, err := reg.Start(run.StartOptions{Project: "alpha"})
		if err != nil {
			t.Fatalf("registering %s: %v", seed.id, err)
		}
		if err := h.SetStatus(run.StatusDone); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
	}

	row, ok, err := f.lister.LastRunOf(repo, "alpha")
	if err != nil || !ok {
		t.Fatalf("LastRunOf: %v (ok=%v)", err, ok)
	}
	if row.RunID != "run_00bb" {
		t.Errorf("last run = %s, want run_00bb", row.RunID)
	}
}

func TestIsRunID_SeparatesIdsFromProjectNames(t *testing.T) {
	for arg, want := range map[string]bool{
		"run_8f21": true,
		"run_":     false,
		"alpha":    false,
		"runner":   false,
		"run_zzzz": false,
	} {
		if got := IsRunID(arg); got != want {
			t.Errorf("IsRunID(%q) = %v, want %v", arg, got, want)
		}
	}
}

func asUnknownRun(err error, target **UnknownRunError) bool {
	u, ok := err.(*UnknownRunError)
	if ok {
		*target = u
	}
	return ok
}

// Filtering, and why it exists: waiting for a run by grepping an unfiltered
// listing bites, because the listing is history AND present at once. My own
// wait loop matched the `failed` of a previous run and returned in 3 seconds.
func TestListRuns_FiltersOnStatusAndOnLivenessSeparately(t *testing.T) {
	f := newRunFixture(t)
	f.add(t, "run_0001", "alpha", time.Hour, run.StatusFailed)
	// Started seconds ago, not minutes: liveness needs a FRESH heartbeat, so a
	// run whose record is older than StaleAfter reads `stale` however healthy
	// its status looks. That is the whole reason the two axes are separate
	// flags, and this fixture would be lying if it pretended otherwise.
	f.add(t, "run_0002", "beta", 5*time.Second, run.StatusRunning)
	f.add(t, "run_0003", "gamma", 5*time.Second, run.StatusParked)

	failed, err := f.lister.ListRuns(RunListOptions{Status: run.StatusFailed})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(failed) != 1 || failed[0].RunID != "run_0001" {
		t.Fatalf("--status failed kept %+v, want only run_0001", failed)
	}

	// The other axis. Every run in this fixture has a live probe, so `--live`
	// keeps the two non-terminal ones and drops the finished one — which a
	// status filter could not express, and vice versa.
	live, err := f.lister.ListRuns(RunListOptions{Live: true})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("--live kept %d run(s), want 2 (running + parked): %+v", len(live), live)
	}
	for _, r := range live {
		if r.Status == run.StatusFailed {
			t.Errorf("--live kept a finished run: %s", r.RunID)
		}
	}
}

// A misspelled status must fail loudly. Silently matching nothing would produce
// an empty listing, which reads exactly like "nothing is running" — the answer
// the script waiting on it is looking for.
func TestParseRunStatus_RefusesWhatItCannotMean(t *testing.T) {
	if _, err := ParseRunStatus("dnoe"); err == nil {
		t.Fatal("a misspelled status was accepted")
	}
	if _, err := ParseRunStatus("alive"); err == nil {
		t.Error("`alive` is liveness, not status, and must be refused with a pointer to --live")
	}
	for _, ok := range []string{"", "running", "parked", "done", "failed", "canceled", "canceling"} {
		if _, err := ParseRunStatus(ok); err != nil {
			t.Errorf("ParseRunStatus(%q): %v", ok, err)
		}
	}
}
