package step

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	e.resolveDeclared(context.Background(), &Run{}, tk, acc)
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

// Evidence that came back empty SAYS so.
//
// Measured on the first real run of a fan-out recipe: the gate that decides
// whether a feature may be shipped had one piece of required reading — a
// `git diff --stat` with the runner's own files excluded — and the step had
// written no product code. The box a person was forced to acknowledge was
// blank, and a blank box cannot distinguish "there was nothing to read" from
// "the command that was supposed to show you something printed nothing". The
// second is the interesting case, and it was the invisible one.
func TestResolveDeclaredEvidence_EmptyOutputIsSaidOutLoud(t *testing.T) {
	e := gateExecutor(t, &mockProvider{}, nil)
	acc := newEvidenceSet()
	tk := &types.Task{ID: "S05", Evidence: []types.Evidence{
		{Kind: types.EvidenceDiff, Label: "O diff da feature", From: "true", RequiredReading: true},
		{Kind: types.EvidenceTestOutput, Label: "Só espaços", From: "printf '   \n'"},
	}}
	e.resolveDeclared(context.Background(), &Run{}, tk, acc)
	items := acc.all()
	if len(items) != 2 {
		t.Fatalf("resolved %d items, want 2", len(items))
	}
	for _, item := range items {
		if strings.TrimSpace(item.Content) == "" {
			t.Errorf("%q is still blank: a reader cannot tell it apart from evidence nobody produced", item.Label)
		}
		if !strings.Contains(item.Content, "printed nothing") {
			t.Errorf("%q does not say the command printed nothing: %q", item.Label, item.Content)
		}
		// `warn`, not `fail`: an empty diff is frequently the truth. It is
		// unreadable, not wrong.
		if item.Status != types.EvidenceWarn {
			t.Errorf("%q has status %q, want warn", item.Label, item.Status)
		}
	}
	// And the lock still holds: emptiness does not quietly drop the requirement
	// to acknowledge, because "nothing changed" is exactly the claim somebody
	// should have to look at before shipping.
	if !items[0].RequiredReading {
		t.Error("required_reading was dropped when the evidence came back empty")
	}
}

// The REASON a gate refused survives on the ledger.
//
// Measured: a person rejected a human gate with "a migration derruba a coluna
// sem backfill", and the step detail — the canonical surface, the one the UI
// draws — said `refused by gate` and nothing more. The sentence lived in the
// terminal that happened to be running the run, which closes, and in the gate
// file, which no screen joins. The person who refuses and the person who reads
// the run later are routinely not the same person; when they are, they are not
// the same hour.
func TestGateRefused_TheReasonIsOnTheLine(t *testing.T) {
	var evs []event.Event
	e := gateExecutor(t, &mockProvider{}, &evs)
	const reason = "a migration derruba a coluna sem backfill"

	err := e.gateRefused(&types.Task{ID: "S02"}, types.Gate{Nature: types.GateHuman, Label: "Aprovar o irreversível"}, reason)
	if err == nil {
		t.Fatal("a refusal has to produce an error")
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("the error dropped the reason: %v", err)
	}

	var found bool
	for _, ev := range evs {
		if ev.Type == event.GateFailed {
			found = true
			if !strings.Contains(ev.Message, reason) {
				t.Errorf("the gate_failed line says %q — a reader of the run never sees why", ev.Message)
			}
			if !strings.Contains(ev.Message, "Aprovar o irreversível") {
				t.Errorf("the gate_failed line stopped naming the gate: %q", ev.Message)
			}
		}
	}
	if !found {
		t.Error("no gate_failed line was emitted at all")
	}
}

// And the step's terminal line — the last thing a reader sees — carries it too.
func TestMarkGateFailure_TheTerminalLineSaysWhy(t *testing.T) {
	var evs []event.Event
	e := gateExecutor(t, &mockProvider{}, &evs)
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(tasksPath, []byte("---\ndag:\n  S02: []\n---\n\n## S02 — x ⬜ PENDING\n\n### O que fazer\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e.markGateFailure(&Run{TasksPath: tasksPath}, &types.Task{ID: "S02"}, errors.New("gate human: Aprovar refused: sem backfill"))

	for _, ev := range evs {
		if ev.Type == event.TaskComplete {
			if !strings.Contains(ev.Message, "sem backfill") {
				t.Errorf("task_complete says %q, which sends the reader looking for a sentence that lived in a closed terminal", ev.Message)
			}
			return
		}
	}
	t.Error("no task_complete line was emitted")
}

// The reply to a question gate reaches the step's command.
//
// The gate type's own contract says approving a question means "continue, and
// here is the answer". Measured before this worked: a recipe asked "which branch
// should receive this PR?", a person answered `release/1.8.0`, the run resumed —
// and the step ran exactly the command it would have run without asking. The
// answer was evidence a reader could see and the work could not use.
func TestRunShellForStep_CarriesTheAnswerToTheCommand(t *testing.T) {
	e := gateExecutor(t, &mockProvider{}, nil)
	acc := newEvidenceSet()
	acc.setAnswer("release/1.8.0")

	out, err := e.runShellForStep(context.Background(), &Run{}, &types.Task{ID: "S01"}, acc,
		`printf '%s' "${CORVEX_GATE_ANSWER:-<vazio>}"`)
	if err != nil {
		t.Fatalf("running the step: %v (%s)", err, out)
	}
	if out != "release/1.8.0" {
		t.Errorf("the command saw %q, want the answer a person typed", out)
	}

	// A step that asked nothing gets the variable empty, not absent: a recipe
	// that reads it unconditionally must not die under `set -u`.
	out, err = e.runShellForStep(context.Background(), &Run{}, &types.Task{ID: "S02"}, newEvidenceSet(),
		`set -u; printf '[%s]' "$CORVEX_GATE_ANSWER"`)
	if err != nil {
		t.Fatalf("a step with no question died reading the variable: %v (%s)", err, out)
	}
	if out != "[]" {
		t.Errorf("a step that asked nothing saw %q, want an empty answer", out)
	}
}
