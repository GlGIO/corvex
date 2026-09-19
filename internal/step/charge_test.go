package step

// The spend that trips a ceiling is RECORDED before the run aborts.
//
// MEASURED on a fan-out of six expensive items against a $25 ceiling: the run
// aborted saying `cumulative cost $27.00`, and the run screen then reported
// $22.50. The worker call and the review of the aborted item had happened, had
// been paid for, and neither had reached the ledger — because the lines that
// carry cost are written on the path where a task COMPLETES, and this task never
// did. The screen that answers "where did the money go" was quietest about the
// most expensive moment, and it erred LOW: the direction that gets a ceiling
// raised by somebody who thinks they have room left.

import (
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

func chargeExecutor(t *testing.T, cfg *config.Config, evs *[]event.Event) *Executor {
	t.Helper()
	var book Bookkeeper
	return NewExecutor(Options{
		Config:  cfg,
		WorkDir: t.TempDir(),
		Book:    &book,
		Emit: func(e event.Event) {
			if evs != nil {
				*evs = append(*evs, e)
			}
		},
	})
}

func TestCharge_RecordsTheSpendThatTrippedTheCeiling(t *testing.T) {
	cfg := config.Default()
	cfg.Execution.MaxCostUSD = 10
	cfg.Execution.MaxCostPerTaskUSD = 0 // the run ceiling is what this test is about

	var evs []event.Event
	e := chargeExecutor(t, cfg, &evs)
	task := &types.Task{ID: "S02/005/implementar"}
	st := &aiTask{}
	total := 9.0
	run := &Run{TotalCostUSD: &total}

	if err := e.charge(run, task, st, 4.50, event.PhaseWorker); err == nil {
		t.Fatal("13.50 against a 10.00 ceiling did not abort")
	}

	var recorded float64
	var phase string
	for _, ev := range evs {
		if ev.Type == event.AttemptCost && ev.TaskID == task.ID {
			recorded += ev.CostUSD
			phase = ev.Phase
		}
	}
	if recorded != 4.50 {
		t.Errorf("the ledger would carry $%.2f of the spend that aborted the run, want $4.50", recorded)
	}
	if phase != event.PhaseWorker {
		t.Errorf("the recorded spend has phase %q: the money has to land in the bucket that spent it", phase)
	}
}

// A charge that stays under the ceiling records nothing here: the success path
// writes task_complete, and a second line would double the number.
func TestCharge_UnderTheCeilingRecordsNothingExtra(t *testing.T) {
	cfg := config.Default()
	cfg.Execution.MaxCostUSD = 100
	cfg.Execution.MaxCostPerTaskUSD = 0

	var evs []event.Event
	e := chargeExecutor(t, cfg, &evs)
	total := 1.0
	if err := e.charge(&Run{TotalCostUSD: &total}, &types.Task{ID: "S01"}, &aiTask{}, 2.0, event.PhaseWorker); err != nil {
		t.Fatalf("charge under the ceiling returned %v", err)
	}
	for _, ev := range evs {
		if ev.Type == event.AttemptCost {
			t.Errorf("an attempt_cost line was written on the success path: the cost would be counted twice (%+v)", ev)
		}
	}
}
