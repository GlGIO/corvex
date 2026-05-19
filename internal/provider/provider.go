package provider

import (
	"context"

	"github.com/giovannialves/corvex/internal/types"
)

type Provider interface {
	Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error)
	Stream(ctx context.Context, req types.ExecuteRequest) (<-chan types.StreamEvent, error)
	Name() string
	Models() []string
}

// ProgressStreamer is implemented by providers that can emit streaming events
// via a callback while still returning the final ExecuteResult. The Worker uses
// it (when available) to forward per-chunk progress to the TUI, so users see
// the AI's tool calls and intermediate text instead of a static "worker S03"
// while a multi-minute task runs.
type ProgressStreamer interface {
	ExecuteWithProgress(ctx context.Context, req types.ExecuteRequest, onEvent func(types.StreamEvent)) (*types.ExecuteResult, error)
}
