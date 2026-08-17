package step

import (
	"context"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

func gateExecutor(t *testing.T, p *mockProvider, evs *[]event.Event) *Executor {
	t.Helper()
	cfg := config.Default()
	var book Bookkeeper
	return NewExecutor(Options{
		Config:   cfg,
		Provider: p,
		Reviewer: NewReviewer(p, "test-model", t.TempDir(), ""),
		WorkDir:  t.TempDir(),
		Book:     &book,
		Emit: func(e event.Event) {
			if evs != nil {
				*evs = append(*evs, e)
			}
		},
	})
}

func TestEffectiveGates_HumanGateKindImpliesOne(t *testing.T) {
	tk := &types.Task{ID: "S01", Title: "Approve release", Kind: types.LegacyKindHumanGate}
	gs := effectiveGates(tk)
	if len(gs) != 1 || gs[0].Nature != types.GateHuman || gs[0].EffectiveWhen() != types.GateBefore {
		t.Fatalf("legacy human-gate did not imply a human gate: %+v", gs)
	}
	// Declaring one explicitly must not produce two.
	tk.Gates = []types.Gate{{Nature: types.GateHuman, Prompt: "sure?"}}
	if got := effectiveGates(tk); len(got) != 1 {
		t.Fatalf("explicit human gate got duplicated: %+v", got)
	}
}

// TestEffectiveGates_CodeStepGetsNoImpliedReviewer guards a bill, not a rule: a
// code step's reviewer already runs in runAITask, and synthesising a second
// inferential gate here would silently double the token cost of every AI task in
// every recipe written before F2.
func TestEffectiveGates_CodeStepGetsNoImpliedReviewer(t *testing.T) {
	if got := effectiveGates(&types.Task{ID: "S01", Kind: "code"}); len(got) != 0 {
		t.Fatalf("a plain code step must declare no gates, got %+v", got)
	}
}

func TestComputationalGate(t *testing.T) {
	var evs []event.Event
	e := gateExecutor(t, &mockProvider{}, &evs)
	acc := newEvidenceSet()
	tk := &types.Task{ID: "S01", Gates: []types.Gate{
		{Nature: types.GateComputational, Label: "Schema", Command: "true"},
	}}
	if err := e.runGates(context.Background(), &Run{}, tk, types.GateAfter, acc); err != nil {
		t.Fatalf("passing gate returned %v", err)
	}
	items := acc.all()
	if len(items) != 1 || items[0].Status != types.EvidencePass || items[0].Label != "Schema" {
		t.Fatalf("evidence not produced: %+v", items)
	}

	acc = newEvidenceSet()
	tk.Gates[0].Command = "exit 3"
	err := e.runGates(context.Background(), &Run{}, tk, types.GateAfter, acc)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("failing gate = %v, want a refusal", err)
	}
	if items := acc.all(); len(items) != 1 || items[0].Status != types.EvidenceFail {
		t.Fatalf("failing gate produced %+v, want one fail item", items)
	}
	if IsFatal(err) {
		t.Error("a refused gate must be a task-level failure, so the DAG cascade handles it")
	}
}

func TestInferentialGate(t *testing.T) {
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
		return &types.ExecuteResult{Output: "VERDICT: FAIL\nCATEGORY: wrong-approach\nit reimplements X", CostUSD: 0.02}, nil
	}
	var evs []event.Event
	e := gateExecutor(t, p, &evs)
	acc := newEvidenceSet()
	total := 0.0
	tk := &types.Task{ID: "S01", Gates: []types.Gate{{Nature: types.GateInferential, Label: "Review de migration"}}}

	err := e.runGates(context.Background(), &Run{TotalCostUSD: &total}, tk, types.GateAfter, acc)
	if err == nil || !strings.Contains(err.Error(), "independent review returned FAIL") {
		t.Fatalf("failing review = %v, want a refusal naming the verdict", err)
	}
	items := acc.all()
	if len(items) != 1 || items[0].Kind != types.EvidenceVerdict || !strings.Contains(items[0].Content, "wrong-approach") {
		t.Fatalf("verdict evidence lost the category: %+v", items)
	}
	if total != 0.02 {
		t.Errorf("gate cost %v was not billed to the run, want 0.02", total)
	}
}

// TestInferentialGate_UsesAFreshCall is what "never the author" actually means
// here: the judge gets its own provider call with read-only tools and no worker
// context. It is structural, which is why no test compares model names.
func TestInferentialGate_UsesAFreshCall(t *testing.T) {
	p := &mockProvider{}
	p.executeFn = func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
		return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
	}
	e := gateExecutor(t, p, nil)
	total := 0.0
	tk := &types.Task{ID: "S01", Title: "Add index", Gates: []types.Gate{{Nature: types.GateInferential}}}
	if err := e.runGates(context.Background(), &Run{TotalCostUSD: &total}, tk, types.GateAfter, nil); err != nil {
		t.Fatalf("passing review returned %v", err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("expected exactly one provider call, got %d", len(p.calls))
	}
	req := p.calls[0]
	for _, tool := range req.AllowedTools {
		if tool == "Write" || tool == "Edit" {
			t.Errorf("the independent judge was given a writing tool (%s)", tool)
		}
	}
	if len(req.AllowedTools) == 0 {
		t.Error("the judge got no tools at all — it cannot read the work it is judging")
	}
}

func TestPolicyGate_BranchNot(t *testing.T) {
	e := gateExecutor(t, &mockProvider{}, nil)
	e.branchFn = func(context.Context) (string, error) { return "main", nil }
	tk := &types.Task{ID: "S07", Gates: []types.Gate{
		{Nature: types.GatePolicy, When: types.GateBefore, BranchNot: []string{"main", "develop"}},
	}}
	err := e.runGates(context.Background(), &Run{}, tk, types.GateBefore, nil)
	if err == nil || !strings.Contains(err.Error(), "branch_not") {
		t.Fatalf("merge into main = %v, want a refusal", err)
	}

	e.branchFn = func(context.Context) (string, error) { return "feat/x", nil }
	if err := e.runGates(context.Background(), &Run{}, tk, types.GateBefore, nil); err != nil {
		t.Fatalf("feature branch = %v, want nil", err)
	}
}

// TestPolicyGate_UnreadableBranchRefuses: this rule guards merges into main, so
// "we could not tell which branch we are on" is not satisfied — it is unknown,
// and unknown must not pass.
func TestPolicyGate_UnreadableBranchRefuses(t *testing.T) {
	e := gateExecutor(t, &mockProvider{}, nil)
	e.branchFn = func(context.Context) (string, error) { return "", context.DeadlineExceeded }
	tk := &types.Task{ID: "S07", Gates: []types.Gate{
		{Nature: types.GatePolicy, When: types.GateBefore, BranchNot: []string{"main"}},
	}}
	if err := e.runGates(context.Background(), &Run{}, tk, types.GateBefore, nil); err == nil {
		t.Fatal("an unreadable branch must refuse, not pass")
	}
}

func TestPolicyKnobs(t *testing.T) {
	tk := &types.Task{ID: "S01", Gates: []types.Gate{
		{Nature: types.GatePolicy, MaxAttempts: 2},
		{Nature: types.GatePolicy, MaxCostUSD: 3},
	}}
	k := policyFor(tk)
	if k.maxAttempts != 2 || k.maxCostUSD != 3 {
		t.Fatalf("policyFor = %+v, want attempts 2 cost 3", k)
	}
	// Two ceilings on one step: the tighter one wins, because a step that
	// declares both means it wants both respected.
	tk.Gates = append(tk.Gates, types.Gate{Nature: types.GatePolicy, MaxAttempts: 1, MaxCostUSD: 9})
	if k := policyFor(tk); k.maxAttempts != 1 || k.maxCostUSD != 3 {
		t.Fatalf("policyFor = %+v, want the tighter of each", k)
	}
}

// TestGatesRunInDeclarationOrder: the canonical migration gate of the reference
// flow chains computational → inferential → apply → verify → human, and that
// chain only means something if the runner honours the author's order.
func TestGatesRunInDeclarationOrder(t *testing.T) {
	e := gateExecutor(t, &mockProvider{}, nil)
	acc := newEvidenceSet()
	tk := &types.Task{ID: "S01", Gates: []types.Gate{
		{Nature: types.GateComputational, Label: "first", Command: "true"},
		{Nature: types.GateComputational, Label: "second", Command: "true"},
		{Nature: types.GateComputational, Label: "third", Command: "false"},
		{Nature: types.GateComputational, Label: "never", Command: "true"},
	}}
	if err := e.runGates(context.Background(), &Run{}, tk, types.GateAfter, acc); err == nil {
		t.Fatal("the third gate must refuse")
	}
	items := acc.all()
	if len(items) != 3 {
		t.Fatalf("ran %d gates, want 3 (stop at the first refusal)", len(items))
	}
	for i, want := range []string{"first", "second", "third"} {
		if items[i].Label != want {
			t.Errorf("gate %d = %q, want %q", i, items[i].Label, want)
		}
	}
}

func TestResolveDeclaredEvidence(t *testing.T) {
	e := gateExecutor(t, &mockProvider{}, nil)
	acc := newEvidenceSet()
	tk := &types.Task{ID: "S01", Evidence: []types.Evidence{
		{Kind: types.EvidenceSQL, Label: "Migration", From: "echo 'alter table x'", RequiredReading: true},
		{Kind: types.EvidenceLink, Label: "PR", Content: "https://example.test/pr/1"},
		{Kind: types.EvidenceDiff, Label: "Plano", From: "exit 7"},
	}}
	e.resolveDeclared(context.Background(), tk, acc)
	items := acc.all()
	if len(items) != 3 {
		t.Fatalf("resolved %d items, want 3", len(items))
	}
	if !strings.Contains(items[0].Content, "alter table x") || items[0].From != "" {
		t.Errorf("`from` was not resolved into content: %+v", items[0])
	}
	if !items[0].RequiredReading {
		t.Error("required_reading was lost while resolving")
	}
	if items[1].Content != "https://example.test/pr/1" {
		t.Errorf("inline content was altered: %+v", items[1])
	}
	// A failing `from` is information for the decider, not a reason to fail a
	// step nothing has judged yet.
	if items[2].Status != types.EvidenceFail || !strings.Contains(items[2].Content, "failed") {
		t.Errorf("a failing `from` must show up as failed evidence: %+v", items[2])
	}
}
