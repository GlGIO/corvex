package orchestrator

import (
	"strings"
	"testing"
)

func TestParseGrillStep_Question(t *testing.T) {
	t.Parallel()
	output := "Reasoning blah blah.\n\n```grill\n" +
		`{"type":"question","text":"Pick storage format?","recommended":"JSON","rationale":"matches existing tools"}` +
		"\n```\n"

	step, err := parseGrillStep(output)
	if err != nil {
		t.Fatalf("parseGrillStep error = %v", err)
	}
	if step.Done {
		t.Error("expected Done = false")
	}
	if step.Question != "Pick storage format?" {
		t.Errorf("Question = %q", step.Question)
	}
	if step.Recommended != "JSON" {
		t.Errorf("Recommended = %q", step.Recommended)
	}
	if step.Rationale != "matches existing tools" {
		t.Errorf("Rationale = %q", step.Rationale)
	}
}

func TestParseGrillStep_Done(t *testing.T) {
	t.Parallel()
	output := "All clear.\n\n```grill\n{\"type\":\"done\"}\n```"
	step, err := parseGrillStep(output)
	if err != nil {
		t.Fatalf("parseGrillStep error = %v", err)
	}
	if !step.Done {
		t.Error("expected Done = true")
	}
}

func TestParseGrillStep_NoBlock(t *testing.T) {
	t.Parallel()
	_, err := parseGrillStep("just some text without a fence")
	if err == nil {
		t.Fatal("expected error for missing grill block")
	}
}

func TestParseGrillStep_InvalidJSON(t *testing.T) {
	t.Parallel()
	output := "```grill\nnot-json\n```"
	_, err := parseGrillStep(output)
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestParseGrillStep_UnknownType(t *testing.T) {
	t.Parallel()
	output := "```grill\n{\"type\":\"weird\"}\n```"
	_, err := parseGrillStep(output)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestParseGrillStep_QuestionMissingText(t *testing.T) {
	t.Parallel()
	output := "```grill\n{\"type\":\"question\",\"text\":\"\"}\n```"
	_, err := parseGrillStep(output)
	if err == nil {
		t.Fatal("expected error for empty question text")
	}
}

func TestParseGrillStep_LastBlockWins(t *testing.T) {
	t.Parallel()
	output := "first attempt:\n```grill\n{\"type\":\"question\",\"text\":\"old\"}\n```\nreconsidered:\n```grill\n{\"type\":\"done\"}\n```"
	step, err := parseGrillStep(output)
	if err != nil {
		t.Fatalf("parseGrillStep error = %v", err)
	}
	if !step.Done {
		t.Error("expected last block (done) to win")
	}
}

func TestBuildGrillerPrompt_IncludesSpec(t *testing.T) {
	t.Parallel()
	prompt := buildGrillerPrompt("# My spec\nbuild X", "")
	if !strings.Contains(prompt, "# My spec") {
		t.Error("prompt missing spec content")
	}
	if !strings.Contains(prompt, "None yet.") {
		t.Error("prompt should mark decisions as empty when no decisions")
	}
	if !strings.Contains(prompt, "```grill") {
		t.Error("prompt missing grill output format instruction")
	}
}

func TestBuildGrillerPrompt_IncludesDecisions(t *testing.T) {
	t.Parallel()
	prompt := buildGrillerPrompt("# spec", "## Q: foo?\n**A:** bar\n")
	if !strings.Contains(prompt, "**A:** bar") {
		t.Error("prompt missing existing decisions")
	}
	if strings.Contains(prompt, "None yet.") {
		t.Error("prompt should not say 'None yet.' when decisions exist")
	}
}
