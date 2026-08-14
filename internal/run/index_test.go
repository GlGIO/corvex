package run_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

func TestHomeHonoursTheEnvOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv(run.HomeEnv, custom)
	got, err := run.Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got != custom {
		t.Fatalf("Home = %q, want %q", got, custom)
	}
	if run.IndexPath(got) != filepath.Join(custom, "runs.jsonl") {
		t.Errorf("IndexPath = %q", run.IndexPath(got))
	}

	// Whitespace-only is treated as unset, otherwise a stray `CORVEX_HOME=" "`
	// would silently write runs into a directory named " ".
	t.Setenv(run.HomeEnv, "   ")
	fallback, err := run.Home()
	if err != nil {
		t.Fatalf("Home without override: %v", err)
	}
	real, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no user home on this machine: %v", err)
	}
	if fallback != filepath.Join(real, ".corvex") {
		t.Errorf("fallback home = %q, want %q", fallback, filepath.Join(real, ".corvex"))
	}
	// Nothing was created: this test resolves a path, it never writes to it.
	if _, err := os.Stat(run.IndexPath(fallback)); err == nil {
		t.Log("note: the real index already exists; this test did not create it")
	}
}

func TestIndexAppendReadConsolidate(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)

	start := run.Record{
		RunID: "run_8f21", Repo: "/repo/a", Recipe: "ship", PID: 99,
		Status: run.StatusRunning, StartedAt: at, UpdatedAt: at,
	}
	end := start
	end.Status = run.StatusDone
	end.UpdatedAt = at.Add(2 * time.Minute)
	other := run.Record{
		RunID: "run_aaaa", Repo: "/repo/b", Project: "demo", PID: 100,
		Status: run.StatusRunning, StartedAt: at.Add(time.Minute), UpdatedAt: at.Add(time.Minute),
	}

	for _, rec := range []run.Record{start, other, end} {
		if err := run.AppendIndex(home, rec, rec.UpdatedAt); err != nil {
			t.Fatalf("AppendIndex %s: %v", rec.RunID, err)
		}
	}

	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("index lines = %d, want 3 (the end of a run is a new line, never a rewrite)", len(entries))
	}
	if entries[2].WrittenAt.IsZero() {
		t.Error("written_at not stamped")
	}

	recs := run.ConsolidateIndex(entries)
	if len(recs) != 2 {
		t.Fatalf("consolidated = %d runs, want 2", len(recs))
	}
	byID := map[string]run.Record{}
	for _, r := range recs {
		byID[r.RunID] = r
	}
	if got := byID["run_8f21"]; got.Status != run.StatusDone {
		t.Errorf("run_8f21 status = %q, want %q (last line wins)", got.Status, run.StatusDone)
	}
	if got := byID["run_8f21"]; got.Recipe != "ship" {
		t.Errorf("run_8f21 recipe = %q, want ship — each line is a whole snapshot", got.Recipe)
	}
	if recs[0].RunID != "run_aaaa" {
		t.Errorf("order = %v, want newest-started first", ids(recs))
	}
}

func TestIndexTolerateGarbageLines(t *testing.T) {
	home := t.TempDir()
	good := run.Record{RunID: "run_600d", Repo: "/repo", Status: run.StatusRunning, PID: 5}
	if err := run.AppendIndex(home, good, time.Now()); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	extra := strings.Join([]string{
		``,
		`   `,
		`{"run_id":"run_torn","pid":`, // a line cut in half
		`not json`,
		`{"pid":7}`,             // no run id: identifies nothing
		`{"run_id":"bad-id"}`,   // malformed id
		`{"run_id":"run_f00d"}`, // fine
	}, "\n") + "\n"
	f, err := os.OpenFile(run.IndexPath(home), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if _, err := f.WriteString(extra); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("parseable lines = %d (%+v), want 2", len(entries), entries)
	}
}

func TestIndexPermissionsAreOwnerOnly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "corvex-home")
	rec := run.Record{RunID: "run_0001", Repo: "/repo", Status: run.StatusRunning, PID: 1}
	if err := run.AppendIndex(home, rec, time.Now()); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	dir, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat home: %v", err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("home dir perm = %o, want 700 (it sits in $HOME)", perm)
	}
	file, err := os.Stat(run.IndexPath(home))
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	if perm := file.Mode().Perm(); perm != 0o600 {
		t.Errorf("index perm = %o, want 600", perm)
	}
}

func TestIndexRejectsBadInput(t *testing.T) {
	home := t.TempDir()
	if err := run.AppendIndex(home, run.Record{RunID: "nope"}, time.Now()); err == nil {
		t.Error("AppendIndex accepted a malformed run id")
	}
	if err := run.AppendIndex("", run.Record{RunID: "run_0001"}, time.Now()); err == nil {
		t.Error("AppendIndex accepted an empty home")
	}
	if _, err := run.ReadIndex(""); err == nil {
		t.Error("ReadIndex accepted an empty home")
	}
	entries, err := run.ReadIndex(t.TempDir())
	if err != nil || len(entries) != 0 {
		t.Errorf("ReadIndex on a machine with no runs = %v, %v; want empty, nil", entries, err)
	}
	// A zero timestamp is stamped rather than rejected.
	if err := run.AppendIndex(home, run.Record{RunID: "run_0002"}, time.Time{}); err != nil {
		t.Fatalf("AppendIndex with a zero time: %v", err)
	}
	entries, _ = run.ReadIndex(home)
	if len(entries) != 1 || entries[0].WrittenAt.IsZero() {
		t.Errorf("entries = %+v, want one line with written_at stamped", entries)
	}
}

func TestIndexSurvivesConcurrentGoroutines(t *testing.T) {
	home := t.TempDir()
	const workers, perWorker = 8, 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				rec := run.Record{
					RunID:     fmt.Sprintf("run_%04x", w*perWorker+i+1),
					Repo:      "/repo/concurrent",
					Status:    run.StatusRunning,
					PID:       os.Getpid(),
					StartedAt: time.Now().UTC(),
				}
				if err := run.AppendIndex(home, rec, time.Now()); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("AppendIndex: %v", err)
	}

	entries, err := run.ReadIndex(home)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(entries) != workers*perWorker {
		t.Fatalf("parseable lines = %d, want %d", len(entries), workers*perWorker)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.RunID] = true
	}
	if len(seen) != workers*perWorker {
		t.Errorf("distinct ids = %d, want %d", len(seen), workers*perWorker)
	}
}
