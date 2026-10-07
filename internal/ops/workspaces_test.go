package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
)

// The global index remembers every repository a run ever started in, and most
// of those were scratch: a test's temp dir, a session's scratchpad. MEASURED on
// a real machine: 54 of the 57 repositories the dispatch form offered no longer
// existed, and the three that did were lost among them. A repository that is
// gone cannot be dispatched into and has no gate to show, so it is not offered.
func TestWorkspaces_LeavesOutRepositoriesThatNoLongerExist(t *testing.T) {
	home := t.TempDir()
	here := t.TempDir()
	alive := t.TempDir()
	gone := filepath.Join(t.TempDir(), "apagado")
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for i, repo := range []string{alive, gone} {
		rec := run.Record{RunID: "run_000" + string(rune('a'+i)), Repo: repo, Status: run.StatusDone, StartedAt: now}
		if err := run.AppendIndex(home, rec, now); err != nil {
			t.Fatal(err)
		}
	}
	lister := GateLister{Resolver: run.Resolver{Home: home, Now: func() time.Time { return now }, Alive: func(int) bool { return false }}}

	got := map[string]bool{}
	for _, w := range lister.Workspaces(here) {
		got[w.Path] = true
	}
	if !got[CanonicalRepo(here)] || !got[CanonicalRepo(alive)] {
		t.Errorf("a repository that exists was dropped: %v", got)
	}
	if got[CanonicalRepo(gone)] {
		t.Errorf("offered a repository that no longer exists: %v", got)
	}
}

func TestListWorktrees_LeavesOutDeletedCheckouts(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	kept := filepath.Join(t.TempDir(), "kept")
	deleted := filepath.Join(t.TempDir(), "deleted")
	for i, wt := range []string{kept, deleted} {
		if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", wt, "-b", "b"+string(rune('0'+i))).CombinedOutput(); err != nil {
			t.Fatalf("worktree add: %s", out)
		}
	}
	if err := os.RemoveAll(deleted); err != nil {
		t.Fatal(err)
	}

	list, err := ListWorktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	var branches []string
	for _, w := range list {
		branches = append(branches, w.Branch)
	}
	if len(list) != 2 || list[0].Branch != "main" || list[1].Branch != "b0" {
		t.Errorf("worktrees = %v, want [main b0]: the deleted one is not a place to run", branches)
	}
}
