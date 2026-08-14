package step

import (
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestBuildWorkerPrompt_BasicTask(t *testing.T) {
	t.Parallel()
	task := &types.Task{
		ID:          "S01",
		Title:       "Basic Task",
		Description: "Do something basic",
	}

	prompt := buildWorkerPrompt(task, "", nil, "", "", "")

	if !strings.Contains(prompt, "## Current Task: S01 — Basic Task") {
		t.Error("prompt missing current task header")
	}
	if !strings.Contains(prompt, "Do something basic") {
		t.Error("prompt missing description")
	}
	if strings.Contains(prompt, "## Agent Instructions") {
		t.Error("prompt should not contain agent instructions when empty")
	}
	if strings.Contains(prompt, "## Project Context") {
		t.Error("prompt should not contain project context when empty")
	}
	if strings.Contains(prompt, "## Previous Work") {
		t.Error("prompt should not contain previous work when empty")
	}
	if strings.Contains(prompt, "## Previous Attempt Failed") {
		t.Error("prompt should not contain diagnosis when empty")
	}
}

func TestBuildWorkerPrompt_WithAnchorContext(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S02", Title: "Task", Description: "desc"}
	anchor := "## Completed Work\n\n### S01 — First\nDone."

	prompt := buildWorkerPrompt(task, anchor, nil, "", "", "")

	if !strings.Contains(prompt, "## Previous Work") {
		t.Error("prompt missing previous work section")
	}
	if !strings.Contains(prompt, anchor) {
		t.Error("prompt missing anchor context content")
	}
}

func TestBuildWorkerPrompt_WithContextDocs(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}
	docs := []string{"doc1 content", "doc2 content"}

	prompt := buildWorkerPrompt(task, "", docs, "", "", "")

	if !strings.Contains(prompt, "## Project Context") {
		t.Error("prompt missing project context section")
	}
	if !strings.Contains(prompt, "doc1 content") {
		t.Error("prompt missing first doc")
	}
	if !strings.Contains(prompt, "doc2 content") {
		t.Error("prompt missing second doc")
	}
}

func TestBuildWorkerPrompt_WithAgentPrompt(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}
	agent := "You are a database specialist."

	prompt := buildWorkerPrompt(task, "", nil, agent, "", "")

	if !strings.Contains(prompt, "## Agent Instructions") {
		t.Error("prompt missing agent instructions section")
	}
	if !strings.Contains(prompt, agent) {
		t.Error("prompt missing agent prompt content")
	}
}

func TestBuildWorkerPrompt_WithDiagnosis(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}
	diag := "Missing import for fmt package"

	prompt := buildWorkerPrompt(task, "", nil, "", diag, "")

	if !strings.Contains(prompt, "## Previous Attempt Failed") {
		t.Error("prompt missing diagnosis section")
	}
	if !strings.Contains(prompt, diag) {
		t.Error("prompt missing diagnosis content")
	}
}

func TestBuildWorkerPrompt_AllCombined(t *testing.T) {
	t.Parallel()
	task := &types.Task{
		ID:          "S03",
		Title:       "Full Task",
		Description: "Complete implementation",
		Criteria:    []string{"Tests pass", "No lint errors"},
		Files: types.TaskFiles{
			Create: []string{"new.go"},
			Modify: []string{"existing.go"},
		},
	}

	prompt := buildWorkerPrompt(task, "anchor ctx", []string{"doc1"}, "agent prompt", "prev error", "")

	if strings.Contains(prompt, "Required Skill") {
		t.Error("prompt should not mention a skill when none is routed")
	}

	expectedOrder := []string{
		"## Agent Instructions",
		"## Project Context",
		"## Previous Work",
		"## Current Task: S03",
		"### Description",
		"### Success Criteria",
		"### Files",
		"## Previous Attempt Failed",
		"## Instructions",
	}

	lastIdx := -1
	for _, section := range expectedOrder {
		idx := strings.Index(prompt, section)
		if idx == -1 {
			t.Errorf("prompt missing section %q", section)
			continue
		}
		if idx <= lastIdx {
			t.Errorf("section %q at index %d is not after previous section at index %d", section, idx, lastIdx)
		}
		lastIdx = idx
	}

	if !strings.Contains(prompt, "- [ ] Tests pass") {
		t.Error("prompt missing criterion checkbox")
	}
	if !strings.Contains(prompt, "- Create: new.go") {
		t.Error("prompt missing create file")
	}
	if !strings.Contains(prompt, "- Modify: existing.go") {
		t.Error("prompt missing modify file")
	}
}

func TestBuildWorkerPrompt_RoutedSkill(t *testing.T) {
	task := &types.Task{ID: "S01", Title: "Build UI", Type: types.TypeFrontend, Description: "do it"}
	prompt := buildWorkerPrompt(task, "", nil, "", "", "frontend-design")
	if !strings.Contains(prompt, "Required Skill") {
		t.Error("prompt missing the Required Skill section for a routed task")
	}
	if !strings.Contains(prompt, "frontend-design") {
		t.Error("prompt should name the routed skill")
	}
}
