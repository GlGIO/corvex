package provider

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/giovannialves/corvex/internal/types"
)

// ProgressBase carries an optional writer that AI-driven components use
// to surface tool-use events live, so the user never stares at a blank
// terminal while the AI explores the codebase.
//
// Components embed this struct; callers wire it with `SetProgressWriter`.
// When the writer is nil — or the underlying provider does not implement
// `ProgressExecutor` — calls fall back to the plain `Execute` path
// and behave exactly as before.
type ProgressBase struct {
	progress io.Writer
}

// SetProgressWriter sets the destination for live tool-call indicators.
// Pass nil to disable streaming.
func (p *ProgressBase) SetProgressWriter(w io.Writer) {
	p.progress = w
}

// RunStep executes a single provider call. When `p.progress` is set and the
// provider implements `ProgressExecutor`, every tool-use event is rendered
// as a one-line indicator (`  · Read foo.go`) to the writer in real time.
// In all other cases the behaviour is identical to `Provider.Execute`.
func (p *ProgressBase) RunStep(ctx context.Context, prov Provider, req types.ExecuteRequest) (*types.ExecuteResult, error) {
	if p.progress != nil {
		if pe, ok := prov.(ProgressExecutor); ok {
			return pe.ExecuteWithProgress(ctx, req, p.toolUseHandler())
		}
	}
	return prov.Execute(ctx, req)
}

// toolUseHandler returns a stream-event callback that prints a compact
// summary of each tool call to `p.progress`. Non tool-use events are
// ignored — text deltas and result lines would just spam the terminal.
func (p *ProgressBase) toolUseHandler() func(types.StreamEvent) {
	out := p.progress
	if out == nil {
		return nil
	}
	return func(ev types.StreamEvent) {
		if ev.Type != types.EventToolUse {
			return
		}
		target := strings.TrimSpace(ev.File)
		if target == "" {
			target = strings.TrimSpace(ev.Content)
		}
		if len(target) > 80 {
			target = target[:79] + "…"
		}
		if target == "" {
			fmt.Fprintf(out, "  · %s\n", ev.Tool)
		} else {
			fmt.Fprintf(out, "  · %s %s\n", ev.Tool, target)
		}
	}
}
