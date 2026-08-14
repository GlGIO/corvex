package step

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

// mockProvider is a verbatim copy of the orchestrator package's test stub: Go
// test helpers do not cross package boundaries, and copying it is what let the
// Worker and Reviewer move out of the orchestrator in F0 without changing
// behaviour.
type mockProvider struct {
	mu        sync.Mutex
	executeFn func(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error)
	calls     []types.ExecuteRequest
}

func (m *mockProvider) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()
	if m.executeFn != nil {
		return m.executeFn(ctx, req)
	}
	return &types.ExecuteResult{Output: ""}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ types.ExecuteRequest) (<-chan types.StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockProvider) Name() string     { return "mock" }
func (m *mockProvider) Models() []string { return []string{"test-model"} }

type mockSandbox struct {
	mu           sync.Mutex
	prepareErr   error
	runFn        func(ctx context.Context, req sandbox.RunRequest) (*sandbox.RunResult, error)
	cleanupErr   error
	available    bool
	prepareCalls int
	cleanupCalls int
	runCalls     []sandbox.RunRequest
}

func (m *mockSandbox) Prepare(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prepareCalls++
	return m.prepareErr
}

func (m *mockSandbox) Run(ctx context.Context, req sandbox.RunRequest) (*sandbox.RunResult, error) {
	m.mu.Lock()
	m.runCalls = append(m.runCalls, req)
	m.mu.Unlock()
	if m.runFn != nil {
		return m.runFn(ctx, req)
	}
	return &sandbox.RunResult{Stdout: "", ExitCode: 0}, nil
}

func (m *mockSandbox) Cleanup(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupCalls++
	return m.cleanupErr
}

func (m *mockSandbox) IsAvailable(_ context.Context) bool {
	return m.available
}

type mockCommandProvider struct {
	mockProvider
	buildCommandFn    func(req types.ExecuteRequest) (string, []string, map[string]string)
	parseFullOutputFn func(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error)
}

func (m *mockCommandProvider) BuildCommand(req types.ExecuteRequest) (string, []string, map[string]string) {
	if m.buildCommandFn != nil {
		return m.buildCommandFn(req)
	}
	return "test-bin", []string{"-p", req.Prompt}, nil
}

func (m *mockCommandProvider) ParseFullOutput(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
	if m.parseFullOutputFn != nil {
		return m.parseFullOutputFn(stdout, exitCode, elapsed)
	}
	return &types.ExecuteResult{Output: stdout, ExitCode: exitCode, DurationMs: elapsed.Milliseconds()}, nil
}
