package step

import (
	"fmt"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// runHumanGate handles a recipe "human-gate" stage. With --approve-gates it is
// auto-approved and passes; otherwise it stops the run with an actionable
// message, leaving the gate PENDING so a re-run with approval proceeds past it.
func (e *Executor) runHumanGate(r *Run, t *types.Task) error {
	e.emit(event.Event{Type: event.HumanGate, TaskID: t.ID, Message: t.Title})
	if e.approveGates {
		charmbraceletlog.Info("human-gate auto-approved (--approve-gates)", "task", t.ID)
		e.markStagePassed(r, t, "human-gate approved: "+t.Title, 0)
		return nil
	}
	return Fatal(fmt.Errorf("human-gate %q (%s) reached — review the work so far, then re-run with --approve-gates to proceed past it", t.ID, t.Title))
}
