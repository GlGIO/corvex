package provider

import (
	"context"

	"github.com/giovannialves/corvex/internal/types"
)

// ProgressExecutor is an optional provider extension that surfaces stream
// events (tool calls, text deltas, errors) to a callback while still
// returning the final ExecuteResult with cost/token totals. Callers that
// need both visibility *and* accounting — like the Brainstormer printing
// `· Read foo.go` lines as the model explores — type-assert their provider
// against this interface and fall back to Execute when it is not
// implemented.
type ProgressExecutor interface {
	ExecuteWithProgress(ctx context.Context, req types.ExecuteRequest, onEvent func(types.StreamEvent)) (*types.ExecuteResult, error)
}
