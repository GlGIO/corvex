package step

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/hooks"
	"github.com/giovannialves/corvex/internal/types"
)

// recorder collects emitted events so a test can ask what reached the ledger.
// No mutex: every emit happens on the step's own goroutine, and the human-gate
// test only reads the slice after that goroutine has reported through a
// channel. Adding a lock here would hide it if that ever stopped being true.
type recorder struct{ evs []event.Event }

func (r *recorder) emit(e event.Event) { r.evs = append(r.evs, e) }

func (r *recorder) phaseOf(t *testing.T, typ event.Type) string {
	t.Helper()
	for _, e := range r.evs {
		if e.Type == typ {
			return e.Phase
		}
	}
	t.Fatalf("no %s event was emitted; got %s", typ, r.types())
	return ""
}

func (r *recorder) first(t *testing.T, typ event.Type) event.Event {
	t.Helper()
	for _, e := range r.evs {
		if e.Type == typ {
			return e
		}
	}
	t.Fatalf("no %s event was emitted; got %s", typ, r.types())
	return event.Event{}
}

func (r *recorder) has(typ event.Type) bool {
	for _, e := range r.evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func (r *recorder) types() string {
	var b []string
	for _, e := range r.evs {
		b = append(b, string(e.Type))
	}
	return strings.Join(b, ",")
}

// TestPhase_InferentialGateIsReviewButItsRefusalIsGate pins the one attribution
// that is genuinely ambiguous: an inferential gate is a judge (its spend is the
// same nature as a code step's reviewer, and chargeGate bills it to the run),
// but the refusal it produces is the gate acting. Money on `review`, verdict on
// `gate`.
func TestPhase_InferentialGateIsReviewButItsRefusalIsGate(t *testing.T) {
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
		return &types.ExecuteResult{Output: "VERDICT: FAIL\nCATEGORY: wrong-approach\nno", CostUSD: 0.02}, nil
	}
	rec := &recorder{}
	e := gateExecutor(t, p, nil)
	e.emit = rec.emit

	total := 0.0
	tk := &types.Task{ID: "S01", Gates: []types.Gate{{Nature: types.GateInferential, Label: "Review"}}}
	if err := e.runGates(context.Background(), &Run{TotalCostUSD: &total}, tk, types.GateAfter, newEvidenceSet()); err == nil {
		t.Fatal("a FAIL verdict must refuse")
	}

	if got := rec.phaseOf(t, event.ReviewStart); got != event.PhaseReview {
		t.Errorf("review_start phase = %q, want %q", got, event.PhaseReview)
	}
	res := rec.first(t, event.ReviewResult)
	if res.Phase != event.PhaseReview {
		t.Errorf("review_result phase = %q, want %q", res.Phase, event.PhaseReview)
	}
	// The whole point of the column: the cost line must be attributable.
	if res.CostUSD != 0.02 {
		t.Errorf("review_result cost = %v, want the gate's 0.02 on the review line", res.CostUSD)
	}
	if got := rec.phaseOf(t, event.GateFailed); got != event.PhaseGate {
		t.Errorf("gate_failed phase = %q, want %q", got, event.PhaseGate)
	}
}

// TestPhase_ComputationalGateRefusalIsGate: no LLM ran, so nothing is `review`
// here — the refusal is still the gate.
func TestPhase_ComputationalGateRefusalIsGate(t *testing.T) {
	rec := &recorder{}
	e := gateExecutor(t, &mockProvider{}, nil)
	e.emit = rec.emit

	tk := &types.Task{ID: "S01", Gates: []types.Gate{
		{Nature: types.GateComputational, Label: "Schema", Command: "exit 1"},
	}}
	if err := e.runGates(context.Background(), &Run{}, tk, types.GateAfter, newEvidenceSet()); err == nil {
		t.Fatal("a non-zero check must refuse")
	}
	if got := rec.phaseOf(t, event.GateFailed); got != event.PhaseGate {
		t.Errorf("gate_failed phase = %q, want %q", got, event.PhaseGate)
	}
}

// aiExecutor builds an executor that can drive one full worker/review cycle.
func aiExecutor(t *testing.T, p *mockProvider, rec *recorder) (*Executor, *Run) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	// No auto-commit: these tests assert which phase a line carries, and the
	// git checkpoint is neither under test nor available in a bare temp dir.
	cfg.Execution.AutoCommit = false
	var book Bookkeeper
	e := NewExecutor(Options{
		Config:   cfg,
		Provider: p,
		Worker:   NewWorker(p, "test-model", dir, nil, nil, nil, config.SecurityConfig{}),
		Reviewer: NewReviewer(p, "test-model", dir, ""),
		Hooks:    hooks.NewRunner(dir, 0),
		WorkDir:  dir,
		Book:     &book,
		Emit:     rec.emit,
	})
	total := 0.0
	return e, &Run{
		TasksPath:    filepath.Join(dir, "tasks.md"),
		AnchorPath:   filepath.Join(dir, "anchor.yaml"),
		Anchor:       &types.AnchorState{},
		Completed:    map[string]bool{},
		DAG:          dag.NewDAG([]types.Task{{ID: "S01"}}),
		TotalCostUSD: &total,
	}
}

// TestPhase_WorkerAndReviewOnACodeStep: the two natures of an AI step land in
// different buckets on the same task's lines.
func TestPhase_WorkerAndReviewOnACodeStep(t *testing.T) {
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		if strings.Contains(req.Prompt, "VERDICT") {
			return &types.ExecuteResult{Output: "VERDICT: FAIL\nCATEGORY: bug\nbroken", CostUSD: 0.01}, nil
		}
		return &types.ExecuteResult{Output: "did the work", CostUSD: 0.02}, nil
	}
	rec := &recorder{}
	e, r := aiExecutor(t, p, rec)

	// max_attempts 1 keeps this to a single cycle, so the assertions read the
	// first and only attempt.
	tk := &types.Task{ID: "S01", Title: "Do it", Gates: []types.Gate{{Nature: types.GatePolicy, MaxAttempts: 1}}}
	if err := e.runAITask(context.Background(), r, tk, newEvidenceSet()); err == nil {
		t.Fatal("a FAIL verdict on the only attempt must fail the task")
	}

	if got := rec.phaseOf(t, event.TaskStart); got != event.PhaseWorker {
		t.Errorf("task_start phase = %q, want %q", got, event.PhaseWorker)
	}
	if got := rec.phaseOf(t, event.ReviewStart); got != event.PhaseReview {
		t.Errorf("review_start phase = %q, want %q", got, event.PhaseReview)
	}
	if got := rec.phaseOf(t, event.ReviewResult); got != event.PhaseReview {
		t.Errorf("review_result phase = %q, want %q", got, event.PhaseReview)
	}
	// The step's terminal line belongs to the work attempted, not to the judge
	// that rejected it.
	if got := rec.phaseOf(t, event.TaskComplete); got != event.PhaseWorker {
		t.Errorf("task_complete phase = %q, want %q", got, event.PhaseWorker)
	}
}

// TestPhase_CommandStageIsValidate: the deterministic third of the cost bar. A
// command stage spends no money, and `validate` is what makes that a measured
// zero instead of an absent one.
func TestPhase_CommandStageIsValidate(t *testing.T) {
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)

	tk := &types.Task{ID: "S01", Kind: "test", Command: "exit 1"}
	if err := e.runComputationalStage(context.Background(), r, tk, newEvidenceSet()); err == nil {
		t.Fatal("a failing command stage must fail the task")
	}
	if got := rec.phaseOf(t, event.TaskStart); got != event.PhaseValidate {
		t.Errorf("task_start phase = %q, want %q", got, event.PhaseValidate)
	}
	if got := rec.phaseOf(t, event.TaskComplete); got != event.PhaseValidate {
		t.Errorf("task_complete phase = %q, want %q", got, event.PhaseValidate)
	}
	if got := rec.phaseOf(t, event.TaskStream); got != event.PhaseValidate {
		t.Errorf("task_stream phase = %q, want %q", got, event.PhaseValidate)
	}
}

// TestPhase_MarkStagePassedCarriesItsCallersNature: the same function serves a
// deterministic command and a gate that was the whole step; the bar needs them
// apart.
func TestPhase_MarkStagePassedCarriesItsCallersNature(t *testing.T) {
	for _, phase := range []string{event.PhaseValidate, event.PhaseGate} {
		rec := &recorder{}
		e, r := aiExecutor(t, &mockProvider{}, rec)
		e.markStagePassed(r, &types.Task{ID: "S01", Title: "x"}, "done", 0, phase)
		if got := rec.phaseOf(t, event.TaskComplete); got != phase {
			t.Errorf("task_complete phase = %q, want %q", got, phase)
		}
		if got := rec.phaseOf(t, event.Checkpoint); got != phase {
			t.Errorf("checkpoint phase = %q, want %q", got, phase)
		}
	}
}

// TestHumanGate_DurationIsTheHumanWait is the number the whole of F5 exists to
// make measurable: "approved in 4s, 100% of the time" is theatre, and nothing on
// disk can tell theatre from judgement without it.
//
// The clock is injected and the decision is written by the test with its own
// timestamp, so the asserted 6 minutes is the wait the gate actually held — not
// wall time, and not the poll interval.
func TestHumanGate_DurationIsTheHumanWait(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)
	e.nowFn = func() time.Time { return base }
	e.gatePoll = time.Millisecond
	r.Identity = RunIdentity{RunID: "run_c0ffee", Repo: t.TempDir(), Project: "p"}

	tk := &types.Task{ID: "S01", Title: "Migrate"}
	g := types.Gate{Nature: types.GateHuman, Prompt: "apply it?"}

	done := make(chan error, 1)
	go func() { done <- e.humanGate(context.Background(), r, tk, g, newEvidenceSet()) }()

	// Answer only once the gate file exists — that is the whole cross-process
	// contract, and approving before it is written would test nothing.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := gate.Read(r.Identity.Repo, "run_c0ffee", "S01"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the gate file was never opened")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := gate.Decide(r.Identity.Repo, "run_c0ffee", "S01", gate.Decision{
		Verdict:   gate.Approved,
		DecidedAt: base.Add(6 * time.Minute),
	}); err != nil {
		t.Fatalf("recording the decision: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("an approved gate must let the step through, got %v", err)
	}

	decided := rec.first(t, event.GateDecided)
	if decided.Phase != event.PhaseGate {
		t.Errorf("gate_decided phase = %q, want %q", decided.Phase, event.PhaseGate)
	}
	if decided.DurationMs != int64(6*time.Minute/time.Millisecond) {
		t.Errorf("human wait = %dms, want 360000ms (opened→decided)", decided.DurationMs)
	}
	if !rec.has(event.GatePending) {
		t.Error("a gate somebody had to answer must leave a gate_pending line")
	}
}

// TestHumanGate_AutoApprovalIsNotAFastHuman: --approve-gates produces a zero
// wait, and a zero is also what a human who answers instantly produces — and
// duration_ms is omitempty, so on disk that zero is not even there. The
// distinction has to survive some other way, or the theatre metric quietly
// counts CI runs as very decisive people.
func TestHumanGate_AutoApprovalIsNotAFastHuman(t *testing.T) {
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)
	e.approveGates = true

	tk := &types.Task{ID: "S01", Title: "Migrate"}
	g := types.Gate{Nature: types.GateHuman, Prompt: "apply it?"}
	if err := e.humanGate(context.Background(), r, tk, g, newEvidenceSet()); err != nil {
		t.Fatalf("--approve-gates must let the step through, got %v", err)
	}

	decided := rec.first(t, event.GateDecided)
	if decided.Phase != event.PhaseGate {
		t.Errorf("gate_decided phase = %q, want %q", decided.Phase, event.PhaseGate)
	}
	if decided.DurationMs != 0 {
		t.Errorf("auto-approval waited %dms, want 0 — nobody was asked", decided.DurationMs)
	}
	if !strings.Contains(decided.Message, "no human waited") {
		t.Errorf("gate_decided message = %q, want it to say no human was involved", decided.Message)
	}
	// Structural half of the same distinction: no gate file was written, so
	// there is nothing anybody could have answered.
	if rec.has(event.GatePending) {
		t.Error("auto-approval emitted gate_pending; nothing was ever pending")
	}
	// The ledger is committed: the marker must not smuggle a path in.
	if strings.Contains(decided.Message, "/") {
		t.Errorf("gate_decided message looks like a path: %q", decided.Message)
	}
}

// TestHumanWaitMs_FallsBackWhenTheDecidersClockIsUnusable. The decision is
// stamped by another process, so its clock is not ours to trust blindly; a
// negative wait is a broken clock, never information.
func TestHumanWaitMs_FallsBackWhenTheDecidersClockIsUnusable(t *testing.T) {
	opened := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	observed := opened.Add(90 * time.Second)

	if got := humanWaitMs(opened, gate.Decision{DecidedAt: opened.Add(time.Minute)}, observed); got != 60000 {
		t.Errorf("wait = %d, want 60000 (the decider's own timestamp, not the poll)", got)
	}
	if got := humanWaitMs(opened, gate.Decision{}, observed); got != 90000 {
		t.Errorf("unstamped decision = %d, want the observed 90000", got)
	}
	if got := humanWaitMs(opened, gate.Decision{DecidedAt: opened.Add(-time.Hour)}, observed); got != 90000 {
		t.Errorf("skewed decision = %d, want the observed 90000", got)
	}
	if got := humanWaitMs(observed, gate.Decision{}, opened); got != 0 {
		t.Errorf("negative wait = %d, want 0", got)
	}
}
