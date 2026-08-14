package run_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// The point of this file: every I/O failure must surface as an error. A run
// registry that silently fails to record a run is worse than one that refuses
// to start — the run would execute with no identity and no way to list it.

func requireUnprivileged(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based failures cannot be simulated")
	}
}

func TestWriteRecordFailsOnAnUnwritableDirectory(t *testing.T) {
	requireUnprivileged(t)
	repo := t.TempDir()
	dir := run.RecordsDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	rec := run.Record{RunID: "run_8f21", Repo: repo, Status: run.StatusRunning, PID: 1,
		StartedAt: time.Now().UTC()}
	if err := run.WriteRecord(rec); err == nil {
		t.Fatal("WriteRecord succeeded on a read-only records directory")
	}
	if _, err := (run.Registry{Repo: repo, Home: t.TempDir()}).Start(run.StartOptions{Recipe: "ship"}); err == nil {
		t.Fatal("Start succeeded even though the record could not be written")
	}
}

func TestWriteRecordFailsWhenTheTargetIsADirectory(t *testing.T) {
	repo := t.TempDir()
	path, err := run.RecordPath(repo, "run_8f21")
	if err != nil {
		t.Fatalf("RecordPath: %v", err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := run.Record{RunID: "run_8f21", Repo: repo, Status: run.StatusRunning, PID: 1}
	if err := run.WriteRecord(rec); err == nil {
		t.Fatal("WriteRecord succeeded with a directory in the record's place")
	}
	// The failed rename must not leave the temp file behind.
	ents, err := os.ReadDir(run.RecordsDir(repo))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range ents {
		if len(e.Name()) > 5 && e.Name()[:5] == ".tmp-" {
			t.Errorf("temp file %s left behind after a failed rename", e.Name())
		}
	}
}

func TestReadRecordsFailsOnAnUnreadableDirectory(t *testing.T) {
	requireUnprivileged(t)
	repo := t.TempDir()
	dir := run.RecordsDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := run.ReadRecords(repo); err == nil {
		t.Error("ReadRecords succeeded on an unreadable directory")
	}
	if _, err := (run.Resolver{}).ListRepo(repo); err == nil {
		t.Error("ListRepo succeeded on an unreadable directory")
	}
}

func TestReadRecordOnAMissingRun(t *testing.T) {
	if _, err := run.ReadRecord(t.TempDir(), "run_8f21"); err == nil {
		t.Error("ReadRecord succeeded for a run that was never started")
	}
}

func TestIndexFailsWhenHomeIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec := run.Record{RunID: "run_8f21", Repo: "/repo", Status: run.StatusRunning, PID: 1}
	if err := run.AppendIndex(file, rec, time.Now()); err == nil {
		t.Error("AppendIndex succeeded with a file where the home directory should be")
	}
	if _, err := run.ReadIndex(file); err == nil {
		t.Error("ReadIndex succeeded with a file where the home directory should be")
	}
}

// TestNoHomeIsAnErrorNotAGuess: with neither CORVEX_HOME nor a resolvable user
// home, the registry must refuse rather than scatter run state somewhere.
func TestNoHomeIsAnErrorNotAGuess(t *testing.T) {
	t.Setenv(run.HomeEnv, "")
	t.Setenv("HOME", "")
	if _, err := run.Home(); err == nil {
		t.Skip("this platform resolves a home directory without $HOME")
	}
	if _, err := (run.Registry{Repo: t.TempDir()}).Start(run.StartOptions{Recipe: "ship"}); err == nil {
		t.Error("Start succeeded with no resolvable corvex home")
	}
	if _, err := (run.Resolver{}).List(); err == nil {
		t.Error("List succeeded with no resolvable corvex home")
	}
	if _, _, err := (run.Resolver{}).Get("run_8f21"); err == nil {
		t.Error("Get succeeded with no resolvable corvex home")
	}
}

func TestListFailsOnAnUnreadableIndex(t *testing.T) {
	requireUnprivileged(t)
	home := t.TempDir()
	rec := run.Record{RunID: "run_8f21", Repo: "/repo", Status: run.StatusRunning, PID: 1}
	if err := run.AppendIndex(home, rec, time.Now()); err != nil {
		t.Fatalf("AppendIndex: %v", err)
	}
	path := run.IndexPath(home)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if _, err := (run.Resolver{Home: home}).List(); err == nil {
		t.Error("List succeeded on an unreadable index")
	}
}
