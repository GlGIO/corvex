package step

import (
	"context"
	"encoding/json"
	"testing"

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
