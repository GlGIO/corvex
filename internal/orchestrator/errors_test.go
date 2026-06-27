package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func TestIsFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", fmt.Errorf("plain"), false},
		{"fatal wrapped", fatal(fmt.Errorf("boom")), true},
		{"fatal wrapped in %w", fmt.Errorf("outer: %w", fatal(fmt.Errorf("inner"))), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isFatal(tc.err); got != tc.want {
				t.Errorf("isFatal(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRun_CostCeilingBreach_IsFatal(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cost-fatal"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

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

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected cost ceiling error, got nil")
	}
	if !strings.Contains(err.Error(), "run aborted: cumulative cost") {
		t.Errorf("error %q should contain 'run aborted: cumulative cost'", err.Error())
	}
	if !isFatal(err) {
		t.Error("cost ceiling breach must be a fatal error")
	}
}

func TestRun_TaskFailure_IsNotFatal(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-task-fail-nonfatal"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "VERDICT: FAIL"}, nil
			}
			return &types.ExecuteResult{Output: "task done"}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 1
	cfg.Execution.AutoCommit = false

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected task failure error, got nil")
	}
	if isFatal(err) {
		t.Errorf("task-level failure must NOT be fatal, got: %v", err)
	}
}
