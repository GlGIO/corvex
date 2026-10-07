package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// afterGateTaskMD is one code step guarded by an after-gate. The gate command is
// the probe: it passes only if the worker's file is still UNCOMMITTED when the
// gate runs — which is the only state in which a judge can see the change. The
// reviewer's own prompt says "check git diff for the task's changes", and a diff
// against a HEAD that already holds the change is empty.
func afterGateTaskMD(gateCmd string) string {
	return "---\ndag:\n  S01: []\n---\n\n" +
		"## S01 — Write ⬜ PENDING\n\n```yaml\ntype: general\ngates:\n  - nature: computational\n    command: " + gateCmd + "\n```\n\n" +
		"### O que fazer\nwrite work.txt\n\n### Critérios de sucesso\n- [ ] it exists\n"
}

func runAfterGateTask(t *testing.T, gateCmd string) (dir string, err error, events []Event) {
	t.Helper()
	dir = t.TempDir()
	initGitRepo(t, dir)
	project := "test-after-gate"
	setupProject(t, dir, project, afterGateTaskMD(gateCmd))
	gitCommitAll(t, dir, "add tasks")

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			wd := req.WorkDir
			if wd == "" {
				wd = dir
			}
			if werr := os.WriteFile(filepath.Join(wd, "work.txt"), []byte("done\n"), 0o644); werr != nil {
				t.Errorf("worker could not write: %v", werr)
			}
			return &types.ExecuteResult{Output: "wrote it" + taskReportBlock, CostUSD: 0.07}, nil
		},
	}
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	cfg.Execution.MaxRetries = 0

	ch := make(chan Event, 200)
	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: ch})
	err = orch.Run(context.Background(), project)
	close(ch)
	for ev := range ch {
		events = append(events, ev)
	}
	return dir, err, events
}

func committedFiles(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--name-only", "--format=").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return string(out)
}

func taskStatus(t *testing.T, dir string) types.TaskStatus {
	t.Helper()
	tasks, _, err := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", "test-after-gate", "tasks.md"))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("parse tasks: %v (%d tasks)", err, len(tasks))
	}
	return tasks[0].Status
}

// TestAfterGate_JudgesTheUncommittedChange: the after-gate of a code step runs
// while the change is still a diff. Before this, the step was marked PASSED and
// checkpointed first, so every after-gate — the inferential judge included —
// looked at a tree whose `git diff` was empty.
func TestAfterGate_JudgesTheUncommittedChange(t *testing.T) {
	dir, err, _ := runAfterGateTask(t, `"test -n \"$(git status --porcelain work.txt)\""`)
	if err != nil {
		t.Fatalf("Run = %v; the gate did not see the worker's change as uncommitted", err)
	}
	if got := taskStatus(t, dir); got != types.StatusPassed {
		t.Errorf("S01 = %s, want PASSED", got)
	}
	if !strings.Contains(committedFiles(t, dir), "work.txt") {
		t.Error("the checkpoint after the gate passed must still commit the work")
	}
}

// TestAfterGate_RefusalLeavesNoCheckpoint: a refused after-gate is a failed
// step, and a failed step has no checkpoint and was never PASSED — not even for
// the length of one ledger line.
func TestAfterGate_RefusalLeavesNoCheckpoint(t *testing.T) {
	dir, err, events := runAfterGateTask(t, `"false"`)
	if err == nil {
		t.Fatal("a refused after-gate must fail the run")
	}
	if got := taskStatus(t, dir); got != types.StatusFailed {
		t.Errorf("S01 = %s, want FAILED", got)
	}
	if strings.Contains(committedFiles(t, dir), "work.txt") {
		t.Error("the refused work was checkpointed: the commit happened before the gate")
	}
	var workerSpend float64
	for _, ev := range events {
		if ev.TaskID == "S01" && ev.Type == event.AttemptCost && ev.Phase == event.PhaseWorker {
			workerSpend += ev.CostUSD
		}
	}
	if workerSpend == 0 {
		t.Error("the refused attempts' worker spend never reached the ledger")
	}
	for _, ev := range events {
		if ev.TaskID == "S01" && ev.Type == EventTaskComplete && ev.Status == types.StatusPassed {
			t.Error("S01 was announced PASSED before its after-gate refused it")
		}
		if ev.TaskID == "S01" && ev.Type == EventCheckpoint {
			t.Error("a checkpoint line was written for a step its gate refused")
		}
	}
}

// TestAfterGate_ACheckRefusalSendsTheWorkerBackOnce: a lint that fails after a
// clean review is a diagnosis, not a verdict. The worker gets the check's output
// and one more go; a worker that only fixes it when told is how this is seen.
func TestAfterGate_ACheckRefusalSendsTheWorkerBackOnce(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-after-gate"
	setupProject(t, dir, project, afterGateTaskMD(`"test -f fixed.txt || { echo 'lint: fixed.txt missing'; exit 1; }"`))
	gitCommitAll(t, dir, "add tasks")

	var sawDiagnosis bool
	keptTree := true
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			if strings.Contains(req.Prompt, "lint: fixed.txt missing") {
				sawDiagnosis = true
				// The repair is about THIS change: the tree must still hold it.
				if _, err := os.Stat(filepath.Join(dir, "work.txt")); err != nil {
					keptTree = false
				}
				_ = os.WriteFile(filepath.Join(dir, "fixed.txt"), []byte("ok\n"), 0o644)
			}
			_ = os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644)
			return &types.ExecuteResult{Output: "wrote it" + taskReportBlock}, nil
		},
	}
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	ch := make(chan Event, 200)
	go func() {
		for range ch {
		}
	}()
	err := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: ch}).Run(context.Background(), project)
	close(ch)
	if err != nil {
		t.Fatalf("Run = %v; the worker should have been sent back with the check's output", err)
	}
	if !sawDiagnosis {
		t.Error("the retry prompt never carried what the check printed")
	}
	if !keptTree {
		t.Error("the repair attempt started from a reset tree: the worker was told to fix a change that had been deleted")
	}
	if got := taskStatus(t, dir); got != types.StatusPassed {
		t.Errorf("S01 = %s, want PASSED after the repair", got)
	}
}

func TestLedgerEntryFromEvent_CarriesTheModel(t *testing.T) {
	if got := ledgerEntryFromEvent(Event{Type: EventTaskComplete, Model: "claude-opus-5-5"}).Model; got != "claude-opus-5-5" {
		t.Errorf("ledger entry model = %q: the hop dropped it", got)
	}
}
