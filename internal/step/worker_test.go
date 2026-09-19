package step

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

func TestExecute_NoAllowedTools(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "done"}, nil
		},
	}
	w := NewWorker(mock, "test-model", "/tmp", nil, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}

	if _, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	if len(mock.calls[0].AllowedTools) != 0 {
		t.Errorf("AllowedTools = %v, want empty (no restrictions)", mock.calls[0].AllowedTools)
	}
}

func TestExecute_ModelAndWorkDir(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "done"}, nil
		},
	}
	w := NewWorker(mock, "sonnet", "/my/workdir", nil, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}

	if _, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{}); err != nil {
		t.Fatal(err)
	}

	req := mock.calls[0]
	if req.Model != "sonnet" {
		t.Errorf("Model = %q, want %q", req.Model, "sonnet")
	}
	if req.WorkDir != "/my/workdir" {
		t.Errorf("WorkDir = %q, want %q", req.WorkDir, "/my/workdir")
	}
}

func TestWorkerExecute_ViaSandbox(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{
		runFn: func(_ context.Context, req sandbox.RunRequest) (*sandbox.RunResult, error) {
			return &sandbox.RunResult{
				Stdout:   "raw sandbox stdout",
				ExitCode: 0,
			}, nil
		},
	}

	prov := &mockCommandProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
				t.Error("provider.Execute should not be called when sandbox is used")
				return nil, fmt.Errorf("should not be called")
			},
		},
		parseFullOutputFn: func(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "parsed: " + stdout, ExitCode: exitCode, DurationMs: elapsed.Milliseconds()}, nil
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", sb, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Output != "parsed: raw sandbox stdout" {
		t.Errorf("Output = %q, want %q", result.Output, "parsed: raw sandbox stdout")
	}

	sb.mu.Lock()
	runCount := len(sb.runCalls)
	sb.mu.Unlock()
	if runCount != 1 {
		t.Errorf("sandbox.Run called %d times, want 1", runCount)
	}
}

func TestWorkerExecute_FallbackDirect(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{}
	prov := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "direct output"}, nil
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", sb, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Output != "direct output" {
		t.Errorf("Output = %q, want %q", result.Output, "direct output")
	}

	sb.mu.Lock()
	runCount := len(sb.runCalls)
	sb.mu.Unlock()
	if runCount != 0 {
		t.Errorf("sandbox.Run should not be called for non-CommandBuilder provider, got %d calls", runCount)
	}
}

func TestWorkerExecute_NilSandbox(t *testing.T) {
	t.Parallel()

	called := false
	prov := &mockCommandProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
				called = true
				return &types.ExecuteResult{Output: "direct output"}, nil
			},
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", nil, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !called {
		t.Error("provider.Execute should be called when sandbox is nil")
	}
	if result.Output != "direct output" {
		t.Errorf("Output = %q, want %q", result.Output, "direct output")
	}
}

func TestWorkerExecute_SandboxError(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{
		runFn: func(_ context.Context, _ sandbox.RunRequest) (*sandbox.RunResult, error) {
			return nil, fmt.Errorf("container crashed")
		},
	}

	prov := &mockCommandProvider{}

	w := NewWorker(prov, "sonnet", "/tmp", sb, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	_, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{})
	if err == nil {
		t.Fatal("expected error from sandbox")
	}
	if !strings.Contains(err.Error(), "container crashed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "container crashed")
	}
	if !strings.Contains(err.Error(), "sandbox execution") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "sandbox execution")
	}
}

func TestWorkerExecute_NonZeroExitCode(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{
		runFn: func(_ context.Context, _ sandbox.RunRequest) (*sandbox.RunResult, error) {
			return &sandbox.RunResult{
				Stdout:   "partial output",
				Stderr:   "permission denied",
				ExitCode: 1,
			}, nil
		},
	}

	prov := &mockCommandProvider{
		parseFullOutputFn: func(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: stdout, ExitCode: exitCode, DurationMs: elapsed.Milliseconds()}, nil
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", sb, nil, config.DefaultEnvAllowlist(), config.SecurityConfig{})
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "", GateAnswer{})
	if err == nil {
		t.Fatal("expected error for non-zero exit code")
	}
	if !strings.Contains(err.Error(), "exit code 1") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "exit code 1")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %q, want to contain stderr", err.Error())
	}
	if result == nil {
		t.Fatal("result should not be nil even on non-zero exit")
	}
	if result.Output != "partial output" {
		t.Errorf("Output = %q, want %q", result.Output, "partial output")
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}
