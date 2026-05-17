package orchestrator

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
	VerdictPass ReviewVerdict = "PASS"
	VerdictFail ReviewVerdict = "FAIL"
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
	provider provider.Provider
	model    string
	workDir  string
}

// NewReviewer creates a Reviewer bound to the given provider and model.
func NewReviewer(p provider.Provider, model, workDir string) *Reviewer {
	return &Reviewer{provider: p, model: model, workDir: workDir}
}

// Review executes the AI reviewer for the given task and parses the verdict.
func (r *Reviewer) Review(ctx context.Context, t *types.Task) (*ReviewResult, error) {
	prompt := buildReviewerPrompt(t)

	result, err := r.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        r.model,
		WorkDir:      r.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep", "Bash"},
	})
	if err != nil {
		return nil, fmt.Errorf("reviewer execution for task %s: %w", t.ID, err)
	}

	rr := parseVerdict(result.Output)
	rr.CostUSD = result.CostUSD
	rr.TokensIn = result.TokensIn
	rr.TokensOut = result.TokensOut
	rr.DurationMs = result.DurationMs
	return rr, nil
}

func buildReviewerPrompt(t *types.Task) string {
	var b strings.Builder

	fmt.Fprintf(&b, `You are a code reviewer for Corvex. Verify that this task was completed correctly.

## Task: %s — %s

### Description

%s

### Success Criteria

`, t.ID, t.Title, t.Description)

	for _, c := range t.Criteria {
		fmt.Fprintf(&b, "- %s\n", c)
	}

	b.WriteString("\n### Expected Files\n\n")
	for _, f := range t.Files.Create {
		fmt.Fprintf(&b, "- Should exist (created): %s\n", f)
	}
	for _, f := range t.Files.Modify {
		fmt.Fprintf(&b, "- Should be modified: %s\n", f)
	}

	b.WriteString(`
## Verification Steps

1. Check that all files listed as "created" exist on disk
2. Verify each success criterion by reading the relevant code
3. If criteria include commands (like tests, builds), run them
4. Check git diff for the task's changes

## Output Format

End your response with EXACTLY one of these lines:
VERDICT: PASS
VERDICT: FAIL

When the verdict is FAIL, also include a CATEGORY: line right before the
verdict, choosing exactly one of:
- wrong-approach     — the implementation took the wrong design or direction
- missing-edge-case  — works for the happy path but misses important edge cases
- incomplete         — one or more success criteria are not fully addressed
- flaky-test         — failing tests appear flaky or environment-dependent
- style              — only style / convention issues

Example:
CATEGORY: missing-edge-case
VERDICT: FAIL

Before those lines, provide your analysis.
`)

	return b.String()
}

func parseVerdict(output string) *ReviewResult {
	lines := strings.Split(output, "\n")

	verdict := VerdictFail
	verdictIdx := -1

	for i := len(lines) - 1; i >= 0; i-- {
		upper := strings.TrimSpace(strings.ToUpper(lines[i]))
		if upper == "VERDICT: PASS" {
			verdict = VerdictPass
			verdictIdx = i
			break
		}
		if upper == "VERDICT: FAIL" {
			verdict = VerdictFail
			verdictIdx = i
			break
		}
	}

	// Category is only meaningful on FAIL. Search the last few lines for a
	// CATEGORY: marker; tolerate whitespace and casing.
	category := ""
	if verdict == VerdictFail {
		start := verdictIdx - 5
		if start < 0 {
			start = 0
		}
		end := verdictIdx
		if end < 0 {
			end = len(lines)
		}
		for i := end - 1; i >= start; i-- {
			line := strings.TrimSpace(lines[i])
			upper := strings.ToUpper(line)
			if strings.HasPrefix(upper, "CATEGORY:") {
				category = strings.TrimSpace(line[len("CATEGORY:"):])
				category = strings.ToLower(category)
				break
			}
		}
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
