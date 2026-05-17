package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestParseBrainstormStep_Question(t *testing.T) {
	t.Parallel()
	output := "Reasoning blah blah.\n\n```brainstorm\n" +
		`{"type":"question","text":"Use REST or GraphQL?","recommended":"REST","rationale":"matches existing endpoints"}` +
		"\n```\n"

	step, err := parseBrainstormStep(output)
	if err != nil {
		t.Fatalf("parseBrainstormStep error = %v", err)
	}
	if step.Done {
		t.Error("expected Done = false")
	}
	if step.Question != "Use REST or GraphQL?" {
		t.Errorf("Question = %q", step.Question)
	}
	if step.Recommended != "REST" {
		t.Errorf("Recommended = %q", step.Recommended)
	}
	if step.Rationale != "matches existing endpoints" {
		t.Errorf("Rationale = %q", step.Rationale)
	}
}

func TestParseBrainstormStep_Done(t *testing.T) {
	t.Parallel()
	output := "All clear.\n\n```brainstorm\n{\"type\":\"done\"}\n```"
	step, err := parseBrainstormStep(output)
	if err != nil {
		t.Fatalf("parseBrainstormStep error = %v", err)
	}
	if !step.Done {
		t.Error("expected Done = true")
	}
}

func TestParseBrainstormStep_NoBlock(t *testing.T) {
	t.Parallel()
	_, err := parseBrainstormStep("just some text without a fence")
	if err == nil {
		t.Fatal("expected error for missing brainstorm block")
	}
}

func TestParseBrainstormStep_InvalidJSON(t *testing.T) {
	t.Parallel()
	output := "```brainstorm\nnot-json\n```"
	_, err := parseBrainstormStep(output)
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestParseBrainstormStep_UnknownType(t *testing.T) {
	t.Parallel()
	output := "```brainstorm\n{\"type\":\"weird\"}\n```"
	_, err := parseBrainstormStep(output)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestParseBrainstormStep_QuestionMissingText(t *testing.T) {
	t.Parallel()
	output := "```brainstorm\n{\"type\":\"question\",\"text\":\"\"}\n```"
	_, err := parseBrainstormStep(output)
	if err == nil {
		t.Fatal("expected error for empty question text")
	}
}

func TestParseBrainstormStep_LastBlockWins(t *testing.T) {
	t.Parallel()
	output := "first attempt:\n```brainstorm\n{\"type\":\"question\",\"text\":\"old\"}\n```\nreconsidered:\n```brainstorm\n{\"type\":\"done\"}\n```"
	step, err := parseBrainstormStep(output)
	if err != nil {
		t.Fatalf("parseBrainstormStep error = %v", err)
	}
	if !step.Done {
		t.Error("expected last block (done) to win")
	}
}

func TestExtractSpecBlock_Valid(t *testing.T) {
	t.Parallel()
	output := "Here is your spec:\n\n```spec\n# My Feature\n## Objective\nDo something.\n```\n"
	content, err := extractSpecBlock(output)
	if err != nil {
		t.Fatalf("extractSpecBlock error = %v", err)
	}
	if !strings.HasPrefix(content, "# My Feature") {
		t.Errorf("content = %q", content)
	}
}

func TestExtractSpecBlock_NoBlock(t *testing.T) {
	t.Parallel()
	_, err := extractSpecBlock("no spec here")
	if err == nil {
		t.Fatal("expected error for missing spec block")
	}
}

func TestExtractSpecBlock_MissingHeading(t *testing.T) {
	t.Parallel()
	output := "```spec\nObjective\nno heading\n```"
	_, err := extractSpecBlock(output)
	if err == nil {
		t.Fatal("expected error for spec without # heading")
	}
}

func TestBuildBrainstormerPrompt_IncludesDescription(t *testing.T) {
	t.Parallel()
	prompt := buildBrainstormerPrompt("Add OAuth login", "")
	if !strings.Contains(prompt, "Add OAuth login") {
		t.Error("prompt missing feature description")
	}
	if !strings.Contains(prompt, "None yet.") {
		t.Error("prompt should mark Q&A as empty when none recorded")
	}
	if !strings.Contains(prompt, "```brainstorm") {
		t.Error("prompt missing brainstorm output format instruction")
	}
}

func TestBuildBrainstormerPrompt_IncludesQA(t *testing.T) {
	t.Parallel()
	prompt := buildBrainstormerPrompt("some idea", "## Q: provider?\n**A:** GitHub\n")
	if !strings.Contains(prompt, "**A:** GitHub") {
		t.Error("prompt missing existing Q&A content")
	}
	if strings.Contains(prompt, "None yet.") {
		t.Error("prompt should not say 'None yet.' when Q&A exists")
	}
}

func TestGenerateSpec_WritesFile(t *testing.T) {
	t.Parallel()

	specOutput := "Here is the spec:\n\n```spec\n# OAuth Login\n## Objective\nAllow users to log in via GitHub OAuth.\n## Requirements\n- OAuth flow\n## Validation Criteria\n- [ ] Login works\n## Out of Scope\n- Other providers\n```\n"

	mock := &mockProvider{executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
		return &types.ExecuteResult{Output: specOutput}, nil
	}}
	br := NewBrainstormer(mock, "test-model", t.TempDir())

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")

	if err := br.GenerateSpec(t.Context(), "OAuth login feature", "", specPath); err != nil {
		t.Fatalf("GenerateSpec error = %v", err)
	}

	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading spec.md: %v", err)
	}
	if !strings.HasPrefix(string(data), "# OAuth Login") {
		t.Errorf("spec.md content = %q", string(data))
	}
}
