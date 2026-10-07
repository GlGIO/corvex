package step

import (
	"context"
	"fmt"
	"strings"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// maxInfraRetries bounds how many times one task waits out a provider that is
// unavailable. These do not spend the task's review attempts — a 529 says
// nothing about the work, and retrying it on the same budget as "the reviewer
// rejected this" is how a busy afternoon at the provider turned into failed
// tasks. They are bounded all the same: an outage that outlasts this is a
// failure, not a queue.
const maxInfraRetries = 3

// transientMarkers are the provider failures that are about the provider, not
// the request: rate limits, overload, a dropped connection. The list is
// deliberately short and literal. Anything else keeps the old behaviour — it
// spends an attempt with the error as the diagnosis — so a wrong guess here can
// only cost a wait, never repeat a fatal error on the house.
//
// Status codes are matched only in the shape the CLI prints them ("API Error:
// 529"), never as a bare number: a bare "529" also matches "152900 tokens" in a
// prompt-too-long error, which is as deterministic as failures get.
var transientMarkers = []string{
	"api error: 429", "api error: 529", "api error: 503",
	"rate_limit_error", "overloaded_error", "rate limit exceeded",
	"connection reset", "econnreset", "etimedout", "socket hang up",
}

// isTransient reports whether a provider call failed for a reason that says
// nothing about the work. Cancellation is never transient: the person stopped
// the run, and waiting would be disobeying them.
//
// status is the provider's own report of the API error (ExecuteResult.
// APIErrorStatus), and it decides first: it is a number the provider put in a
// field, where the text is a sentence that may contain any number at all.
func isTransient(ctx context.Context, err error, status int) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	switch status {
	case 429, 500, 502, 503, 504, 529:
		return true
	case 0:
	default:
		// A status the provider reported and that is not about availability
		// (400, 401, 413…) is the request's fault: never waited out, whatever
		// the text says.
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range transientMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// infraBackoff is the wait before the n-th (1-based) infra retry: 10s doubling,
// capped at 2 minutes.
func infraBackoff(n int) time.Duration {
	d := 10 * time.Second
	for i := 1; i < n; i++ {
		d *= 2
	}
	if d > 2*time.Minute {
		d = 2 * time.Minute
	}
	return d
}

// waitOutProvider decides whether a failed provider call is waited out instead
// of spending an attempt. It reports true when the caller must redo the SAME
// attempt; false hands the failure to the ordinary retry path.
func (e *Executor) waitOutProvider(ctx context.Context, t *types.Task, st *aiTask, phase string, err error, status int) bool {
	if !isTransient(ctx, err, status) || st.infraRetries >= maxInfraRetries {
		return false
	}
	st.infraRetries++
	d := infraBackoff(st.infraRetries)
	// The published line names the class, never the provider's text: that
	// carries raw stderr, and raw stderr carries paths.
	e.emit(event.Event{Type: event.Retry, TaskID: t.ID, Phase: phase,
		Message: fmt.Sprintf("provider unavailable; waiting %s (infra retry %d/%d, no attempt spent)", d, st.infraRetries, maxInfraRetries)})
	charmbraceletlog.Warn("provider unavailable, waiting", "task", t.ID, "wait", d, "err", err)
	wait := e.waitFn
	if wait == nil {
		wait = sleepCtx
	}
	return wait(ctx, d) == nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

func apiStatus(r *types.ExecuteResult) int {
	if r == nil {
		return 0
	}
	return r.APIErrorStatus
}

func reviewStatus(r *ReviewResult) int {
	if r == nil {
		return 0
	}
	return r.apiStatus
}
