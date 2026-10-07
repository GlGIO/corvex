package step

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/event"

	"github.com/giovannialves/corvex/internal/types"
)

// TestVerdictOf_TheStructuredAnswerWins: a reviewer that forgot the VERDICT:
// line was INDETERMINATE, which costs a retry; with a validated answer it is not.
func TestVerdictOf_TheStructuredAnswerWins(t *testing.T) {
	rr := verdictOf([]byte(`{"verdict":"FAIL","category":"incomplete","summary":"criterion 2 missing"}`), "analysis without the verdict line")
	if rr.Verdict != VerdictFail || rr.Category != "incomplete" || rr.Summary != "criterion 2 missing" {
		t.Errorf("verdictOf = %+v", rr)
	}
}

func TestVerdictOf_FallsBackToTheTextLine(t *testing.T) {
	for name, structured := range map[string][]byte{
		"absent":  nil,
		"garbage": []byte(`{"verdict":"MAYBE"}`),
		"broken":  []byte(`{`),
	} {
		if rr := verdictOf(structured, "fine\nVERDICT: PASS"); rr.Verdict != VerdictPass {
			t.Errorf("%s: verdict = %s, want the text line's PASS", name, rr.Verdict)
		}
	}
}

func TestVerdictOf_APassCarriesNoCategory(t *testing.T) {
	if rr := verdictOf([]byte(`{"verdict":"PASS","category":"style","summary":"ok"}`), ""); rr.Category != "" {
		t.Errorf("PASS with category %q: a category only selects a policy on FAIL", rr.Category)
	}
}

// TestReviewer_AsksForTheSchemaAndReadsIt goes through Review itself, so the
// schema reaching the provider and its answer reaching the verdict are one test.
func TestReviewer_AsksForTheSchemaAndReadsIt(t *testing.T) {
	p := &mockProvider{executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		if !json.Valid([]byte(req.JSONSchema)) {
			t.Errorf("the reviewer did not send a valid schema: %q", req.JSONSchema)
		}
		return &types.ExecuteResult{Output: "no line here", Structured: []byte(`{"verdict":"PASS","summary":"checked"}`)}, nil
	}}
	rr, err := NewReviewer(p, "m", t.TempDir(), "").Review(context.Background(), &types.Task{ID: "S01"})
	if err != nil || rr.Verdict != VerdictPass {
		t.Fatalf("Review = %+v, %v; want PASS from the structured answer", rr, err)
	}
}

// TestReviewer_AFailedCallThatWroteItsVerdictHasJudged: asking for a schema
// must not turn a readable verdict into a spent attempt.
func TestReviewer_AFailedCallThatWroteItsVerdictHasJudged(t *testing.T) {
	for name, c := range map[string]struct {
		status  int
		wantErr bool
	}{
		"no api error: the text verdict stands": {0, false},
		"an outage: waited out, not read":       {529, true},
	} {
		p := &mockProvider{executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "bad\nCATEGORY: incomplete\nVERDICT: FAIL", CostUSD: 0.05, APIErrorStatus: c.status}, errors.New("exit status 1")
		}}
		rr, err := NewReviewer(p, "m", t.TempDir(), "").Review(context.Background(), &types.Task{ID: "S01"})
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if !c.wantErr && (rr.Verdict != VerdictFail || rr.Category != "incomplete" || rr.CostUSD != 0.05) {
			t.Errorf("%s: %+v", name, rr)
		}
	}
}

func TestVerdictOf_AStructuredFailWithoutCategoryTakesTheTextOne(t *testing.T) {
	rr := verdictOf([]byte(`{"verdict":"FAIL","summary":"edge missing"}`), "x\nCATEGORY: missing-edge-case\nVERDICT: FAIL")
	if rr.Category != "missing-edge-case" {
		t.Errorf("category = %q: the escalation policy would not fire", rr.Category)
	}
}

// TestRunAB_BothSidesSpendReachTheLedger: an A/B run read $0.00 everywhere.
func TestRunAB_BothSidesSpendReachTheLedger(t *testing.T) {
	p := &mockProvider{executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		if strings.Contains(req.Prompt, "VERDICT") {
			return &types.ExecuteResult{Output: "VERDICT: FAIL", CostUSD: 0.01}, nil
		}
		return &types.ExecuteResult{Output: "did it", CostUSD: 0.10}, nil
	}}
	rec := &recorder{}
	e, r := aiExecutor(t, p, rec)
	dir := filepath.Dir(r.TasksPath)
	gitInitForRecovery(t, dir)
	e.workDir = dir
	_ = e.RunAB(context.Background(), &types.Task{ID: "S01", Title: "x"}, []string{"model-a", "model-b"})
	paid := map[string]float64{}
	for _, ev := range rec.evs {
		if ev.Type == event.AttemptCost {
			paid[ev.Phase+"/"+ev.Model] += ev.CostUSD
		}
	}
	for _, k := range []string{"worker/model-a", "worker/model-b"} {
		if paid[k] != 0.10 {
			t.Errorf("%s spend on the ledger = %v, want 0.10 (all: %v)", k, paid[k], paid)
		}
	}
}
