package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
)

// BrainstormStep is one iteration of the requirements interview loop.
// When Done is true, enough information has been gathered to write spec.md.
type BrainstormStep struct {
	Done        bool
	Question    string
	Recommended string
	Rationale   string
	CostUSD     float64
	TokensIn    int
	TokensOut   int
	DurationMs  int64
}

// Brainstormer conducts an AI-driven Q&A to explore a vague feature idea,
// then synthesises the answers into a spec.md.
type Brainstormer struct {
	progressBase
	provider provider.Provider
	model    string
	workDir  string
}

// NewBrainstormer creates a Brainstormer bound to the given provider and model.
func NewBrainstormer(p provider.Provider, model, workDir string) *Brainstormer {
	return &Brainstormer{provider: p, model: model, workDir: workDir}
}

// Interview performs one Q&A step: reads the feature description and accumulated Q&A,
// then asks the provider for the next clarifying question (or declares done).
// The caller is responsible for appending the user's answer to qaPath before the next call.
func (b *Brainstormer) Interview(ctx context.Context, description, qaPath string) (*BrainstormStep, error) {
	qaContent, err := os.ReadFile(qaPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading Q&A file %s: %w", qaPath, err)
	}

	prompt := buildBrainstormerPrompt(description, string(qaContent))

	result, err := b.runStep(ctx, b.provider, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        b.model,
		WorkDir:      b.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		return nil, fmt.Errorf("brainstormer execution: %w", err)
	}

	step, err := parseBrainstormStep(result.Output)
	if err != nil {
		return nil, fmt.Errorf("parsing brainstormer output: %w", err)
	}
	step.CostUSD = result.CostUSD
	step.TokensIn = result.TokensIn
	step.TokensOut = result.TokensOut
	step.DurationMs = result.DurationMs
	return step, nil
}

// GenerateSpec synthesises the feature description and accumulated Q&A into a spec.md file.
func (b *Brainstormer) GenerateSpec(ctx context.Context, description, qaPath, specPath string) error {
	qaContent, err := os.ReadFile(qaPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Q&A file %s: %w", qaPath, err)
	}

	prompt := buildSpecGenPrompt(description, string(qaContent))

	result, err := b.runStep(ctx, b.provider, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        b.model,
		WorkDir:      b.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		return fmt.Errorf("spec generation execution: %w", err)
	}

	specContent, err := extractSpecBlock(result.Output)
	if err != nil {
		return fmt.Errorf("extracting spec block: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		return fmt.Errorf("creating spec directory: %w", err)
	}
	if err := os.WriteFile(specPath, []byte(specContent), 0o644); err != nil {
		return fmt.Errorf("writing spec.md: %w", err)
	}
	return nil
}

func buildBrainstormerPrompt(description, qaContent string) string {
	var b strings.Builder

	b.WriteString(`You are a requirements analyst helping a developer refine a feature idea for the Corvex planner. The user has ALREADY provided a description below — read it carefully, then ask ONE concrete clarifying question that NARROWS the scope. Your goal is to convert a vague idea into a buildable spec through 3–5 targeted questions, not to re-elicit the idea itself.

## Feature description (from the user)

`)
	b.WriteString(description)

	b.WriteString("\n\n## Q&A so far\n\n")
	if strings.TrimSpace(qaContent) != "" {
		b.WriteString(qaContent)
	} else {
		b.WriteString("None yet.")
	}

	b.WriteString(`

## Hard rules

- The description above IS the feature idea, even if it is vague. Treat it as ground truth. **Never** ask "what is the feature?", "describe the problem", "who uses it?", or any other variant that re-elicits the description. Those are forbidden.
- ` + "`recommended`" + ` MUST be a concrete, opinionated default answer the user can accept with one Enter — never empty, never a placeholder like "Ex.:" or "TBD". If you can't form a recommendation, you don't have a useful question.
- Each question must NARROW one specific decision. Examples of useful questions for an analytics feature like the one above:
  • "Track scan events at the QR endpoint or at the redirect target? The first gives reliable counts; the second confirms intent."
  • "Banner suggestions: rank by historical CTR within the same store, or by category match with the active promotion?"
- Use Read/Glob/Grep to ground the recommendation in actual code conventions — but cap yourself at 4 tool calls per turn. Don't survey the whole repo; one targeted Glob and a couple of Reads is plenty.
- Declare done when 3–5 useful answers are accumulated and the spec writer has enough to produce file paths, data shapes, and acceptance criteria.

## Output format

End your response with exactly one fenced code block tagged ` + "`brainstorm`" + `, containing a single JSON object. No other text after the block.

For a question (recommended must be a real answer, not a request to be more specific):

` + "```brainstorm\n" + `{"type":"question","text":"<narrow question>","recommended":"<concrete default answer>","rationale":"<why this decision matters>"}
` + "```" + `

When done:

` + "```brainstorm\n" + `{"type":"done"}
` + "```" + `
`)

	return b.String()
}

func buildSpecGenPrompt(description, qaContent string) string {
	var b strings.Builder

	b.WriteString(`You are a technical writer generating a project spec for the Corvex planner.

## Feature Idea

`)
	b.WriteString(description)

	b.WriteString("\n\n## Design Q&A\n\n")
	if strings.TrimSpace(qaContent) != "" {
		b.WriteString(qaContent)
	} else {
		b.WriteString("None recorded.")
	}

	b.WriteString(`

## Instructions

Write a complete spec.md that a Corvex planner can decompose into tasks. Include:

- A concise title (# heading)
- ## Objective — one paragraph
- ## Requirements — bulleted list of concrete, verifiable requirements
- ## Validation Criteria — checkboxes (- [ ] ...) that define done
- ## Out of Scope — what this feature explicitly will NOT do

Reflect all decisions from the Q&A above. Be specific about file paths, APIs, and data shapes
where the Q&A established them.

End your response with exactly one fenced code block tagged ` + "`spec`" + `. No other text after the block.

` + "```spec\n" + `# ...
` + "```" + `
`)

	return b.String()
}

var brainstormBlockRe = regexp.MustCompile("(?s)```brainstorm\\s*\\n(.*?)\\n```")
var specBlockRe = regexp.MustCompile("(?s)```spec\\s*\\n(.*?)\\n```")

type brainstormPayload struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Recommended string `json:"recommended"`
	Rationale   string `json:"rationale"`
}

func parseBrainstormStep(output string) (*BrainstormStep, error) {
	matches := brainstormBlockRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no ```brainstorm block found in output")
	}
	raw := matches[len(matches)-1][1]
	var p brainstormPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("invalid json in brainstorm block: %w", err)
	}
	switch p.Type {
	case "done":
		return &BrainstormStep{Done: true}, nil
	case "question":
		if strings.TrimSpace(p.Text) == "" {
			return nil, fmt.Errorf("question type missing text")
		}
		return &BrainstormStep{
			Question:    p.Text,
			Recommended: p.Recommended,
			Rationale:   p.Rationale,
		}, nil
	default:
		return nil, fmt.Errorf("unknown brainstorm type %q", p.Type)
	}
}

func extractSpecBlock(output string) (string, error) {
	matches := specBlockRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("no ```spec block found in output")
	}
	content := strings.TrimSpace(matches[len(matches)-1][1])
	if !strings.HasPrefix(content, "# ") {
		return "", fmt.Errorf("spec block does not start with a # heading")
	}
	return content + "\n", nil
}
