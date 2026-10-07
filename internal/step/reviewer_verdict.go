package step

import (
	"encoding/json"
	"strings"
)

// reviewerSchema is the verdict the reviewer is asked to return as structured
// output, next to the VERDICT: line its prompt still asks for.
//
// The category is an ENUM of the six the prompt lists, not a free string.
// Measured on a real call with a free string: the model answered `correctness`,
// which no escalation policy names — and the category is what selects the
// policy, so an invented one makes the escalation not fire, in silence.
const reviewerSchema = `{"type":"object","properties":{` +
	`"verdict":{"type":"string","enum":["PASS","FAIL"]},` +
	`"category":{"type":"string","enum":["wrong-approach","missing-edge-case","incomplete","flaky-test","style","ambiguity"],` +
	`"description":"Only on FAIL: which kind of failure this is."},` +
	`"summary":{"type":"string","description":"Your analysis: what you checked, what you found, and why the verdict. Quote spec lines you rely on."}},` +
	`"required":["verdict","summary"]}`

type structuredVerdict struct {
	Verdict  string `json:"verdict"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

// verdictOf reads the reviewer's answer: the structured one when the provider
// produced a valid one, the VERDICT: line otherwise.
//
// The structured answer wins because it is the one a schema validated; the text
// line is what a model writes when it remembers to, which is why
// INDETERMINATE exists. The fallback stays for providers that cannot do
// structured output and for the calls where it comes back empty.
func verdictOf(structured []byte, text string) *ReviewResult {
	var sv structuredVerdict
	if len(structured) > 0 && json.Unmarshal(structured, &sv) == nil {
		v := ReviewVerdict(strings.ToUpper(strings.TrimSpace(sv.Verdict)))
		if v == VerdictPass || v == VerdictFail {
			rr := &ReviewResult{Verdict: v, Summary: strings.TrimSpace(sv.Summary)}
			if v == VerdictFail {
				rr.Category = strings.TrimSpace(sv.Category)
				// The schema cannot require a category only on FAIL, so a FAIL
				// may come without one; the text line may still carry it, and
				// the category is what makes an escalation policy fire.
				if rr.Category == "" {
					rr.Category = ParseVerdict(text).Category
				}
			}
			if rr.Summary == "" {
				rr.Summary = ParseVerdict(text).Summary
			}
			return rr
		}
	}
	return ParseVerdict(text)
}
