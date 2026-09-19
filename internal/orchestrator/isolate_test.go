package orchestrator

// A worktree per fan-out item: what it buys, and what it must not cost.
//
// Until now "N items" and "one working tree" were both true, so the runner
// serialised every item that writes (schedule.go). Correct and slow. With
// `isolate: worktree` each item edits its own checkout on its own branch, and a
// generated `merge` node brings it home — a node, so it obeys the graph: an item
// whose steps failed has a blocked merge and never lands.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// isolatedTasksMD is a discovery stage plus a fan-out that writes one file per
// item. Every step is a shell command: the point under test is which TREE the
// work lands in, and a provider in the middle would only add noise.
func isolatedTasksMD(discover string) string {
	return "---\ngenerated_by: corvex-recipe:isolate\ndag:\n    S01: []\n    S02:\n        - S01\n---\n\n" +
		"## S01 — Discover ⬜ PENDING\n\n" +
		"```yaml\nkind: tool\ncommand: " + fmt.Sprintf("%q", discover) + "\nproduces: items\n```\n\n" +
		"### O que fazer\ndiscover\n\n---\n\n" +
		"## S02 — Per item ⬜ PENDING\n\n" +
		"```yaml\ndepends_on: [S01]\nfanout:\n    over: S01\n    isolate: worktree\n    template:\n" +
		"        - id: escrever\n          kind: tool\n          command: \"echo {{ item }} > story-{{ item }}.txt\"\n```\n\n" +
		"### O que fazer\nper item\n"
}

func runIsolated(t *testing.T, project, discover string) (string, []types.Task, error) {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	setupProject(t, dir, project, isolatedTasksMD(discover))
	gitCommitAll(t, dir, "add isolate tasks")

	events := make(chan Event, 500)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	cfg.Execution.Parallel = true
	cfg.Execution.MaxParallel = 3

	err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project)
	close(events)
	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	return dir, tasks, err
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

// Each item works in its own checkout, and every item's work is on the run's
// branch when the fan-out is done.
func TestIsolate_EachItemGetsItsOwnTreeAndComesBack(t *testing.T) {
	dir, tasks, err := runIsolated(t, "isolate-happy", `echo '["alfa","beta"]'`)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The template step of each item is pinned to a worktree, and the two
	// worktrees are different: one shared directory would be the defect this
	// whole feature exists to remove.
	dirs := map[string]string{}
	for _, tk := range tasks {
		if strings.HasSuffix(tk.ID, "/escrever") {
			if tk.WorkDir == "" {
				t.Errorf("%s has no worktree: it ran in the run's own checkout", tk.ID)
			}
			dirs[tk.ID] = tk.WorkDir
		}
	}
	if len(dirs) != 2 {
		t.Fatalf("expected two isolated items, got %v", dirs)
	}
	seen := map[string]bool{}
	for id, d := range dirs {
		if seen[d] {
			t.Errorf("%s shares a checkout with another item (%s)", id, d)
		}
		seen[d] = true
	}

	// The merge node exists per item, runs in the RUN's checkout (no WorkDir),
	// and is what the join waits for.
	merges := 0
	for _, tk := range tasks {
		if strings.HasSuffix(tk.ID, "/merge") {
			merges++
			if tk.WorkDir != "" {
				t.Errorf("%s runs inside the item's worktree: it would merge a branch into itself", tk.ID)
			}
			if tk.Status != types.StatusPassed {
				t.Errorf("%s = %s, want PASSED", tk.ID, tk.Status)
			}
		}
	}
	if merges != 2 {
		t.Errorf("found %d merge nodes, want 2", merges)
	}

	// And the actual product: both files are on the branch the run was on.
	for _, item := range []string{"alfa", "beta"} {
		path := filepath.Join(dir, "story-"+item+".txt")
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("%s never came back to the run's checkout: %v", path, statErr)
		}
	}

	// And nothing about the isolation machinery ended up IN the repository.
	//
	// The assertion is over the HISTORY, not the final tree, and the first
	// version of it looked at the tree and passed while the defect was live.
	// Measured with the guard removed: the checkpoint after one item's merge
	// does `git add -A` while the OTHER items' worktrees are still on disk, git
	// commits each of them as an embedded repository (`warning: adding embedded
	// git repository`, printed into a log nobody reads mid-run), and the next
	// merge deletes the directory so the FOLLOWING commit removes the gitlink
	// again. Net effect on the final tree: nothing. Net effect on the history:
	// commits carrying gitlinks to checkouts that no longer exist, which is what
	// a `git clone` of this repository would then try to make sense of.
	if out := gitIn(t, dir, "log", "--all", "--stat", "--oneline"); strings.Contains(out, ".corvex/worktrees") {
		t.Errorf("a commit carries the run's own worktree as an embedded repository:\n%s",
			out)
	}

	// The worktrees are gone, and gone the way git counts: a directory removed
	// behind git's back leaves a stale entry that breaks the NEXT run.
	if out := gitIn(t, dir, "worktree", "list"); strings.Count(out, "\n") != 1 {
		t.Errorf("worktrees were left registered after the merges:\n%s", out)
	}
}

// An item whose own step fails never lands: its merge is blocked by the
// dependency, not by bookkeeping. The healthy item still comes home.
func TestIsolate_AFailedItemDoesNotLand(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "isolate-failure"
	// `beta` writes its file and then fails, which is the interesting shape: the
	// work EXISTS in the item's tree, and must still not reach the run's branch.
	tasks := "---\ngenerated_by: corvex-recipe:isolate\ndag:\n    S01: []\n    S02:\n        - S01\n---\n\n" +
		"## S01 — Discover ⬜ PENDING\n\n" +
		"```yaml\nkind: tool\ncommand: \"echo '[\\\"alfa\\\",\\\"beta\\\"]'\"\nproduces: items\n```\n\n" +
		"### O que fazer\ndiscover\n\n---\n\n" +
		"## S02 — Per item ⬜ PENDING\n\n" +
		"```yaml\ndepends_on: [S01]\nfanout:\n    over: S01\n    isolate: worktree\n    on_item_failure: continue\n    template:\n" +
		"        - id: escrever\n          kind: tool\n          command: \"echo {{ item }} > story-{{ item }}.txt; test {{ item }} != beta\"\n```\n\n" +
		"### O que fazer\nper item\n"
	setupProject(t, dir, project, tasks)
	gitCommitAll(t, dir, "add isolate tasks")

	events := make(chan Event, 500)
	go func() {
		for range events {
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	cfg.Execution.Parallel = true
	cfg.Execution.MaxParallel = 3
	_ = New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project)
	close(events)

	if _, err := os.Stat(filepath.Join(dir, "story-alfa.txt")); err != nil {
		t.Errorf("the healthy item did not land: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "story-beta.txt")); err == nil {
		t.Error("the FAILED item landed on the run's branch — the work existed in its tree, and that is exactly what must not be enough")
	}
	// Its worktree is still on disk: the work in it is the only copy, and a
	// person is about to want to look at it.
	if out := gitIn(t, dir, "worktree", "list"); !strings.Contains(out, "S02-001") {
		t.Errorf("the failed item's worktree was removed; its work was the only copy:\n%s", out)
	}
}

// `always`: a step whose value is in OBSERVING re-runs even when it passed.
//
// Resumability is right for building and wrong for measuring. A post-deploy
// probe asks production "did the failure stop"; skipped because it passed
// yesterday, it answers with yesterday's number and the hole looks covered.
// MEASURED on a real probe recipe before this existed: the second run never
// executed the query and went straight to a human gate announcing success.
func TestAlways_ReObservesAndDragsItsDependentsAlong(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "probe"
	marker := filepath.Join(t.TempDir(), "runs")

	// Both steps are already PASSED on disk, which is the state a second run
	// starts from. Only S01 declares `always`; S02 must be dragged along,
	// because a gate or a report that stays PASSED would show the previous
	// observation next to the new one.
	tasks := "---\ngenerated_by: corvex-recipe:probe\ndag:\n    S01: []\n    S02:\n        - S01\n---\n\n" +
		"## S01 — Reconsulta ✅ PASSED\n\n```yaml\nkind: test\nalways: true\ncommand: \"echo s01 >> " + marker + "\"\n```\n\n### O que fazer\nprobe\n\n---\n\n" +
		"## S02 — O veredito ✅ PASSED\n\n```yaml\nkind: tool\ndepends_on: [S01]\ncommand: \"echo s02 >> " + marker + "\"\n```\n\n### O que fazer\nverdict\n"
	setupProject(t, dir, project, tasks)
	gitCommitAll(t, dir, "add probe tasks")

	events := make(chan Event, 200)
	go func() {
		for range events {
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false

	if err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("neither step ran again: %v", err)
	}
	if got := strings.Count(string(data), "s01"); got != 1 {
		t.Errorf("the `always` step ran %d times, want 1 — it was skipped as PASSED", got)
	}
	if got := strings.Count(string(data), "s02"); got != 1 {
		t.Errorf("the dependent ran %d times, want 1 — it kept the verdict of the previous observation", got)
	}
}

// And the ordinary case is untouched: a PASSED step with no `always` stays done,
// which is the property that makes a resume worth having.
func TestAlways_LeavesOrdinaryPassedStepsAlone(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "resume"
	marker := filepath.Join(t.TempDir(), "runs")
	tasks := "---\ngenerated_by: corvex-recipe:resume\ndag:\n    S01: []\n    S02:\n        - S01\n---\n\n" +
		"## S01 — Já feito ✅ PASSED\n\n```yaml\nkind: tool\ncommand: \"echo s01 >> " + marker + "\"\n```\n\n### O que fazer\nx\n\n---\n\n" +
		"## S02 — Falta este ⬜ PENDING\n\n```yaml\nkind: tool\ndepends_on: [S01]\ncommand: \"echo s02 >> " + marker + "\"\n```\n\n### O que fazer\ny\n"
	setupProject(t, dir, project, tasks)
	gitCommitAll(t, dir, "add resume tasks")

	events := make(chan Event, 200)
	go func() {
		for range events {
		}
	}()
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false
	if err := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events}).Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	data, _ := os.ReadFile(marker)
	if strings.Contains(string(data), "s01") {
		t.Error("a PASSED step with no `always` was re-executed: the resume stopped being a resume")
	}
	if !strings.Contains(string(data), "s02") {
		t.Error("the pending step did not run")
	}
}

// Every attempt a reviewer rejected is on the ledger — including the LAST one.
//
// The recording condition was `attempt < maxRetries`, which billed every
// rejected attempt except the one after which the task fails: the attempt no
// other line will ever cover, and the one a person reading a failed task looks
// at first. Measured against a reviewer that rejects twice with a cap of two:
// two worker calls happened, $0.20 was paid, and the ledger carried $0.10.
//
// The assertion reads the LEDGER FILE rather than the event stream: the event is
// only worth something if it survives the hop to disk, and that hop is a
// different package.
func TestLedger_BillsEveryRejectedAttempt(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "reprova"
	tasks := "---\ngenerated_by: t\ndag:\n    S01: []\n---\n\n" +
		"## S01 — O reviewer nunca aceita ⬜ PENDING\n\n```yaml\nkind: code\ntype: general\n```\n\n" +
		"### O que fazer\nescreva algo\n\n### Critérios de sucesso\n- [ ] x\n"
	setupProject(t, dir, project, tasks)
	gitCommitAll(t, dir, "add task")

	events := make(chan Event, 300)
	go func() {
		for range events {
		}
	}()
	mock := &mockProvider{executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		if strings.Contains(req.Prompt, "code reviewer") {
			return &types.ExecuteResult{Output: "Faltou o caso nulo.\nVERDICT: FAIL\nCATEGORY: correctness", CostUSD: 0.10}, nil
		}
		return &types.ExecuteResult{Output: "feito" + taskReportBlock, CostUSD: 0.10}, nil
	}}
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 1 // two attempts in total
	cfg.Execution.AutoCommit = false

	_ = New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events}).Run(context.Background(), project)
	close(events)

	ledger, err := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", project, "activity.jsonl"))
	if err != nil {
		t.Fatalf("no ledger: %v", err)
	}
	if got := strings.Count(string(ledger), `"type":"attempt_cost"`); got != 2 {
		t.Errorf("the ledger carries %d attempt_cost line(s) for two paid attempts:\n%s", got, ledger)
	}
}
