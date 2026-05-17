package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
)

// ValidationResult carries the outcome of a full integration validation run.
type ValidationResult struct {
	Verdict    ReviewVerdict
	Summary    string
	CostUSD    float64
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// Validator runs an integration check against the live stack at the end of a feature run.
type Validator struct {
	provider provider.Provider
	model    string
	workDir  string
}

// NewValidator creates a Validator bound to the given provider and model.
func NewValidator(p provider.Provider, model, workDir string) *Validator {
	return &Validator{provider: p, model: model, workDir: workDir}
}

// Validate sends spec + tasks to the AI, which tests the live running stack and emits a verdict.
func (v *Validator) Validate(ctx context.Context, specPath, tasksPath string, cfg config.ValidateConfig) (*ValidationResult, error) {
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}

	tasks, err := os.ReadFile(tasksPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading tasks: %w", err)
	}

	prompt := buildValidatorPrompt(string(spec), string(tasks), cfg)

	result, err := v.provider.Execute(ctx, types.ExecuteRequest{
		Prompt:       prompt,
		Model:        v.model,
		WorkDir:      v.workDir,
		AllowedTools: []string{"Read", "Glob", "Grep", "Bash"},
	})
	if err != nil {
		return nil, fmt.Errorf("validator execution: %w", err)
	}

	rr := parseVerdict(result.Output)
	return &ValidationResult{
		Verdict:    rr.Verdict,
		Summary:    rr.Summary,
		CostUSD:    result.CostUSD,
		TokensIn:   result.TokensIn,
		TokensOut:  result.TokensOut,
		DurationMs: result.DurationMs,
	}, nil
}

func buildValidatorPrompt(spec, tasks string, cfg config.ValidateConfig) string {
	var b strings.Builder

	b.WriteString("You are an integration tester for Corvex. The feature has been built — your job is to validate it against the live running stack.\n\n")

	b.WriteString("## Project Specification\n\n")
	b.WriteString(spec)

	if strings.TrimSpace(tasks) != "" {
		b.WriteString("\n\n## Completed Tasks\n\n")
		b.WriteString(tasks)
	}

	b.WriteString("\n\n## Running Stack\n\n")
	fmt.Fprintf(&b, "- App: http://localhost:%d\n", cfg.Stack.Port)
	if cfg.Stack.Framework != "" {
		fmt.Fprintf(&b, "- Framework: %s (%s)\n", cfg.Stack.Framework, cfg.Stack.Runtime)
	}
	if cfg.Database.Type != "" && cfg.Database.Type != "none" {
		fmt.Fprintf(&b, "- Database: %s (default port, credentials in environment)\n", cfg.Database.Type)
	}
	if cfg.UI.Enabled {
		b.WriteString("- Chrome DevTools Protocol: http://localhost:9222\n")
	}

	b.WriteString("\n## Validation Instructions\n\n")
	b.WriteString("1. Read the spec and identify what must be true: endpoints, behaviors, data invariants.\n")
	b.WriteString("2. Use Bash (curl) to test each API endpoint — verify status codes and response structure.\n")
	b.WriteString("3. Where relevant, query the database directly to verify state.\n")

	if cfg.UI.Enabled {
		b.WriteString("4. Use Chrome CDP (http://localhost:9222) to validate UI flows:\n")
		b.WriteString("   - List tabs: GET http://localhost:9222/json\n")
		b.WriteString("   - Use the CDP WebSocket or the /json/new endpoint to navigate\n")
		b.WriteString("   - You may use Bash to run a small Node or Python script for CDP interaction\n")
		b.WriteString("   - Read the DOM/accessibility tree to verify content without screenshots\n")
	}

	b.WriteString("\nFocus on critical paths. Be specific about what passes and what fails.\n")
	b.WriteString("\nEnd your response with EXACTLY one of:\n")
	b.WriteString("VERDICT: PASS\n")
	b.WriteString("VERDICT: FAIL\n")

	return b.String()
}
