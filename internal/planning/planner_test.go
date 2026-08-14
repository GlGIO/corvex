package planning

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestBuildPlannerPrompt_Fresh(t *testing.T) {
	t.Parallel()
	prompt := buildPlannerPrompt("Build a CLI tool", "", "", nil, "")

	if !strings.Contains(prompt, "Build a CLI tool") {
		t.Error("prompt missing spec content")
	}
	if !strings.Contains(prompt, "No previous state") {
		t.Error("prompt missing 'No previous state' default for empty anchor")
	}
	if !strings.Contains(prompt, "No existing tasks") {
		t.Error("prompt missing 'No existing tasks' default for empty tasks")
	}
}

func TestBuildPlannerPrompt_WithAnchor(t *testing.T) {
	t.Parallel()
	anchorContent := "project: myproject\ncompleted:\n  - id: S01"
	prompt := buildPlannerPrompt("spec content", anchorContent, "", nil, "")

	if !strings.Contains(prompt, anchorContent) {
		t.Error("prompt missing anchor content")
	}
	if strings.Contains(prompt, "No previous state") {
		t.Error("prompt should not contain 'No previous state' when anchor is provided")
	}
}

func TestBuildPlannerPrompt_WithExistingTasks(t *testing.T) {
	t.Parallel()
	existingTasks := "## S01 — First Task ⬜ PENDING"
	prompt := buildPlannerPrompt("spec content", "", existingTasks, nil, "")

	if !strings.Contains(prompt, existingTasks) {
		t.Error("prompt missing existing tasks content")
	}
	if strings.Contains(prompt, "No existing tasks") {
		t.Error("prompt should not contain 'No existing tasks' when tasks are provided")
	}
}

func TestBuildPlannerPrompt_Structure(t *testing.T) {
	t.Parallel()
	prompt := buildPlannerPrompt("my spec", "my anchor", "my tasks", nil, "")

	sections := []string{
		"## Project Specification",
		"## Current State (anchor.yaml)",
		"## Existing Tasks",
		"## Instructions",
	}
	for _, s := range sections {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing section %q", s)
		}
	}

	specIdx := strings.Index(prompt, "## Project Specification")
	anchorIdx := strings.Index(prompt, "## Current State")
	tasksIdx := strings.Index(prompt, "## Existing Tasks")
	instrIdx := strings.Index(prompt, "## Instructions")

	if !(specIdx < anchorIdx && anchorIdx < tasksIdx && tasksIdx < instrIdx) {
		t.Error("sections not in expected order: spec < anchor < tasks < instructions")
	}
}

func TestExtractTasksContent_RawFrontmatter(t *testing.T) {
	t.Parallel()
	input := "---\ngenerated_by: corvex\n---\n\n## S01 — Task"
	got := extractTasksContent(input)
	if got != input {
		t.Errorf("extractTasksContent() = %q, want %q", got, input)
	}
}

func TestExtractTasksContent_CodeFenced(t *testing.T) {
	t.Parallel()
	inner := "---\ngenerated_by: corvex\n---\n\n## S01 — Task"
	input := "Here is the file:\n\n```markdown\n" + inner + "\n```"
	got := extractTasksContent(input)
	if got != inner {
		t.Errorf("extractTasksContent() = %q, want %q", got, inner)
	}
}

func TestExtractTasksContent_PlainText(t *testing.T) {
	t.Parallel()
	input := "Just some plain text output without frontmatter or fences"
	got := extractTasksContent(input)
	if got != input {
		t.Errorf("extractTasksContent() = %q, want %q", got, input)
	}
}

func TestExtractTasksContent_CodeFencedNoFrontmatter(t *testing.T) {
	t.Parallel()
	input := "```yaml\nkey: value\n```"
	got := extractTasksContent(input)
	if got != strings.TrimSpace(input) {
		t.Errorf("extractTasksContent() = %q, want %q", got, strings.TrimSpace(input))
	}
}

func TestPlan_ContextCommandInjected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	tasksPath := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(specPath, []byte("Build something"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotPrompt string
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			gotPrompt = req.Prompt
			return &types.ExecuteResult{Output: "---\ndag:\n  S01: []\n---\n\n## S01 — Task ⬜ PENDING\n\n### O que fazer\nx"}, nil
		},
	}

	p := NewPlanner(mock, "test-model", dir, nil, "echo AZURE-CONTEXT-59888")
	if err := p.Plan(context.Background(), specPath, filepath.Join(dir, "anchor.yaml"), tasksPath); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !strings.Contains(gotPrompt, "External Context") {
		t.Error("planner prompt missing the External Context section")
	}
	if !strings.Contains(gotPrompt, "AZURE-CONTEXT-59888") {
		t.Errorf("planner prompt missing the context_command output:\n%s", gotPrompt)
	}
}

func TestPlan_ContextCommandFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	tasksPath := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(specPath, []byte("Build something"), 0o644); err != nil {
		t.Fatal(err)
	}
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "---\ndag:\n  S01: []\n---\n\n## S01 — Task ⬜ PENDING\n\n### O que fazer\nx"}, nil
		},
	}
	// A failing context command must not fail planning.
	p := NewPlanner(mock, "test-model", dir, nil, "exit 3")
	if err := p.Plan(context.Background(), specPath, filepath.Join(dir, "anchor.yaml"), tasksPath); err != nil {
		t.Fatalf("Plan() should tolerate a failing context_command, got %v", err)
	}
}

func TestPlan_WritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	tasksPath := filepath.Join(dir, "output", "tasks.md")

	if err := os.WriteFile(specPath, []byte("Build something"), 0o644); err != nil {
		t.Fatal(err)
	}

	wantContent := "---\ngenerated_by: test\ndag:\n  S01: []\n---\n\n## S01 — Task ⬜ PENDING\n\n### O que fazer\nDo it."
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: wantContent}, nil
		},
	}

	p := NewPlanner(mock, "test-model", dir, nil, "")
	if err := p.Plan(context.Background(), specPath, filepath.Join(dir, "anchor.yaml"), tasksPath); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	data, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatalf("reading tasks file: %v", err)
	}
	if string(data) != wantContent {
		t.Errorf("tasks file = %q, want %q", string(data), wantContent)
	}
}

func TestPlan_AllowedToolsEnforcement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(specPath, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}

	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "---\ngenerated_by: test\ndag:\n  S01: []\n---\n\n## S01 — Task ⬜ PENDING\n\n### O que fazer\nDo it."}, nil
		},
	}

	p := NewPlanner(mock, "test-model", dir, nil, "")
	tasksPath := filepath.Join(dir, "tasks.md")
	if err := p.Plan(context.Background(), specPath, filepath.Join(dir, "anchor.yaml"), tasksPath); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}

	got := mock.calls[0].AllowedTools
	want := []string{"Read", "Glob", "Grep"}
	if len(got) != len(want) {
		t.Fatalf("AllowedTools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllowedTools[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPlan_SpecNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mock := &mockProvider{}

	p := NewPlanner(mock, "test-model", dir, nil, "")
	err := p.Plan(context.Background(), filepath.Join(dir, "missing.md"), "", filepath.Join(dir, "tasks.md"))
	if err == nil {
		t.Fatal("Plan() expected error for missing spec, got nil")
	}
	if !strings.Contains(err.Error(), "reading spec") {
		t.Errorf("error %q should mention 'reading spec'", err)
	}
}

func TestPlan_ProviderError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(specPath, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}

	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return nil, fmt.Errorf("rate limited")
		},
	}

	p := NewPlanner(mock, "test-model", dir, nil, "")
	err := p.Plan(context.Background(), specPath, "", filepath.Join(dir, "tasks.md"))
	if err == nil {
		t.Fatal("Plan() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "planner execution") {
		t.Errorf("error %q should mention 'planner execution'", err)
	}
}

func TestPlan_ModelPassedThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(specPath, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}

	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "---\ngenerated_by: test\ndag:\n  S01: []\n---\n\n## S01 — Task ⬜ PENDING\n\n### O que fazer\nDo it."}, nil
		},
	}

	p := NewPlanner(mock, "opus", dir, nil, "")
	if err := p.Plan(context.Background(), specPath, "", filepath.Join(dir, "tasks.md")); err != nil {
		t.Fatal(err)
	}

	if mock.calls[0].Model != "opus" {
		t.Errorf("Model = %q, want %q", mock.calls[0].Model, "opus")
	}
}
