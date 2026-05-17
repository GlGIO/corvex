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

// Planner runs the AI provider with read-only tools to generate or update a tasks.md file.
type Planner struct {
	provider     provider.Provider
	model        string
	workDir      string
	agentRouting map[string]string
}

// NewPlanner creates a Planner that uses the given provider and model.
// agentRouting is the agent_routing map from config; pass nil if not configured.
func NewPlanner(p provider.Provider, model, workDir string, agentRouting map[string]string) *Planner {
	return &Planner{provider: p, model: model, workDir: workDir, agentRouting: agentRouting}
}

// Plan generates a tasks.md by sending spec + anchor context to the AI provider.
func (p *Planner) Plan(ctx context.Context, specPath, anchorPath, tasksPath string) error {
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("reading spec %s: %w", specPath, err)
	}

	anchorContent, err := os.ReadFile(anchorPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading anchor %s: %w", anchorPath, err)
	}
	existingTasks, err := os.ReadFile(tasksPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading existing tasks %s: %w", tasksPath, err)
	}

	decisionsPath := filepath.Join(filepath.Dir(specPath), "decisions.md")
	decisionsContent, err := os.ReadFile(decisionsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading decisions %s: %w", decisionsPath, err)
	}

	spec := string(specContent)
	if strings.TrimSpace(string(decisionsContent)) != "" {
		spec += "\n\n## Resolved Design Decisions (from `corvex grill`)\n\n" + string(decisionsContent)
	}

	prompt := buildPlannerPrompt(spec, string(anchorContent), string(existingTasks), p.agentRouting)

	result, err := p.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        p.model,
		WorkDir:      p.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep"},
	})
	if err != nil {
		return fmt.Errorf("planner execution: %w", err)
	}

	content := extractTasksContent(result.Output)

	dir := filepath.Dir(tasksPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating tasks dir %s: %w", dir, err)
	}

	if err := os.WriteFile(tasksPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing tasks %s: %w", tasksPath, err)
	}

	return nil
}

func buildPlannerPrompt(specContent, anchorContent, existingTasks string, agentRouting map[string]string) string {
	var b strings.Builder

	b.WriteString(`You are a project planner for Corvex. Your job is to decompose a project specification
into executable tasks.

## Project Specification

`)
	b.WriteString(specContent)

	b.WriteString("\n\n## Current State (anchor.yaml)\n\n")
	if strings.TrimSpace(anchorContent) != "" {
		b.WriteString(anchorContent)
	} else {
		b.WriteString("No previous state — this is a fresh project.")
	}

	b.WriteString("\n\n## Existing Tasks\n\n")
	if strings.TrimSpace(existingTasks) != "" {
		b.WriteString(existingTasks)
	} else {
		b.WriteString("No existing tasks — generate from scratch.")
	}

	b.WriteString("\n\n## Available Task Types\n\n")
	b.WriteString("Assign one of the following types to each task's YAML block.\n\n")
	b.WriteString("Built-in types (always available):\n")
	b.WriteString("- `database` — schema migrations, seed data, query optimisation\n")
	b.WriteString("- `backend`  — API endpoints, business logic, services\n")
	b.WriteString("- `frontend` — UI components, pages, styles\n")
	b.WriteString("- `review`   — code review, audit, documentation\n")
	b.WriteString("- `general`  — anything that doesn't fit a specific type\n")
	if len(agentRouting) > 0 {
		b.WriteString("\nProject-specific types (dedicated agents configured):\n")
		keys := make([]string, 0, len(agentRouting))
		for k := range agentRouting {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- `%s`\n", k)
		}
		b.WriteString("\nPrefer project-specific types when the task fits — they carry specialised instructions.\n")
	}

	b.WriteString(`

## Instructions

Analyze the specification and generate a complete tasks.md file.
Each task should have:
- A unique ID (S01, S02, ...)
- A descriptive title
- Status (⬜ PENDING for new tasks)
- YAML block with type and depends_on
- "O que fazer" section
- "Critérios de sucesso" section
- "Arquivos" section

Include a YAML frontmatter with the DAG (dependencies map).

Output ONLY the complete tasks.md file content, nothing else.
`)

	return b.String()
}

// extractTasksContent pulls tasks.md content from AI output, handling code fences.
func extractTasksContent(output string) string {
	trimmed := strings.TrimSpace(output)
	if strings.HasPrefix(trimmed, "---") {
		return trimmed
	}

	fenceStart := strings.Index(trimmed, "```")
	if fenceStart == -1 {
		return trimmed
	}

	afterFence := trimmed[fenceStart+3:]
	nlIdx := strings.Index(afterFence, "\n")
	if nlIdx == -1 {
		return trimmed
	}

	body := afterFence[nlIdx+1:]
	fenceEnd := strings.LastIndex(body, "```")
	if fenceEnd == -1 {
		return trimmed
	}

	inner := strings.TrimSpace(body[:fenceEnd])
	if strings.HasPrefix(inner, "---") {
		return inner
	}

	return trimmed
}
