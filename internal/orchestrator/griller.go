package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
)

// GrillStep is one iteration of the design interview loop.
// When Done is true, no further questions remain. Otherwise Question/Recommended/Rationale describe
// the next ambiguity the Griller wants the user to resolve.
type GrillStep struct {
	Done        bool
	Question    string
	Recommended string
	Rationale   string
	CostUSD     float64
	TokensIn    int
	TokensOut   int
	DurationMs  int64
}

// Griller runs the AI provider with read-only tools to surface unresolved ambiguities in a spec.
type Griller struct {
	provider provider.Provider
	model    string
	workDir  string
}

// NewGriller creates a Griller bound to the given provider and model.
func NewGriller(p provider.Provider, model, workDir string) *Griller {
	return &Griller{provider: p, model: model, workDir: workDir}
}

// Grill performs one interview step: reads spec + existing decisions, asks the provider for the
// next question (or "done"). The caller is responsible for collecting the user's answer and
// appending it to the decisions file before the next call.
func (g *Griller) Grill(ctx context.Context, specPath, decisionsPath string) (*GrillStep, error) {
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("reading spec %s: %w", specPath, err)
	}

	decisionsContent, err := os.ReadFile(decisionsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading decisions %s: %w", decisionsPath, err)
	}

	prompt := buildGrillerPrompt(string(specContent), string(decisionsContent))

	result, err := g.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        g.model,
		WorkDir:      g.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		return nil, fmt.Errorf("griller execution: %w", err)
	}

	step, err := parseGrillStep(result.Output)
	if err != nil {
		return nil, fmt.Errorf("parsing griller output: %w", err)
	}
	step.CostUSD = result.CostUSD
	step.TokensIn = result.TokensIn
	step.TokensOut = result.TokensOut
	step.DurationMs = result.DurationMs
	return step, nil
}

func buildGrillerPrompt(specContent, decisionsContent string) string {
	var b strings.Builder

	b.WriteString(`You are a design interviewer for the Corvex project planner.

## Goal

Find the SINGLE most important unresolved ambiguity in this project spec and ask the user about
it. Propose a recommended answer grounded in conventions visible in the codebase or in widely
accepted best practice.

## Project Specification

`)
	b.WriteString(specContent)

	b.WriteString("\n\n## Decisions already resolved\n\n")
	if strings.TrimSpace(decisionsContent) != "" {
		b.WriteString(decisionsContent)
	} else {
		b.WriteString("None yet.")
	}

	b.WriteString(`

## How to work

- Before asking, explore the workdir with Read/Glob/Grep to ground your recommendation. Existing
  conventions beat fresh opinions.
- Pick ONE high-impact question that, once answered, unlocks several downstream decisions.
- Skip cosmetic or trivially defaultable details. Ask only about choices that materially change
  what gets built or how it's structured.
- If the resolved decisions plus the spec are sufficient for a competent planner to decompose
  this into tasks, declare done.

## Output format

End your response with exactly one fenced code block tagged ` + "`grill`" + `, containing a single JSON
object. No other text after the block.

For a question:

` + "```grill\n" + `{"type":"question","text":"<question>","recommended":"<answer>","rationale":"<short reason>"}
` + "```" + `

When done:

` + "```grill\n" + `{"type":"done"}
` + "```" + `
`)

	return b.String()
}

var grillBlockRe = regexp.MustCompile("(?s)```grill\\s*\\n(.*?)\\n```")

type grillPayload struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Recommended string `json:"recommended"`
	Rationale   string `json:"rationale"`
}

func parseGrillStep(output string) (*GrillStep, error) {
	matches := grillBlockRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no ```grill block found in output")
	}
	// Use the last fenced block (the model may discuss earlier in the response).
	raw := matches[len(matches)-1][1]
	var p grillPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("invalid json in grill block: %w", err)
	}
	switch p.Type {
	case "done":
		return &GrillStep{Done: true}, nil
	case "question":
		if strings.TrimSpace(p.Text) == "" {
			return nil, fmt.Errorf("question type missing text")
		}
		return &GrillStep{
			Question:    p.Text,
			Recommended: p.Recommended,
			Rationale:   p.Rationale,
		}, nil
	default:
		return nil, fmt.Errorf("unknown grill type %q", p.Type)
	}
}
