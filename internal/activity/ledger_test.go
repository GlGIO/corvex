package activity_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
)

func setupProjectDir(t *testing.T) (workDir, project string) {
	t.Helper()
	workDir = t.TempDir()
	project = "demo"
	dir := filepath.Join(workDir, ".corvex", "tasks", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return workDir, project
}

func TestNew_MissingDirFails(t *testing.T) {
	if _, err := activity.New(t.TempDir(), "nope"); err == nil {
		t.Fatal("expected error for missing project dir, got nil")
	}
}

func TestAppendAndRead_RoundTrip(t *testing.T) {
	workDir, project := setupProjectDir(t)
	l, err := activity.New(workDir, project)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	entries := []activity.Entry{
		{Type: "task_start", TaskID: "S01"},
		{Type: "task_complete", TaskID: "S01", Status: "PASSED", CostUSD: 0.42, DurationMs: 1234},
		{Type: "task_start", TaskID: "S02", Attempt: 1},
	}
	for _, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("entries: got %d, want %d", len(got), len(entries))
	}
	for i, e := range entries {
		if got[i].Type != e.Type || got[i].TaskID != e.TaskID {
			t.Errorf("entry %d: got %+v, want type=%q task=%q", i, got[i], e.Type, e.TaskID)
		}
		if got[i].Timestamp.IsZero() {
			t.Errorf("entry %d: timestamp not stamped", i)
		}
	}
	if got[1].CostUSD != 0.42 || got[1].Status != "PASSED" {
		t.Errorf("cost/status round-trip failed: %+v", got[1])
	}
}

func TestRead_MissingFileReturnsNil(t *testing.T) {
	workDir, project := setupProjectDir(t)
	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read on missing file: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil entries, got %v", got)
	}
}

func TestAppend_NilLedgerNoOp(t *testing.T) {
	var l *activity.Ledger
	if err := l.Append(activity.Entry{Type: "foo"}); err != nil {
		t.Errorf("nil ledger Append should be no-op, got %v", err)
	}
}

func TestRead_TolerantOfMalformedLines(t *testing.T) {
	workDir, project := setupProjectDir(t)
	l, err := activity.New(workDir, project)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Append a valid entry, write a corrupt line directly, then a valid
	// entry. Read should return the two valid ones, silently skipping the bad
	// line — partial writes from crashed runs must not block inspection.
	if err := l.Append(activity.Entry{Type: "ok-1", Timestamp: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workDir, ".corvex", "tasks", project, "activity.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not valid json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := l.Append(activity.Entry{Type: "ok-2", Timestamp: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	got, err := activity.Read(workDir, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after malformed-line skip, got %d", len(got))
	}
	if got[0].Type != "ok-1" || got[1].Type != "ok-2" {
		t.Errorf("entries: %+v", got)
	}
}
