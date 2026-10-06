package step

import (
	"context"
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
)

// ReviewVerdict represents the outcome of a task review.
type ReviewVerdict string

const (
	VerdictPass          ReviewVerdict = "PASS"
	VerdictFail          ReviewVerdict = "FAIL"
	VerdictIndeterminate ReviewVerdict = "INDETERMINATE"
)

// ReviewResult carries the reviewer's verdict and analysis summary.
type ReviewResult struct {
	Verdict ReviewVerdict
	// Category, when non-empty, classifies a failed verdict (e.g.
	// "wrong-approach", "missing-edge-case", "flaky-test"). Used by the
	// escalation engine. Empty for PASS verdicts or when the Reviewer
	// did not emit a CATEGORY: line.
	Category   string
	Summary    string
	CostUSD    float64
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// Reviewer independently verifies that a task was completed correctly.
type Reviewer struct {
	provider.ProgressBase
	provider provider.Provider
	model    string
	workDir  string
	skill    string // optional repo skill to guide the review
}

// NewReviewer creates a Reviewer bound to the given provider and model.
// skill (optional) is a repo skill the Reviewer is told to use; when set, the
// Skill tool is allowed so it can be invoked.
func NewReviewer(p provider.Provider, model, workDir, skill string) *Reviewer {
	return &Reviewer{provider: p, model: model, workDir: workDir, skill: skill}
}

// Review executes the AI reviewer for the given task and parses the verdict.
func (r *Reviewer) Review(ctx context.Context, t *types.Task) (*ReviewResult, error) {
	prompt := buildReviewerPrompt(t, r.skill)

	allowedTools := []string{"Read", "Glob", "Grep", "Bash"}
	if r.skill != "" {
		allowedTools = append(allowedTools, "Skill")
	}

	// The reviewer has Bash so it can run the tests, which also means it can
	// edit the work it is judging — and a verdict on a tree the judge itself
	// changed is a verdict on nobody's work. So the tree is fingerprinted
	// around the call, and a judge that moved it has its verdict discarded.
	before := treeState(ctx, r.workDir)

	result, err := r.RunStep(ctx, r.provider, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        r.model,
		WorkDir:      r.workDir,
		AllowedTools: allowedTools,
	})
	if err != nil {
		return nil, fmt.Errorf("reviewer execution for task %s: %w", t.ID, err)
	}

	rr := ParseVerdict(result.Output)
	rr.CostUSD = result.CostUSD
	rr.TokensIn = result.TokensIn
	rr.TokensOut = result.TokensOut
	rr.DurationMs = result.DurationMs
	// The result rides along with the error: the call was made and paid for,
	// and a caller that drops the spend because the verdict is void would make
	// the ledger blind in exactly the case worth accounting for.
	if before != "" && treeState(ctx, r.workDir) != before {
		return rr, fmt.Errorf("reviewer for task %s changed the working tree it was judging; its verdict (%s) is discarded", t.ID, rr.Verdict)
	}
	return rr, nil
}

// findCategory looks for the CATEGORY: line around the verdict: the five lines
// before it (where the prompt asks for it), then the five after (where a model
// that reordered them puts it). Nothing further away is accepted — a CATEGORY:
// at the top of a long review is more likely to be the reviewer QUOTING the
// instruction than answering it.
func findCategory(lines []string, verdictIdx int) string {
	look := func(from, to int) string {
		if from < 0 {
			from = 0
		}
		if to > len(lines) {
			to = len(lines)
		}
		for i := from; i < to; i++ {
			normalized := stripMarkup(lines[i])
			if strings.HasPrefix(normalized, "CATEGORY:") {
				// The markup is stripped from the VALUE too, not just from the
				// label: `**CATEGORY:** flaky-test` — ordinary markdown from a
				// model asked to emphasise a field — otherwise yields the
				// category `** flaky-test`, which matches no policy and fails
				// the same silent way a missing line does.
				value := strings.TrimSpace(normalized[len("CATEGORY:"):])
				value = strings.Trim(value, " *_`:")
				return strings.ToLower(strings.TrimSpace(value))
			}
		}
		return ""
	}
	if verdictIdx < 0 {
		return look(0, len(lines))
	}
	if cat := look(verdictIdx-5, verdictIdx); cat != "" {
		return cat
	}
	return look(verdictIdx+1, verdictIdx+6)
}

// stripMarkup strips leading markdown/quote markers (* _ # > ` and spaces)
// and trailing punctuation/markup (. * _ ` and spaces), then uppercases the
// result for tolerant verdict/category matching.
func stripMarkup(line string) string {
	line = strings.TrimLeft(line, " *_#>`")
	line = strings.TrimRight(line, " .*_`")
	return strings.ToUpper(strings.TrimSpace(line))
}

func ParseVerdict(output string) *ReviewResult {
	lines := strings.Split(output, "\n")

	verdict := VerdictIndeterminate
	verdictIdx := -1

	for i := len(lines) - 1; i >= 0; i-- {
		normalized := stripMarkup(lines[i])
		if normalized == "VERDICT: PASS" {
			verdict = VerdictPass
			verdictIdx = i
			break
		}
		if normalized == "VERDICT: FAIL" {
			verdict = VerdictFail
			verdictIdx = i
			break
		}
	}

	// Category is only meaningful on FAIL. The prompt asks for the CATEGORY:
	// line right before the verdict, so that is where it is looked for first —
	// and then, if it is not there, in the lines just after.
	//
	// The second look is not tolerance for its own sake. The category is what
	// selects an escalation policy (upgrade the model, spawn an investigation,
	// hand the task to a person), so a model that puts the line one position
	// lower does not produce a worse category: it produces NO category, and the
	// policy then never fires, in silence, on the path that exists for when
	// things are going badly. Found while driving the escalation with a stub
	// that emitted `VERDICT: FAIL` then `CATEGORY: correctness` — a shape the
	// prompt does not ask for and a model can easily produce.
	category := ""
	if verdict == VerdictFail {
		category = findCategory(lines, verdictIdx)
	}

	summary := strings.TrimSpace(output)
	if verdictIdx > 0 {
		summary = strings.TrimSpace(strings.Join(lines[:verdictIdx], "\n"))
	}

	return &ReviewResult{
		Verdict:  verdict,
		Category: category,
		Summary:  summary,
	}
}
