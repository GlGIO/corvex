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
	return rr, nil
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

	// Category is only meaningful on FAIL. Search the last few lines for a
	// CATEGORY: marker; tolerate markdown markup and casing.
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
			normalized := stripMarkup(lines[i])
			if strings.HasPrefix(normalized, "CATEGORY:") {
				category = strings.ToLower(strings.TrimSpace(normalized[len("CATEGORY:"):]))
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
