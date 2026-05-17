package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
)

const defaultInsightThreshold = 3

// Advisor analyses completed tasks and suggests new agent files for recurring patterns.
type Advisor struct {
	provider provider.Provider
	model    string
	workDir  string
}

// NewAdvisor creates an Advisor bound to the given provider and model.
func NewAdvisor(p provider.Provider, model, workDir string) *Advisor {
	return &Advisor{provider: p, model: model, workDir: workDir}
}

// Analyze scans completed tasks for types that exceed the threshold and lack a configured agent.
// For each qualifying type it generates a suggested agent prompt and writes it to
// .corvex/insights/<type>-agent-suggestion.md. Returns the list of insights produced.
func (a *Advisor) Analyze(ctx context.Context, tasks []types.Task, routing map[string]string, threshold int) ([]InsightData, error) {
	if threshold == 0 {
		return nil, nil
	}
	if threshold < 0 {
		threshold = defaultInsightThreshold
	}

	byType := map[string][]types.Task{}
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			typ := string(t.Type)
			byType[typ] = append(byType[typ], t)
		}
	}

	// Sort types for deterministic order.
	sortedTypes := make([]string, 0, len(byType))
	for typ := range byType {
		sortedTypes = append(sortedTypes, typ)
	}
	sort.Strings(sortedTypes)

	var insights []InsightData
	for _, typ := range sortedTypes {
		typeTasks := byType[typ]
		if len(typeTasks) < threshold {
			continue
		}
		if _, hasAgent := routing[typ]; hasAgent {
			continue
		}

		content, err := a.generateAgentSuggestion(ctx, typ, typeTasks)
		if err != nil {
			return nil, fmt.Errorf("generating suggestion for type %q: %w", typ, err)
		}

		suggestedPath := ".corvex/agents/" + typ + ".md"
		insight := InsightData{
			TaskType:         typ,
			Count:            len(typeTasks),
			SuggestedPath:    suggestedPath,
			SuggestedContent: content,
		}

		if err := a.writeSuggestion(typ, content); err != nil {
			return nil, fmt.Errorf("writing suggestion for type %q: %w", typ, err)
		}

		insights = append(insights, insight)
	}
	return insights, nil
}

func (a *Advisor) generateAgentSuggestion(ctx context.Context, taskType string, tasks []types.Task) (string, error) {
	prompt := buildAdvisorPrompt(taskType, tasks)

	result, err := a.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        a.model,
		WorkDir:      a.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		return "", fmt.Errorf("advisor execution: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (a *Advisor) writeSuggestion(taskType, content string) error {
	dir := filepath.Join(a.workDir, ".corvex", "insights")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating insights dir: %w", err)
	}
	path := filepath.Join(dir, taskType+"-agent-suggestion.md")
	return os.WriteFile(path, []byte(content+"\n"), 0o644)
}

func buildAdvisorPrompt(taskType string, tasks []types.Task) string {
	var b strings.Builder

	fmt.Fprintf(&b, `You are analysing a completed software feature to extract reusable conventions.

The following %d tasks were all of type "%s":

`, len(tasks), taskType)

	for _, t := range tasks {
		fmt.Fprintf(&b, "### %s — %s\n\n", t.ID, t.Title)
		if t.Description != "" {
			b.WriteString(t.Description)
			b.WriteString("\n\n")
		}
		if len(t.Criteria) > 0 {
			b.WriteString("Success criteria:\n")
			for _, c := range t.Criteria {
				fmt.Fprintf(&b, "- %s\n", c)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n\n## Goal\n\n")
	fmt.Fprintf(&b, "Write the content of a new agent file at `.corvex/agents/%s.md`.\n", taskType)
	fmt.Fprintf(&b, "This file will be prepended to the prompt of every future AI worker that handles a %q task.\n\n", taskType)
	b.WriteString(`Capture:
- Conventions and patterns evident from the tasks above
- Best practices for this type of work in this codebase (explore with Read/Glob/Grep)
- Specific constraints or rules that should always apply
- Common pitfalls to avoid based on what these tasks required

Write 2–4 focused paragraphs. Be specific and actionable — not generic advice.
Output ONLY the agent file content, no fencing, no extra commentary.
`)

	return b.String()
}
