package step

import (
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// emitAttemptCost records what one attempt spent, on the paths where the
// attempt does not end in a task_complete. It is a method rather than two
// inline literals so the two producers cannot drift into reporting different
// things about the same event.
//
// A nil result means the provider returned nothing at all — there is no number
// to report, and inventing a zero would be worse than the silence it replaces.
func (e *Executor) emitAttemptCost(_ *aiTask, t *types.Task, attempt int, result *types.ExecuteResult, why string) {
	if result == nil {
		return
	}
	e.emit(event.Event{
		Type:      event.AttemptCost,
		TaskID:    t.ID,
		Phase:     event.PhaseWorker,
		Attempt:   attempt,
		CostUSD:   result.CostUSD,
		TokensIn:  result.TokensIn,
		TokensOut: result.TokensOut,
		Message:   why,
	})
}

// emitReviewCost records a review call whose verdict never reached a
// review_result line — an error, an outage, a discarded verdict — so the ledger
// sees what the ceiling already counted.
func (e *Executor) emitReviewCost(t *types.Task, attempt int, rr *ReviewResult, why string) {
	if rr == nil {
		return
	}
	e.emit(event.Event{
		Type:      event.AttemptCost,
		TaskID:    t.ID,
		Phase:     event.PhaseReview,
		Attempt:   attempt,
		CostUSD:   rr.CostUSD,
		TokensIn:  rr.TokensIn,
		TokensOut: rr.TokensOut,
		Message:   why,
	})
}
