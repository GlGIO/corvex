package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

func TestRun_DependencyFailureSkipsDependentsContinuesIndependent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-diamond"
	setupProject(t, dir, project, diamondTasksMD)
	gitCommitAll(t, dir, "add diamond tasks")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				// S02 always fails review; everything else passes.
				if strings.Contains(req.Prompt, "## Task: S02 ") {
					return &types.ExecuteResult{Output: "Broken.\nCATEGORY: incomplete\nVERDICT: FAIL"}, nil
				}
				return &types.ExecuteResult{Output: "Looks good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "task completed" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	// AutoCommit on: each PASS checkpoints, so the retry-time recovery reset
	// can't revert prior tasks' committed statuses.
	cfg.Execution.AutoCommit = true
	cfg.Execution.MaxRetries = 1

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})

	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected a summary failure error, got nil")
	}
	if !strings.Contains(err.Error(), "S02") {
		t.Errorf("error %q should name the failed task S02", err)
	}
	if !strings.Contains(err.Error(), "S04") {
		t.Errorf("error %q should name the skipped dependent S04", err)
	}

	// Independent branch S03 must have run; dependent S04 must have been skipped.
	var s03Passed, s04Skipped bool
	for ev := range events {
		if ev.Type == EventTaskComplete && ev.TaskID == "S03" && ev.Status == types.StatusPassed {
			s03Passed = true
		}
		if ev.Type == EventTaskComplete && ev.TaskID == "S04" && ev.Status == types.StatusSkipped {
			s04Skipped = true
		}
	}
	if !s03Passed {
		t.Error("independent task S03 should have run to PASSED despite S02 failing")
	}
	if !s04Skipped {
		t.Error("S04 (depends on failed S02) should have been SKIPPED")
	}

	// Final on-disk statuses.
	tasks, _, perr := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	if perr != nil {
		t.Fatalf("parse tasks: %v", perr)
	}
	statuses := map[string]types.TaskStatus{}
	for _, tk := range tasks {
		statuses[tk.ID] = tk.Status
	}
	if statuses["S01"] != types.StatusPassed {
		t.Errorf("S01 = %s, want PASSED", statuses["S01"])
	}
	if statuses["S02"] != types.StatusFailed {
		t.Errorf("S02 = %s, want FAILED", statuses["S02"])
	}
	if statuses["S03"] != types.StatusPassed {
		t.Errorf("S03 = %s, want PASSED", statuses["S03"])
	}
	if statuses["S04"] != types.StatusSkipped {
		t.Errorf("S04 = %s, want SKIPPED", statuses["S04"])
	}
}

func TestRun_CostCeilingAbortsImmediately(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cost-abort"
	setupProject(t, dir, project, testTasksMD) // S01 → S02
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 100)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
			}
			// Worker burns more than the ceiling on the very first task.
			return &types.ExecuteResult{Output: "done" + taskReportBlock, CostUSD: 10}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false
	cfg.Execution.MaxCostUSD = 5 // first task's $10 blows this

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})

	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected cost-ceiling abort error, got nil")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Errorf("error %q should mention the cost ceiling", err)
	}
	// A fatal cost abort must NOT be downgraded to the skippable-failure summary.
	if strings.Contains(err.Error(), "run completed with failures") {
		t.Errorf("cost ceiling breach was swallowed as a task failure: %q", err)
	}

	// Only S01 should have been attempted before the abort.
	for ev := range events {
		if ev.Type == EventTaskStart && ev.TaskID == "S02" {
			t.Error("S02 should never start after a cost-ceiling abort on S01")
		}
	}
}

func TestRun_ParallelExecution(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-parallel"
	setupProject(t, dir, project, fanOutTasksMD)
	gitCommitAll(t, dir, "add fan-out tasks")

	events := make(chan Event, 500)
	go func() { // drain so emit never blocks
		for range events {
		}
	}()

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "done" + taskReportBlock, CostUSD: 0.01}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	cfg.Execution.Parallel = true
	cfg.Execution.MaxParallel = 3

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("parallel Run() error = %v", err)
	}
	close(events)

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	for _, tk := range tasks {
		if tk.Status != types.StatusPassed {
			t.Errorf("task %s = %s, want PASSED", tk.ID, tk.Status)
		}
	}
}

func TestRun_DAGIntegrityViolationAborts(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-dag-violation"
	setupProject(t, dir, project, dagViolationTasksMD)
	gitCommitAll(t, dir, "add tasks with stale-dep PASSED")

	events := make(chan Event, 100)
	mock := &mockProvider{}
	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{
		Config: cfg, Provider: mock, WorkDir: dir, Events: events,
	})

	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected DAG integrity error, got nil")
	}
	if !strings.Contains(err.Error(), "DAG integrity violation") {
		t.Errorf("error %q missing 'DAG integrity violation'", err)
	}
	if !strings.Contains(err.Error(), "S02") || !strings.Contains(err.Error(), "S01") {
		t.Errorf("error %q should name both offending task and dep", err)
	}

	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount != 0 {
		t.Errorf("expected 0 provider calls on integrity failure, got %d", callCount)
	}
}

func TestRun_ResetsRunningTaskOnResume(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-running-reset"
	tasksPath := setupProject(t, dir, project, runningOnResumeTasksMD)
	gitCommitAll(t, dir, "add interrupted task")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "task completed" + taskReportBlock}, nil
		},
	}
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false

	orch := New(Options{
		Config: cfg, Provider: mock, WorkDir: dir, Events: events,
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	// Provider should have been called for S01 (worker + review = at least 1).
	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount == 0 {
		t.Error("expected provider calls (S01 should re-run after RUNNING reset), got 0")
	}

	// Final state in tasks.md: S01 must be PASSED, not RUNNING.
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		t.Fatalf("parsing final tasks.md: %v", err)
	}
	var s01 *types.Task
	for i := range tasks {
		if tasks[i].ID == "S01" {
			s01 = &tasks[i]
		}
	}
	if s01 == nil {
		t.Fatal("S01 missing from final tasks.md")
	}
	if s01.Status != types.StatusPassed {
		t.Errorf("S01 final status = %q, want PASSED (RUNNING should have been reset and re-run)", s01.Status)
	}
}
