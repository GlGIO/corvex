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

	b.WriteString(`You are a requirements analyst helping define a software feature.

## Goal

Ask ONE clarifying question that will most help define this feature. Ground your recommendation
in conventions visible in the codebase. When you have enough information (typically 3–5 answers)
to write a complete spec.md, declare done.

## Feature Idea

`)
	b.WriteString(description)

	b.WriteString("\n\n## Q&A so far\n\n")
	if strings.TrimSpace(qaContent) != "" {
		b.WriteString(qaContent)
	} else {
		b.WriteString("None yet.")
	}

	b.WriteString(`

## How to work

- **Be fast.** The user is waiting at a prompt. Ask immediately based on the description plus any prior Q&A — do NOT survey the codebase upfront.
- Use Read/Glob/Grep **only when an answer cannot be defaulted without that look-up**, and cap yourself at **3 tool calls total** for this turn. If a single targeted Read can confirm a convention, do it; otherwise rely on the description and your recommendation. Deep exploration is the Planner's job, not yours.
- Pick ONE high-impact question whose answer unlocks several downstream decisions.
- Skip trivially defaultable details; ask only about choices that materially change what gets built.
- Declare done when the idea plus gathered answers are sufficient for a competent planner (typically 3–5 answers).

## Output format

End your response with exactly one fenced code block tagged ` + "`brainstorm`" + `, containing a single JSON
object. No other text after the block.

For a question:

` + "```brainstorm\n" + `{"type":"question","text":"<question>","recommended":"<answer>","rationale":"<short reason>"}
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
