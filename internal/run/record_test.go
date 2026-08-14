package run_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

func sampleRecord(repo, id string, at time.Time) run.Record {
	return run.Record{
		RunID:     id,
		Repo:      repo,
		Project:   "demo",
		PID:       4242,
		Host:      "test-host",
		Status:    run.StatusRunning,
		StartedAt: at,
		UpdatedAt: at,
	}
}

func TestWriteReadRecordRoundTrip(t *testing.T) {
	repo := t.TempDir()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	want := sampleRecord(repo, "run_8f21", at)

	if err := run.WriteRecord(want); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	got, err := run.ReadRecord(repo, "run_8f21")
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if got.RunID != want.RunID || got.Repo != want.Repo || got.Project != want.Project ||
		got.PID != want.PID || got.Host != want.Host || got.Status != want.Status {
		t.Errorf("record round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.StartedAt.Equal(at) || !got.UpdatedAt.Equal(at) {
		t.Errorf("timestamps: got %v/%v, want %v", got.StartedAt, got.UpdatedAt, at)
	}

	// The path is the documented one, and the directory is gitignored: records
	// carry a pid and a machine path, they must never enter a commit.
	path := filepath.Join(repo, ".corvex", "runs", "run_8f21.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected record at %s: %v", path, err)
	}
	ignore, err := os.ReadFile(filepath.Join(repo, ".corvex", "runs", ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if strings.TrimSpace(string(ignore)) != "*" {
		t.Errorf(".gitignore = %q, want \"*\"", ignore)
	}
}

func TestWriteRecordLeavesNoTempFileBehind(t *testing.T) {
	repo := t.TempDir()
	at := time.Now().UTC()
	rec := sampleRecord(repo, "run_1111", at)
	for i := 0; i < 5; i++ {
		rec.UpdatedAt = at.Add(time.Duration(i) * time.Second)
		if err := run.WriteRecord(rec); err != nil {
			t.Fatalf("WriteRecord %d: %v", i, err)
		}
	}
	ents, err := os.ReadDir(run.RecordsDir(repo))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp file %s survived the rename", e.Name())
		}
	}
	if len(names) != 2 { // the record plus .gitignore
		t.Errorf("directory entries = %v, want exactly the record and .gitignore", names)
	}
}

func TestRecordPathRefusesMalformedIDs(t *testing.T) {
	repo := t.TempDir()
	for _, id := range []string{"", "run_", "../../etc/passwd", "run_../../escape", "run_A"} {
		if _, err := run.RecordPath(repo, id); err == nil {
			t.Errorf("RecordPath(%q) succeeded; a malformed id must not build a path", id)
		}
		if err := run.WriteRecord(run.Record{RunID: id, Repo: repo}); err == nil {
			t.Errorf("WriteRecord with id %q succeeded", id)
		}
	}
	if _, err := run.RecordPath("", "run_8f21"); err == nil {
		t.Error("RecordPath with an empty repo succeeded")
	}
	// Nothing was created outside the records dir.
	if _, err := os.Stat(filepath.Join(repo, ".corvex")); err == nil {
		t.Error("a rejected write created .corvex anyway")
	}
}

// TestCorruptRecordDoesNotTakeDownTheListing is the atomic-write safety net seen
// from the reader's side: whatever garbage is in the directory, the runs that
// are readable still get listed.
func TestCorruptRecordDoesNotTakeDownTheListing(t *testing.T) {
	repo := t.TempDir()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	good := sampleRecord(repo, "run_600d", now)
	if err := run.WriteRecord(good); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	dir := run.RecordsDir(repo)
	junk := map[string]string{
		"run_dead.json":      `{"run_id":"run_dead","pid":1,"star`, // truncated mid-write
		"run_empty.json":     ``,                                   // zero-length
		"run_noid.json":      `{"pid":7}`,                          // parses, but identifies nothing
		"notes.txt":          `not json at all`,                    // wrong extension
		".tmp-run_x.json-99": `{"run_id":"run_xxxx"}`,              // an abandoned temp file
	}
	for name, body := range junk {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "run_dir.json"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	recs, err := run.ReadRecords(repo)
	if err != nil {
		t.Fatalf("ReadRecords: %v", err)
	}
	if len(recs) != 1 || recs[0].RunID != "run_600d" {
		t.Fatalf("ReadRecords = %+v, want just run_600d", recs)
	}

	res := run.Resolver{Now: func() time.Time { return now }, Alive: alwaysAlive, Host: "test-host"}
	views, err := res.ListRepo(repo)
	if err != nil {
		t.Fatalf("ListRepo: %v", err)
	}
	if len(views) != 1 || views[0].Liveness != run.LivenessAlive {
		t.Fatalf("ListRepo = %+v, want one alive run", views)
	}
}

func TestReadRecordsOnFreshRepo(t *testing.T) {
	recs, err := run.ReadRecords(t.TempDir())
	if err != nil {
		t.Fatalf("ReadRecords on a repo with no runs: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("got %d records, want none", len(recs))
	}
	views, err := run.Resolver{}.ListRepo(t.TempDir())
	if err != nil || len(views) != 0 {
		t.Errorf("ListRepo = %v, %v; want empty and no error", views, err)
	}
}

func TestSortRecordsIsDeterministic(t *testing.T) {
	base := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	recs := []run.Record{
		{RunID: "run_bbbb", StartedAt: base},
		{RunID: "run_cccc", StartedAt: base.Add(time.Hour)},
		{RunID: "run_aaaa", StartedAt: base},
	}
	run.SortRecords(recs)
	want := []string{"run_cccc", "run_aaaa", "run_bbbb"} // newest first, ties by id
	for i, id := range want {
		if recs[i].RunID != id {
			t.Fatalf("order = %v, want %v", ids(recs), want)
		}
	}
}

func TestFreshnessFallsBackToStartedAt(t *testing.T) {
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	rec := run.Record{StartedAt: at}
	if !rec.Freshness().Equal(at) {
		t.Errorf("Freshness = %v, want started_at %v", rec.Freshness(), at)
	}
	rec.UpdatedAt = at.Add(time.Minute)
	if !rec.Freshness().Equal(at.Add(time.Minute)) {
		t.Errorf("Freshness = %v, want updated_at", rec.Freshness())
	}
}

func TestStatusIsTerminal(t *testing.T) {
	terminal := []run.Status{run.StatusDone, run.StatusFailed, run.StatusCanceled}
	open := []run.Status{run.StatusRunning, run.StatusParked, "", "gating-on-something-f2-adds"}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q.IsTerminal() = false", s)
		}
	}
	for _, s := range open {
		if s.IsTerminal() {
			t.Errorf("%q.IsTerminal() = true; unknown statuses must stay non-terminal so "+
				"liveness falls back to pid + heartbeat", s)
		}
	}
}

func ids(recs []run.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.RunID
	}
	return out
}

func alwaysAlive(int) bool { return true }
func neverAlive(int) bool  { return false }
