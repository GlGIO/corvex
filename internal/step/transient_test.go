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
	if isTransient(ctx, errors.New("529 overloaded"), 529) {
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

// TestIsTransient_ANumberInsideATokenCountIsNotAStatus: the review's finding,
// pinned. "152900 tokens" contains 529 and is the most deterministic failure
// there is; a task called S0429 is not a rate limit either.
func TestIsTransient_ANumberInsideATokenCountIsNotAStatus(t *testing.T) {
	for _, msg := range []string{
		"reviewer execution for task S01: prompt is too long: 152900 tokens > 150000 maximum",
		"worker execution for task S0429: claude cli: model is required",
	} {
		if isTransient(context.Background(), errors.New(msg), 0) {
			t.Errorf("%q was classified as a provider outage", msg)
		}
	}
}

// TestInfraRetry_AReviewOutageRetriesTheReviewNotTheWorker: the worker's tree
// is intact and paid for; an overloaded reviewer is no reason to redo it.
func TestInfraRetry_AReviewOutageRetriesTheReviewNotTheWorker(t *testing.T) {
	var mu sync.Mutex
	workerCalls, reviewFails := 0, 1
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(req.Prompt, "VERDICT") {
			if reviewFails > 0 {
				reviewFails--
				return &types.ExecuteResult{CostUSD: 0.01}, errors.New("API Error: 529 overloaded_error")
			}
			return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
		}
		workerCalls++
		return &types.ExecuteResult{Output: passingWorkerOutput}, nil
	}
	e, r, rec, _ := noRetryExecutor(t, p)
	if err := e.runAITask(context.Background(), r, oneShotTask(), newEvidenceSet()); err != nil {
		t.Fatalf("one review outage must be waited out: %v", err)
	}
	if workerCalls != 1 {
		t.Errorf("worker ran %d times; a review outage must not redo the work", workerCalls)
	}
	var sawReviewCost bool
	for _, ev := range rec.evs {
		if ev.Type == event.AttemptCost && ev.Phase == event.PhaseReview && ev.CostUSD == 0.01 {
			sawReviewCost = true
		}
	}
	if !sawReviewCost {
		t.Error("the failed review call was paid and never reached the ledger")
	}
}

// TestRepairAfterGate_NotWhenAPersonGuardsTheResult: a repair re-runs every
// after-gate, and a human gate's file cannot be opened twice.
func TestRepairAfterGate_NotWhenAPersonGuardsTheResult(t *testing.T) {
	e := &Executor{}
	tk := &types.Task{ID: "S01", Gates: []types.Gate{
		{Nature: types.GateHuman, When: types.GateAfter},
		{Nature: types.GateComputational, Command: "lint"},
	}}
	if e.repairAfterGate(tk, &aiTask{maxRetries: 2}, 0, &gateRefusal{nature: types.GateComputational, msg: "lint"}) {
		t.Error("a repair with a human after-gate would reopen a decided gate")
	}
}

// TestTail_KeepsTheEnd: where a runner prints what failed.
func TestTail_KeepsTheEnd(t *testing.T) {
	got := tail(strings.Repeat("x", 100)+"FAIL: TestFoo", 20)
	if !strings.HasSuffix(got, "FAIL: TestFoo") || len(got) > 20+len("…") {
		t.Errorf("tail = %q", got)
	}
}

// TestReviewer_JudgesTheIsolatedItemsTree: the built-in reviewer followed the
// run's checkout even when the worker wrote an item's worktree.
func TestReviewer_JudgesTheIsolatedItemsTree(t *testing.T) {
	r := NewReviewer(&mockProvider{}, "m", "/run", "")
	if got := r.inDir("/item").workDir; got != "/item" {
		t.Errorf("inDir workDir = %q, want /item", got)
	}
	if r.workDir != "/run" {
		t.Error("inDir mutated the shared reviewer")
	}
}

// TestIsTransient_TheProvidersStatusDecidesFirst: the field beats the text in
// both directions — a 529 the provider reported is waited out whatever the
// message says, and a 413 is never, even when its text mentions an overload.
func TestIsTransient_TheProvidersStatusDecidesFirst(t *testing.T) {
	ctx := context.Background()
	if !isTransient(ctx, errors.New("claude cli exited with error: exit status 1"), 529) {
		t.Error("status 529 with an uninformative message was not waited out")
	}
	if isTransient(ctx, errors.New("API Error: 529 overloaded_error"), 413) {
		t.Error("a 413 was waited out because its text said 529")
	}
}

// TestInfraRetry_TheWorkersReportedStatusReachesTheClassifier: through the
// attempt loop, with a message that says nothing — only the field knows.
func TestInfraRetry_TheWorkersReportedStatusReachesTheClassifier(t *testing.T) {
	var mu sync.Mutex
	fails := 1
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		if strings.Contains(req.Prompt, "VERDICT") {
			return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			return &types.ExecuteResult{APIErrorStatus: 529}, errors.New("claude cli exited with error: exit status 1")
		}
		return &types.ExecuteResult{Output: passingWorkerOutput}, nil
	}
	e, r, _, waits := noRetryExecutor(t, p)
	if err := e.runAITask(context.Background(), r, oneShotTask(), newEvidenceSet()); err != nil {
		t.Fatalf("a reported 529 must be waited out: %v", err)
	}
	if len(*waits) != 1 {
		t.Errorf("waits = %v, want one", *waits)
	}
}

// TestLedger_PaidLinesNameTheirModel: the worker's pass line, the reviewer's
// result line and an outage's cost line all say which model was paid for.
func TestLedger_PaidLinesNameTheirModel(t *testing.T) {
	var mu sync.Mutex
	reviewFails := 1
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(req.Prompt, "VERDICT") {
			if reviewFails > 0 {
				reviewFails--
				return &types.ExecuteResult{CostUSD: 0.01, APIErrorStatus: 529}, errors.New("exit 1")
			}
			return &types.ExecuteResult{Output: "VERDICT: PASS", CostUSD: 0.02}, nil
		}
		return &types.ExecuteResult{Output: passingWorkerOutput, CostUSD: 0.03}, nil
	}
	e, r, rec, _ := noRetryExecutor(t, p)
	e.worker.model, e.reviewer.model = "worker-model", "judge-model"
	if err := e.runAITask(context.Background(), r, oneShotTask(), newEvidenceSet()); err != nil {
		t.Fatal(err)
	}
	want := map[event.Type]string{event.TaskComplete: "worker-model", event.ReviewResult: "judge-model", event.AttemptCost: "judge-model"}
	for _, ev := range rec.evs {
		if m, ok := want[ev.Type]; ok && ev.CostUSD > 0 {
			if ev.Model != m {
				t.Errorf("%s line (cost %v) names model %q, want %q", ev.Type, ev.CostUSD, ev.Model, m)
			}
			delete(want, ev.Type)
		}
	}
	if len(want) > 0 {
		t.Errorf("no paid line was seen for %v", want)
	}
}
