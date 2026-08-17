package cmd

// The activity ledger is the ONE corvex artifact that reaches the user's git
// history, so what it must not contain is as much a contract as what it must.
//
// This is not a golden. A golden compares scrubbed text, and the scrubber is
// exactly what hides the defect these tests exist for: the F1 golden
// `run_identity_inspect_task_json` printed `"repo": "<TMP>"` — a normaliser
// standing in for `/private/var/folders/.../repo` on the machine that recorded
// it, and for `/Users/<username>/projects/...` on a real user's. So these tests
// read the RAW BYTES the run left on disk and assert the machine is not in them.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// machinePaths returns the absolute path spellings that must never appear in a
// committed file: the repo root (both as handed out and as fully resolved, since
// macOS temp dirs are symlinks), the real user home, and the username.
func machinePaths(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := map[string]string{"the repo root": repo}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil && resolved != repo {
		out["the resolved repo root"] = resolved
	}
	// The home directory, which on macOS and Linux alike embeds the username. The
	// bare username is deliberately NOT searched for: it is a short, arbitrary
	// string that can appear inside an unrelated word, and every realistic way it
	// reaches a ledger line is as part of a path, which this already covers.
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		out["the user's home"] = home
	}
	return out
}

// firstLineContaining returns the first line holding needle, so a failure shows
// the offending bytes instead of the whole ledger.
func firstLineContaining(body, needle string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// TestCharacterizeLedgerCommittedLinesCarryNoMachinePath is the regression test
// for the F1 privacy defect: `corvex run` stamped `repo` = absolute path on every
// activity.jsonl line, and activity.jsonl is committed in practice by corvex's own
// auto_commit (proved in this repo's history: `git log --all --diff-filter=A --
// '.corvex/tasks/pilot-feedback/activity.jsonl'` lands on 101a40d "corvex:
// checkpoint S01").
//
// Before the fix every line held `"repo":"/private/var/folders/.../repo"`, which on
// a user's machine reads `/Users/<username>/…` — username and directory layout,
// published to whoever clones the repo. The run identity that a reader actually
// needs (run_id) stays; the absolute path lives in the record and the global
// index, which are gitignored and $HOME-private respectively.
func TestCharacterizeLedgerCommittedLinesCarryNoMachinePath(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("run: %v", err)
	}

	rel := filepath.Join(".corvex", "tasks", "alpha", "activity.jsonl")
	raw := f.Read(rel)
	if !strings.Contains(raw, `"run_id":"run_`) {
		t.Fatalf("the run wrote no identified ledger line, so this test proves nothing:\n%s", raw)
	}
	for what, path := range machinePaths(t, f.Dir) {
		if strings.Contains(raw, path) {
			t.Errorf("activity.jsonl leaks %s (%q) into a committed file, e.g.\n%s",
				what, path, firstLineContaining(raw, path))
		}
	}

	// The premise: this file is not ignored, so those bytes are the ones that go
	// into a commit. If a future change starts ignoring it, that is a different
	// (and worse) design and this test should be reconsidered, not deleted.
	cmd := exec.Command("git", "check-ignore", "-q", rel)
	cmd.Dir = f.Dir
	if err := cmd.Run(); err == nil {
		t.Errorf("%s is gitignored — the ledger stopped being a versioned artifact", rel)
	}
}

// TestCharacterizeRunRecordKeepsTheAbsolutePathAndStaysIgnored is the other half
// of the trade: dropping `repo` from the ledger must not lose the path. It moves
// nowhere new — it is already in `<repo>/.corvex/runs/<run_id>.json`, which F1
// gitignores with `*` on first write, and the run_id on the ledger line is the
// join key. So "which directory did run_ab12 execute in?" is still answerable,
// from a file that never reaches a commit.
func TestCharacterizeRunRecordKeepsTheAbsolutePathAndStaysIgnored(t *testing.T) {
	stubClaude(t, runStubPass)
	f := newFixture(t).AddProject("alpha", fixtureSpecMD, runTasksOnePendingMD).GitInit()
	runSeedAnchor(f, "alpha", fixtureSpecMD)

	if _, _, err := runCLIIn(t, f.Dir, "run", "alpha", "--plain", "--yes"); err != nil {
		t.Fatalf("run: %v", err)
	}

	records, err := filepath.Glob(f.Path(".corvex", "runs", "run_*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("expected one run record, got %v (%v)", records, err)
	}
	body, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"repo"`) {
		t.Errorf("the run record dropped the repo path too — nothing now resolves a run to its directory:\n%s", body)
	}

	// And it is ignored, so holding the absolute path there costs the user nothing.
	cmd := exec.Command("git", "check-ignore", "-q", filepath.Join(".corvex", "runs", filepath.Base(records[0])))
	cmd.Dir = f.Dir
	if err := cmd.Run(); err != nil {
		t.Errorf("the run record is NOT gitignored, so it now leaks what the ledger stopped leaking")
	}
}
