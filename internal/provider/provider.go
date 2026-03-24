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
