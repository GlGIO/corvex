package planning

import (
	"context"
	"fmt"
	"sync"

	"github.com/giovannialves/corvex/internal/types"
)

// mockProvider is a provider.Provider stub for the planning tests. It is a
// verbatim copy of the orchestrator package's test stub: Go test helpers do not
// cross package boundaries, and the planning components moved out of
// orchestrator in F0 without changing behaviour.
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
