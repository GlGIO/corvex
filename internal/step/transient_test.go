package step

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/recovery"
	"github.com/giovannialves/corvex/internal/types"
)

const passingWorkerOutput = "did the work\n\nTASK-REPORT:\nSUMMARY: did it\nDECISIONS:\n- none\nHANDOFF: none"

// flakyWorker fails the worker call `fails` times with err, then works; the
// reviewer always passes.
func flakyWorker(fails int, err error) *mockProvider {
	var mu sync.Mutex
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		if strings.Contains(req.Prompt, "VERDICT") {
			return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			return &types.ExecuteResult{}, err
		}
		return &types.ExecuteResult{Output: passingWorkerOutput}, nil
	}
	return p
}

// oneShotTask caps the step at ONE attempt (a policy gate's max_attempts; the
// config's max_retries treats 0 as "default"), so any failure that spends an
// attempt fails the task — which is what makes "no attempt spent" observable.
func oneShotTask() *types.Task {
	return &types.Task{ID: "S01", Title: "Do it", Gates: []types.Gate{{Nature: types.GatePolicy, MaxAttempts: 1}}}
}

// noRetryExecutor is an AI-task executor whose waits are recorded, not slept.
func noRetryExecutor(t *testing.T, p *mockProvider) (*Executor, *Run, *recorder, *[]time.Duration) {
	t.Helper()
	rec := &recorder{}
	e, r := aiExecutor(t, p, rec)
	dir := filepath.Dir(r.TasksPath)
	gitInitForRecovery(t, dir)
	e.recovery = recovery.NewManager(dir)
	waits := &[]time.Duration{}
	e.waitFn = func(_ context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return nil
	}
	return e, r, rec, waits
}

// TestInfraRetry_AnOverloadedProviderIsWaitedOutWithoutSpendingAnAttempt: two
// 529s and then a clean pass, on a task that has no retries at all. Before, the
// first 529 spent the only attempt and the task failed.
func TestInfraRetry_AnOverloadedProviderIsWaitedOutWithoutSpendingAnAttempt(t *testing.T) {
	e, r, rec, waits := noRetryExecutor(t, flakyWorker(2, errors.New(`claude cli exited with error: exit status 1 (stderr: API Error: 529 {"type":"overloaded_error"})`)))
	if err := e.runAITask(context.Background(), r, oneShotTask(), newEvidenceSet()); err != nil {
		t.Fatalf("two overloads then a pass must pass a task with no retries: %v", err)
	}
	if len(*waits) != 2 || (*waits)[0] != 10*time.Second || (*waits)[1] != 20*time.Second {
		t.Errorf("waits = %v, want [10s 20s]", *waits)
	}
	for _, ev := range rec.evs {
		if ev.Type == event.Retry && strings.Contains(ev.Message, "529") {
			t.Errorf("the published retry line carries the provider's raw text: %q", ev.Message)
		}
	}
}

// TestInfraRetry_IsBounded: an outage longer than the budget is a failure, not
// a queue.
func TestInfraRetry_IsBounded(t *testing.T) {
	e, r, _, waits := noRetryExecutor(t, flakyWorker(100, errors.New("API Error: 429 rate limit exceeded")))
	if err := e.runAITask(context.Background(), r, oneShotTask(), newEvidenceSet()); err == nil {
		t.Fatal("a provider that never comes back must fail the task")
	}
	if len(*waits) != maxInfraRetries {
		t.Errorf("waited %d times, want %d", len(*waits), maxInfraRetries)
	}
}

// TestInfraRetry_AnythingElseStillSpendsTheAttempt: an unknown failure keeps the
// old path. A wrong guess in the classifier may only ever cost a wait.
func TestInfraRetry_AnythingElseStillSpendsTheAttempt(t *testing.T) {
	e, r, _, waits := noRetryExecutor(t, flakyWorker(1, errors.New("claude cli: model is required")))
	if err := e.runAITask(context.Background(), r, oneShotTask(), newEvidenceSet()); err == nil {
		t.Fatal("a non-transient failure with no retries left must fail the task")
	}
	if len(*waits) != 0 {
		t.Errorf("waited %v for a failure that is not about the provider", *waits)
	}
}

// TestIsTransient_CancellationIsNeverWaitedOut: the person stopped the run.
func TestIsTransient_CancellationIsNeverWaitedOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if isTransient(ctx, errors.New("529 overloaded")) {
		t.Error("a cancelled run must not wait out an overload")
	}
}

// TestRepairAfterGate_OnlyAChecksRefusalIsADiagnosis: a person's "no" and a
// judge's "no" are not sent back to the worker; and the repair happens once.
func TestRepairAfterGate_OnlyAChecksRefusalIsADiagnosis(t *testing.T) {
	e := &Executor{}
	tk := &types.Task{ID: "S01"}
	for _, n := range []types.GateNature{types.GateHuman, types.GateInferential, types.GatePolicy} {
		st := &aiTask{maxRetries: 2}
		if e.repairAfterGate(tk, st, 0, &gateRefusal{nature: n, msg: "no"}) {
			t.Errorf("a %s refusal was sent back to the worker", n)
		}
	}
	st := &aiTask{maxRetries: 2}
	if !e.repairAfterGate(tk, st, 0, &gateRefusal{nature: types.GateComputational, msg: "lint"}) {
		t.Fatal("a check's refusal with attempts left must send the worker back")
	}
	if e.repairAfterGate(tk, st, 1, &gateRefusal{nature: types.GateComputational, msg: "lint"}) {
		t.Error("a second repair in the same task: this must not become a second loop")
	}
	if e.repairAfterGate(tk, &aiTask{maxRetries: 1}, 1, &gateRefusal{nature: types.GateComputational, msg: "lint"}) {
		t.Error("a repair with no attempt left would be an attempt nobody budgeted")
	}
}

// TestEvidenceRewind_TheDiscardedAttemptsRefusalIsGone: the repaired work is
// what a later human gate shows; the refusal of the attempt it replaced is not.
func TestEvidenceRewind_TheDiscardedAttemptsRefusalIsGone(t *testing.T) {
	acc := newEvidenceSet()
	acc.add(types.Evidence{Label: "kept"})
	m := acc.mark()
	acc.add(types.Evidence{Label: "refused attempt"})
	acc.rewind(m)
	if len(acc.items) != 1 || acc.items[0].Label != "kept" {
		t.Errorf("evidence after rewind = %+v, want only what came before the mark", acc.items)
	}
}
