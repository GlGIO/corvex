package orchestrator

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

// mockProgressProvider implements both provider.Provider and the optional
// provider.ProgressExecutor, so we can verify the Brainstormer switches to
// the progress path when SetProgressWriter is set.
type mockProgressProvider struct {
	mockProvider
	events []types.StreamEvent
}

func (m *mockProgressProvider) ExecuteWithProgress(ctx context.Context, req types.ExecuteRequest, onEvent func(types.StreamEvent)) (*types.ExecuteResult, error) {
	for _, ev := range m.events {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return m.Execute(ctx, req)
}

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

func TestBrainstormer_StreamsToolUsesWhenWriterSet(t *testing.T) {
	t.Parallel()

	canned := "...\n```brainstorm\n{\"type\":\"question\",\"text\":\"REST or GraphQL?\",\"recommended\":\"REST\",\"rationale\":\"matches existing\"}\n```"

	mp := &mockProgressProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
				return &types.ExecuteResult{Output: canned, CostUSD: 0.05}, nil
			},
		},
		events: []types.StreamEvent{
			{Type: types.EventToolUse, Tool: "Read", File: "package.json"},
			{Type: types.EventToolUse, Tool: "Glob", Content: "src/**/*.ts"},
			{Type: types.EventText, Content: "thinking..."}, // should not surface
		},
	}

	dir := t.TempDir()
	br := NewBrainstormer(mp, "sonnet", dir)
	var buf bytes.Buffer
	br.SetProgressWriter(&buf)

	step, err := br.Interview(context.Background(), "build something", filepath.Join(dir, "qa.md"))
	if err != nil {
		t.Fatalf("Interview error = %v", err)
	}
	if step.Question != "REST or GraphQL?" {
		t.Errorf("Question = %q", step.Question)
	}
	if step.CostUSD != 0.05 {
		t.Errorf("CostUSD = %v, want 0.05", step.CostUSD)
	}

	out := buf.String()
	if !strings.Contains(out, "Read package.json") {
		t.Errorf("progress missing Read line; got:\n%s", out)
	}
	if !strings.Contains(out, "Glob src/**/*.ts") {
		t.Errorf("progress missing Glob line; got:\n%s", out)
	}
	if strings.Contains(out, "thinking") {
		t.Errorf("progress should not echo plain text events; got:\n%s", out)
	}
}

func TestBrainstormer_NoStreamingFallsBackToExecute(t *testing.T) {
	t.Parallel()

	canned := "```brainstorm\n{\"type\":\"done\"}\n```"
	mp := &mockProgressProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
				return &types.ExecuteResult{Output: canned}, nil
			},
		},
		events: []types.StreamEvent{
			{Type: types.EventToolUse, Tool: "Read", File: "should-not-print.go"},
		},
	}

	dir := t.TempDir()
	br := NewBrainstormer(mp, "sonnet", dir)
	// SetProgressWriter intentionally NOT called.

	step, err := br.Interview(context.Background(), "x", filepath.Join(dir, "qa.md"))
	if err != nil {
		t.Fatalf("Interview error = %v", err)
	}
	if !step.Done {
		t.Error("expected Done = true")
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
