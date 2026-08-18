package step

import (
	"context"
	"fmt"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// runWorker executes one Worker attempt under the watchdog: it assembles the
// prompt inputs, streams the provider's chunks out as events, and cancels the
// attempt when the watchdog decides the provider hung.
//
// A cancelled attempt comes back with the watchdog's diagnosis instead of a
// bare "context canceled", so retries and logs say *why* it died.
func (e *Executor) runWorker(ctx context.Context, r *Run, t *types.Task, st *aiTask) (*types.ExecuteResult, error) {
	contextDocs := loadContextDocs(e.workDir, e.cfg.Context.AlwaysInclude)
	agentPrompt := loadAgentPrompt(e.workDir, e.cfg.AgentRouting, t.Type)
	anchorCtx := e.book.AnchorContext(r.Anchor, t.ID)

	// Stream per-chunk events from the worker so the TUI panel can show what
	// the AI is doing (tool calls, intermediate text) instead of just
	// "worker S03" for several minutes. Each stream event also refreshes the
	// liveness clock the watchdog reads.
	taskID := t.ID
	live := newLiveness()
	st.worker.SetOnStream(func(se types.StreamEvent) {
		live.touch(streamSummary(se))
		ev := se
		e.emit(event.Event{Type: event.TaskStream, TaskID: taskID, Phase: event.PhaseWorker, Stream: &ev})
	})

	// Watchdog: warn after task_warn_minutes, and CANCEL (not just warn) when
	// the attempt blows the wall-clock ceiling or goes idle with no stream
	// output — the signatures of a hung provider. Cancelling the derived
	// context unblocks worker.Execute, which returns a ctx error and feeds the
	// normal retry/fail path. A diagnostic event records where it stalled.
	taskCtx, cancelTask := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	timedOut := newTimeoutFlag()
	// Idle detection is only meaningful on the streaming path; a buffered
	// sandbox produces no per-chunk events, so its idle clock would tick
	// falsely. Buffered runs rely on the wall-clock ceiling.
	streaming := isLocalOrNilSandbox(e.sandbox)
	go e.watchTask(taskCtx, cancelTask, watchDone, taskID, live, timedOut, streaming, t.Timeout)

	result, err := st.worker.Execute(taskCtx, t, anchorCtx, contextDocs, agentPrompt, st.diagnosis)
	close(watchDone)
	cancelTask()
	st.worker.SetOnStream(nil)
	if reason := timedOut.reason(); reason != "" && err != nil {
		// Replace the opaque "context canceled" with the watchdog's diagnosis.
		err = fmt.Errorf("%s", reason)
	}
	return result, err
}
