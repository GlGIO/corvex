package run_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// fixedClock returns a clock that advances a fixed step on every read, so tests
// can distinguish "written at start" from "written by the heartbeat" without
// touching the wall clock.
func fixedClock(start time.Time, step time.Duration) func() time.Time {
	now := start
	return func() time.Time {
		t := now
		now = now.Add(step)
		return t
	}
}

func TestStartRecordsIdentity(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	at := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)

	reg := run.Registry{
		Repo:  repo,
		Home:  home,
		NewID: func() (string, error) { return "run_8f21", nil },
		Now:   func() time.Time { return at },
		PID:   4242,
		Host:  "test-host",
	}
	h, err := reg.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	rec := h.Record()
	if rec.RunID != "run_8f21" || h.RunID() != "run_8f21" {
		t.Errorf("run id = %q", rec.RunID)
	}
	if rec.Repo != repo || rec.Recipe != "ship" || rec.PID != 4242 || rec.Host != "test-host" {
		t.Errorf("record = %+v", rec)
	}
	if rec.Status != run.StatusRunning {
		t.Errorf("status = %q, want running by default", rec.Status)
	}
	if !rec.StartedAt.Equal(at) || !rec.UpdatedAt.Equal(at) {
		t.Errorf("timestamps = %v/%v, want %v", rec.StartedAt, rec.UpdatedAt, at)
	}

	onDisk, err := run.ReadRecord(repo, "run_8f21")
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if onDisk != rec {
		t.Errorf("on-disk record differs:\n got %+v\nwant %+v", onDisk, rec)
	}
	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != "run_8f21" || entries[0].Repo != repo {
		t.Errorf("index = %+v, want one line announcing the run with its repo", entries)
	}
}

func TestStartRequiresAnAbsoluteRepo(t *testing.T) {
	home := t.TempDir()
	for _, repo := range []string{"", ".", "relative/path"} {
		if _, err := (run.Registry{Repo: repo, Home: home}).Start(run.StartOptions{}); err == nil {
			t.Errorf("Start with repo %q succeeded; the recorded repo must be absolute so a "+
				"second process can find it from anywhere", repo)
		}
	}
	if entries, _ := run.ReadIndex(home); len(entries) != 0 {
		t.Errorf("a rejected Start still wrote %d index lines", len(entries))
	}
}

func TestStartRequiresAnExistingRepo(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(t.TempDir(), "typo")
	if _, err := (run.Registry{Repo: missing, Home: home}).Start(run.StartOptions{}); err == nil {
		t.Error("Start invented a repository directory instead of failing")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Errorf("%s was created by a rejected Start", missing)
	}

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := (run.Registry{Repo: file, Home: home}).Start(run.StartOptions{}); err == nil {
		t.Error("Start accepted a file as the repository")
	}
}

func TestStartDefaultsCoverProductionUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv(run.HomeEnv, home)
	repo := t.TempDir()

	h, err := run.Registry{Repo: repo}.Start(run.StartOptions{Project: "demo", Status: run.StatusParked})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rec := h.Record()
	if rec.PID != os.Getpid() {
		t.Errorf("pid = %d, want this process (%d)", rec.PID, os.Getpid())
	}
	if rec.Host == "" {
		t.Error("host not filled from the machine")
	}
	if rec.Status != run.StatusParked {
		t.Errorf("status = %q, want the requested %q", rec.Status, run.StatusParked)
	}
	if rec.StartedAt.IsZero() || rec.UpdatedAt.IsZero() {
		t.Error("timestamps not stamped from the real clock")
	}
	// Home came from the env override, not from the real ~/.corvex.
	if _, err := os.Stat(filepath.Join(home, run.IndexFile)); err != nil {
		t.Errorf("index not written under $%s: %v", run.HomeEnv, err)
	}
}

// TestTwoRunsOfTheSameProjectAreDistinguishable is the other half of the F1
// acceptance criterion, in-process and field by field.
func TestTwoRunsOfTheSameProjectAreDistinguishable(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	clock := fixedClock(time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC), time.Minute)
	reg := run.Registry{Repo: repo, Home: home, Now: clock}

	first, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	second, err := reg.Start(run.StartOptions{Project: "demo"})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if first.RunID() == second.RunID() {
		t.Fatalf("both runs got id %q", first.RunID())
	}
	if first.Record().StartedAt.Equal(second.Record().StartedAt) {
		t.Error("both runs claim the same started_at")
	}

	p1, _ := run.RecordPath(repo, first.RunID())
	p2, _ := run.RecordPath(repo, second.RunID())
	if p1 == p2 {
		t.Fatal("both runs write the same record file")
	}
	// The second run must not have clobbered the first.
	r1, err := run.ReadRecord(repo, first.RunID())
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if r1.RunID != first.RunID() {
		t.Errorf("first record now says %q", r1.RunID)
	}

	views, err := run.Resolver{Home: home, Alive: alwaysAlive, Now: clock}.ListRepo(repo)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("ListRepo = %d runs, want 2", len(views))
	}
}

// TestTouchDoesNotGrowTheIndex locks the design decision that keeps runs.jsonl
// bounded: the heartbeat writes the local record only, and readers get
// freshness by overlaying it.
func TestTouchDoesNotGrowTheIndex(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	at := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	clock := fixedClock(at, 30*time.Second)
	h, err := run.Registry{Repo: repo, Home: home, Now: clock,
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := h.Touch(); err != nil {
			t.Fatalf("Touch %d: %v", i, err)
		}
	}
	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("index lines after 3 heartbeats = %d, want 1", len(entries))
	}
	rec, err := run.ReadRecord(repo, "run_8f21")
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if !rec.UpdatedAt.After(rec.StartedAt) {
		t.Errorf("updated_at %v did not advance past started_at %v", rec.UpdatedAt, rec.StartedAt)
	}
	if !rec.StartedAt.Equal(at) {
		t.Errorf("started_at moved to %v", rec.StartedAt)
	}

	// The listing prefers the fresher local record over the index snapshot.
	views, err := run.Resolver{Home: home, Alive: alwaysAlive, Now: func() time.Time { return at.Add(time.Minute) }}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("List = %d runs, want 1", len(views))
	}
	if views[0].Source != run.SourceRecord {
		t.Errorf("source = %q, want %q", views[0].Source, run.SourceRecord)
	}
	if !views[0].Record.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Errorf("listed updated_at = %v, want the heartbeat's %v", views[0].Record.UpdatedAt, rec.UpdatedAt)
	}
}

func TestSetStatusAppendsASecondLine(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	at := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	h, err := run.Registry{Repo: repo, Home: home, Now: fixedClock(at, time.Minute),
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.SetStatus(run.StatusDone); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := h.SetStatus(""); err == nil {
		t.Error("SetStatus accepted an empty status")
	}

	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("index lines = %d, want 2 (start, then end)", len(entries))
	}
	if entries[0].Status != run.StatusRunning || entries[1].Status != run.StatusDone {
		t.Errorf("statuses = %q, %q; want running then done", entries[0].Status, entries[1].Status)
	}
	if entries[0].StartedAt.IsZero() || !entries[1].StartedAt.Equal(entries[0].StartedAt) {
		t.Error("the second line lost started_at; every line must be a whole snapshot")
	}

	views, err := run.Resolver{Home: home, Alive: alwaysAlive}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 || views[0].Liveness != run.LivenessFinished {
		t.Fatalf("List = %+v, want a single finished run", views)
	}
}

func TestGetByID(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	h, err := run.Registry{Repo: repo, Home: home,
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res := run.Resolver{Home: home, Alive: alwaysAlive}
	view, ok, err := res.Get(h.RunID())
	if err != nil || !ok {
		t.Fatalf("Get(%s) = %v, %v", h.RunID(), ok, err)
	}
	if view.Record.RunID != h.RunID() {
		t.Errorf("got %q", view.Record.RunID)
	}
	if _, ok, err := res.Get("run_ffff"); ok || err != nil {
		t.Errorf("Get(unknown) = %v, %v; want false, nil", ok, err)
	}
}

// TestListFallsBackToTheIndexWhenTheRepoIsGone: a deleted worktree must not make
// the run vanish from the global listing.
func TestListFallsBackToTheIndexWhenTheRepoIsGone(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h, err := run.Registry{Repo: repo, Home: home, PID: 4242,
		NewID: func() (string, error) { return "run_8f21", nil }}.Start(run.StartOptions{Recipe: "ship"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("rm: %v", err)
	}

	views, err := run.Resolver{Home: home, Alive: neverAlive}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 || views[0].Record.RunID != h.RunID() {
		t.Fatalf("List = %+v, want the run still listed from the index", views)
	}
	if views[0].Source != run.SourceIndex {
		t.Errorf("source = %q, want %q", views[0].Source, run.SourceIndex)
	}
	if views[0].Liveness != run.LivenessDead {
		t.Errorf("liveness = %q, want dead", views[0].Liveness)
	}
}
