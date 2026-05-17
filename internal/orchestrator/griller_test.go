package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
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

func TestParseGrillStep_Reflection(t *testing.T) {
	t.Parallel()

	t.Run("first turn omits reflection", func(t *testing.T) {
		t.Parallel()
		out := "```grill\n" +
			`{"type":"question","text":"Storage?","recommended":"JSON","rationale":"matches tools"}` +
			"\n```"
		step, err := parseGrillStep(out)
		if err != nil {
			t.Fatalf("parseGrillStep: %v", err)
		}
		if step.Reflection != "" {
			t.Errorf("Reflection = %q, want empty on first turn", step.Reflection)
		}
	})

	t.Run("subsequent turns include reflection", func(t *testing.T) {
		t.Parallel()
		out := "```grill\n" +
			`{"type":"question","reflection":"OK — JSON it is.","text":"Pretty-print?","recommended":"Yes","rationale":"diffable"}` +
			"\n```"
		step, err := parseGrillStep(out)
		if err != nil {
			t.Fatalf("parseGrillStep: %v", err)
		}
		if step.Reflection != "OK — JSON it is." {
			t.Errorf("Reflection = %q", step.Reflection)
		}
	})
}

func TestGriller_AskFollowupReturnsPlainText(t *testing.T) {
	t.Parallel()

	mp := &mockProgressProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
				if !strings.Contains(req.Prompt, "User question") {
					t.Errorf("prompt missing User question header")
				}
				return &types.ExecuteResult{Output: "Indexes typically speed reads at write cost."}, nil
			},
		},
	}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(specPath, []byte("# spec\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	g := NewGriller(mp, "sonnet", dir)
	ans, err := g.AskFollowup(context.Background(), specPath, filepath.Join(dir, "decisions.md"), "do I need an index?")
	if err != nil {
		t.Fatalf("AskFollowup error = %v", err)
	}
	if !strings.Contains(ans, "Indexes") {
		t.Errorf("answer = %q", ans)
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
