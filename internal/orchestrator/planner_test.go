package orchestrator

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
	prompt := buildPlannerPrompt("Build a CLI tool", "", "")

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
	prompt := buildPlannerPrompt("spec content", anchorContent, "")

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
	prompt := buildPlannerPrompt("spec content", "", existingTasks)

	if !strings.Contains(prompt, existingTasks) {
		t.Error("prompt missing existing tasks content")
	}
	if strings.Contains(prompt, "No existing tasks") {
		t.Error("prompt should not contain 'No existing tasks' when tasks are provided")
	}
}

func TestBuildPlannerPrompt_Structure(t *testing.T) {
	t.Parallel()
	prompt := buildPlannerPrompt("my spec", "my anchor", "my tasks")

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

func TestPlan_WritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	tasksPath := filepath.Join(dir, "output", "tasks.md")

	if err := os.WriteFile(specPath, []byte("Build something"), 0o644); err != nil {
		t.Fatal(err)
	}

	wantContent := "---\ngenerated_by: test\n---\n\n## S01 — Task"
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: wantContent}, nil
		},
	}

	p := NewPlanner(mock, "test-model", dir)
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
			return &types.ExecuteResult{Output: "---\n---"}, nil
		},
	}

	p := NewPlanner(mock, "test-model", dir)
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

	p := NewPlanner(mock, "test-model", dir)
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

	p := NewPlanner(mock, "test-model", dir)
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
			return &types.ExecuteResult{Output: "---\n---"}, nil
		},
	}

	p := NewPlanner(mock, "opus", dir)
	if err := p.Plan(context.Background(), specPath, "", filepath.Join(dir, "tasks.md")); err != nil {
		t.Fatal(err)
	}

	if mock.calls[0].Model != "opus" {
		t.Errorf("Model = %q, want %q", mock.calls[0].Model, "opus")
	}
}
