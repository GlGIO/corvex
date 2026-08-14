package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/step"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

func TestRun_MissingHandoffFailsTask(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-handoff-missing"
	setupProject(t, dir, project, testTasksMD) // S01 → S02
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
			}
			// Worker passes review but never emits a TASK-REPORT/HANDOFF.
			return &types.ExecuteResult{Output: "I finished the task."}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected failure when the worker never emits a TASK-REPORT, got nil")
	}
	if !strings.Contains(err.Error(), "TASK-REPORT") {
		t.Errorf("error %q should mention the missing TASK-REPORT", err)
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	for _, tk := range tasks {
		if tk.ID == "S01" && tk.Status != types.StatusFailed {
			t.Errorf("S01 = %s, want FAILED (no handoff)", tk.Status)
		}
	}
}

func TestRun_HandoffPopulatesAnchor(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-handoff-anchor"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "TASK-REPORT:\nSUMMARY: built it.\nDECISIONS:\n- chose approach A\nHANDOFF: call NewThing() to reuse this."}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	state, err := anchor.Load(filepath.Join(dir, ".corvex", "tasks", project, "anchor.yaml"))
	if err != nil {
		t.Fatalf("loading anchor: %v", err)
	}
	if len(state.Completed) == 0 {
		t.Fatal("anchor has no completed tasks")
	}
	first := state.Completed[0]
	if first.Summary != "built it." {
		t.Errorf("Summary = %q, want the worker's report summary", first.Summary)
	}
	if len(first.Decisions) == 0 || first.Decisions[0] != "chose approach A" {
		t.Errorf("Decisions = %v, want the report's decisions", first.Decisions)
	}
	// After S01 the next task is S02, so its handoff context must be recorded.
	if !strings.Contains(state.NextTaskContext, "NewThing") {
		t.Errorf("NextTaskContext = %q, want the worker's HANDOFF", state.NextTaskContext)
	}
}

func TestRun_FailedAttemptsCostTriggersAbort(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cost-abort"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	// Worker returns success with $0.02; reviewer returns FAIL with $0.02.
	// Per-attempt cost = $0.04. After attempt 1 total = $0.08 > $0.05 ceiling.
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "VERDICT: FAIL", CostUSD: 0.02}, nil
			}
			return &types.ExecuteResult{Output: "task work done", CostUSD: 0.02}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false
	cfg.Execution.MaxCostUSD = 0.05
	cfg.Execution.MaxRetries = 5

	orch := New(Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
	})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected cost ceiling error, got nil")
	}
	if !strings.Contains(err.Error(), "run aborted: cumulative cost") {
		t.Errorf("error %q should contain 'run aborted: cumulative cost'", err.Error())
	}
}

func TestRun_IndeterminateVerdictRetriesAndEventuallyFails(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-indeterminate-fail"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				// return empty output — no verdict line → VerdictIndeterminate
				return &types.ExecuteResult{Output: "I looked at the code."}, nil
			}
			return &types.ExecuteResult{Output: "task done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 1
	cfg.Execution.AutoCommit = false

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected error when reviewer never produces a verdict, got nil")
	}
	if !strings.Contains(err.Error(), "reviewer never produced a verdict") {
		t.Errorf("error %q should contain 'reviewer never produced a verdict'", err.Error())
	}
}

func TestRun_IndeterminateVerdictRetryDiagnosisPassedToWorker(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-indeterminate-retry"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	var mu sync.Mutex
	var workerPrompts []string
	reviewerCall := 0

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				mu.Lock()
				n := reviewerCall
				reviewerCall++
				mu.Unlock()
				if n == 0 {
					return &types.ExecuteResult{Output: "I looked at the code."}, nil
				}
				return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
			}
			mu.Lock()
			workerPrompts = append(workerPrompts, req.Prompt)
			mu.Unlock()
			return &types.ExecuteResult{Output: "task done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 2
	cfg.Execution.AutoCommit = false

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	prompts := workerPrompts
	mu.Unlock()

	if len(prompts) < 2 {
		t.Fatalf("expected at least 2 worker calls (initial + retry), got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "parseable verdict") {
		t.Errorf("second worker prompt %q should contain 'parseable verdict' diagnosis", prompts[1])
	}
}

func TestRun_SpawnInvestigationProducesDiagnosisForNextWorker(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-spawn-investigation"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	var mu sync.Mutex
	var workerPrompts []string
	reviewerCall := 0
	providerCall := 0

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			mu.Lock()
			providerCall++
			mu.Unlock()

			if strings.Contains(req.Prompt, "code reviewer") {
				mu.Lock()
				n := reviewerCall
				reviewerCall++
				mu.Unlock()
				if n == 0 {
					// First review: fail with a category to trigger spawn-investigation.
					// CATEGORY and summary must appear BEFORE VERDICT so parseVerdict
					// can find them (it searches lines[:verdictIdx]).
					return &types.ExecuteResult{
						Output: "missing error handling\nCATEGORY: wrong-approach\nVERDICT: FAIL",
					}, nil
				}
				// Second review: pass.
				return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
			}

			if strings.Contains(req.Prompt, "investigator") {
				// Investigation step: return a concrete diagnosis.
				return &types.ExecuteResult{
					Output: "root-cause: the function lacks nil checks; fix: add nil guard at line 10",
				}, nil
			}

			// Worker call.
			mu.Lock()
			workerPrompts = append(workerPrompts, req.Prompt)
			mu.Unlock()
			return &types.ExecuteResult{Output: "task done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 2
	cfg.Execution.AutoCommit = false
	cfg.Review.Escalation = map[string]config.EscalationPolicy{
		"wrong-approach": {After: 1, Action: step.ActionSpawnInvestigation},
	}

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	prompts := workerPrompts
	mu.Unlock()

	if len(prompts) < 2 {
		t.Fatalf("expected at least 2 worker calls (initial + post-investigation retry), got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "root-cause") {
		t.Errorf("second worker prompt %q should contain investigation diagnosis 'root-cause'", prompts[1])
	}
}

// helpers
